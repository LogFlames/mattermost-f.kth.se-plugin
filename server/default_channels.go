package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

const defaultChannelJobPrefix = "default_channel_add_"
const defaultChannelDescription = "Default channels automatically add new team members. Setting a channel as default also adds all current team members."

// Keep the existing custom setting's format. String1 is a legacy label, not a
// sidebar category override: Mattermost's channel.DefaultCategoryName owns that.
type defaultChannelEntry struct {
	String1    string
	ChannelIDs []string
}

type defaultChannelJob struct {
	TeamID, ChannelID           string
	RequesterID, ReplyChannelID string
	Page                        int
	Scanned                     bool
	Pending                     []string
	NextAttempt                 int64
}

func (c *configuration) defaultChannelIDs() []string {
	var ids []string
	for _, entry := range c.DefaultChannels_Custom {
		for _, id := range entry.ChannelIDs {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func (p *Plugin) defaultChannelCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	fields := strings.Fields(args.Command)
	if args.TeamId == "" {
		response.Text = "Run /default_channel from a team."
	} else if !p.API.HasPermissionToTeam(args.UserId, args.TeamId, model.PermissionManageTeam) {
		response.Text = "Only team admins can run /default_channel."
	} else if len(fields) != 2 || (fields[1] != "set" && fields[1] != "unset" && fields[1] != "list") {
		response.Text = "Usage: /default_channel set|unset|list. Set and unset apply to the current channel."
	} else {
		// Serialize config read/modify/write and membership work across instances.
		// Do not hold configurationLock: SavePluginConfig invokes its change hook.
		lock, err := cluster.NewMutex(p.API, "default_channels")
		if err != nil {
			return nil, model.NewAppError("defaultChannelCommand", "plugin.default_channels.lock", nil, err.Error(), http.StatusInternalServerError)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := lock.LockWithContext(ctx); err != nil {
			response.Text = "Default channels are busy. Please try again."
			return response, nil
		}
		defer lock.Unlock()
		// Read persisted settings, not a potentially stale hook snapshot.
		settings := maps.Clone(p.API.GetPluginConfig())
		var config configuration
		data, err := json.Marshal(settings)
		if err == nil {
			err = json.Unmarshal(data, &config)
		}
		if err != nil {
			response.Text = "Could not read default channel configuration."
		} else if !config.DefaultChannels_OnOffBool {
			response.Text = "The Default Channels module is disabled. Ask a system admin to enable it in the plugin settings."
		} else if fields[1] == "list" {
			return p.listDefaultChannels(args.TeamId, &config)
		} else {
			return p.setDefaultChannel(args, fields[1] == "set", settings, &config)
		}
	}
	return response, nil
}

func (p *Plugin) listDefaultChannels(teamID string, config *configuration) (*model.CommandResponse, *model.AppError) {
	ids := config.defaultChannelIDs()
	townSquare, err := p.API.GetChannelByName(teamID, model.DefaultChannelName, false)
	if err != nil && err.StatusCode != http.StatusNotFound {
		return nil, err
	}
	if townSquare != nil && !slices.Contains(ids, townSquare.Id) {
		ids = append(ids, townSquare.Id)
	}
	var lines []string
	var builtInChannel string
	for _, id := range ids {
		channel, err := p.API.GetChannel(id)
		if err != nil {
			if err.StatusCode == http.StatusNotFound {
				continue
			}
			return nil, err
		}
		if !eligibleDefaultChannel(channel, teamID) {
			continue
		}
		category := channel.DefaultCategoryName
		if category == "" {
			category = "Channels"
		}
		category = strings.NewReplacer("\\", "\\\\", "*", "\\*", "_", "\\_", "`", "\\`", "[", "\\[", "]", "\\]", "\n", " ", "\r", " ").Replace(category)
		entry := fmt.Sprintf("~%s (category: %s)", channel.Name, category)
		if channel.Name == model.DefaultChannelName {
			builtInChannel = "**Default channel (town-square):** " + entry
		} else {
			lines = append(lines, "* "+entry)
		}
	}
	sort.Strings(lines)
	text := defaultChannelDescription + "\n\nNo default channels are configured for this team."
	if len(lines) > 0 {
		text = defaultChannelDescription + "\n\n" + strings.Join(lines, "\n")
	}
	if builtInChannel != "" {
		text += "\n\n" + builtInChannel
	}
	return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: text}, nil
}

func eligibleDefaultChannel(channel *model.Channel, teamID string) bool {
	return channel.TeamId == teamID && channel.DeleteAt == 0 &&
		(channel.Type == model.ChannelTypeOpen || channel.Type == model.ChannelTypePrivate)
}

func (p *Plugin) setDefaultChannel(args *model.CommandArgs, set bool, settings map[string]any, config *configuration) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	channel, err := p.API.GetChannel(args.ChannelId)
	if err != nil {
		return nil, err
	}
	if !eligibleDefaultChannel(channel, args.TeamId) {
		response.Text = "Use this command in an active public or private channel in this team."
		return response, nil
	}
	exists := slices.Contains(config.defaultChannelIDs(), channel.Id)
	if exists == set {
		if set {
			response.Text = "This channel is already a default channel. No bulk addition was started."
		} else {
			response.Text = "This channel is not a default channel."
		}
		return response, nil
	}
	// Re-setting after an unset replaces any unfinished bulk pass rather than
	// resurrecting it alongside a second pass.
	key := defaultChannelJobPrefix + "bulk_" + channel.Id
	if set {
		// Persist the job first; the worker takes the same lock and verifies the
		// saved config. Failed config saves therefore cannot add any members.
		job := &defaultChannelJob{TeamID: args.TeamId, ChannelID: channel.Id, RequesterID: args.UserId, ReplyChannelID: args.ChannelId}
		if err := p.saveDefaultChannelJob(key, job); err != nil {
			return nil, err
		}
		config.DefaultChannels_Custom = append(config.DefaultChannels_Custom, defaultChannelEntry{ChannelIDs: []string{channel.Id}})
	} else {
		for i := range config.DefaultChannels_Custom {
			config.DefaultChannels_Custom[i].ChannelIDs = slices.DeleteFunc(config.DefaultChannels_Custom[i].ChannelIDs, func(id string) bool { return id == channel.Id })
		}
	}
	// The System Console lowercases schema keys. Remove other spellings to
	// avoid ambiguous duplicate keys when LoadPluginConfiguration folds case.
	for key := range settings {
		if strings.EqualFold(key, "defaultchannels_custom") {
			delete(settings, key)
		}
	}
	// Plugin RPC only registers generic JSON containers for interface values,
	// not this plugin's Go structs. Keep the wire format identical to config.json.
	entries := make([]any, len(config.DefaultChannels_Custom))
	for i, entry := range config.DefaultChannels_Custom {
		ids := make([]any, len(entry.ChannelIDs))
		for j, id := range entry.ChannelIDs {
			ids[j] = id
		}
		entries[i] = map[string]any{"String1": entry.String1, "ChannelIDs": ids}
	}
	settings["defaultchannels_custom"] = entries
	if err := p.API.SavePluginConfig(settings); err != nil {
		if set {
			if deleteErr := p.API.KVDelete(key); deleteErr != nil {
				p.API.LogError("Delete uncommitted default channel job failed", "error", deleteErr.Error())
			}
		}
		return nil, err
	}
	if set {
		response.Text = "This channel is now a default channel. Adding current team members in the background; you'll receive a completion message here. New team members will be added automatically."
	} else {
		response.Text = "This channel is no longer a default channel. Existing members have not been removed."
	}
	return response, nil
}

func (p *Plugin) saveDefaultChannelJob(key string, job *defaultChannelJob) *model.AppError {
	data, err := json.Marshal(job)
	if err != nil {
		return model.NewAppError("saveDefaultChannelJob", "plugin.default_channels.encode", nil, err.Error(), http.StatusInternalServerError)
	}
	return p.API.KVSet(key, data)
}

func (p *Plugin) UserHasJoinedTeam(_ *plugin.Context, member *model.TeamMember, _ *model.User) {
	config := p.getConfiguration()
	if !config.DefaultChannels_OnOffBool || member.DeleteAt != 0 {
		return
	}
	for _, id := range config.defaultChannelIDs() {
		// Eligibility is checked by the worker; a failed lookup must not lose
		// this join event. Scanned means only this member needs processing.
		job := &defaultChannelJob{TeamID: member.TeamId, ChannelID: id, Scanned: true, Pending: []string{member.UserId}}
		if err := p.saveDefaultChannelJob(defaultChannelJobPrefix+model.NewId(), job); err != nil {
			p.API.LogError("Queue default channels for team join failed", "team_id", member.TeamId, "user_id", member.UserId, "error", err.Error())
		}
	}
}

func (p *Plugin) runDefaultChannelJobs(ctx context.Context) {
	// Snapshot keys before deletions so offset pagination cannot skip jobs.
	var keys []string
	for page := 0; ; page++ {
		batch, err := p.API.KVList(page, 100)
		if err != nil {
			p.API.LogError("List default channel jobs failed", "error", err.Error())
			return
		}
		for _, key := range batch {
			if strings.HasPrefix(key, defaultChannelJobPrefix) {
				keys = append(keys, key)
			}
		}
		if len(batch) < 100 {
			break
		}
	}
	for _, key := range keys {
		if ctx.Err() != nil {
			return
		}
		if err := p.processDefaultChannelJob(ctx, key); err != nil {
			p.API.LogError("Default channel addition will retry", "key", key, "error", err.Error())
		}
	}
}

func (p *Plugin) processDefaultChannelJob(ctx context.Context, key string) error {
	lock, err := cluster.NewMutex(p.API, "default_channels")
	if err != nil {
		return err
	}
	if err := lock.LockWithContext(ctx); err != nil {
		return err
	}
	defer lock.Unlock()
	data, appErr := p.API.KVGet(key)
	if appErr != nil {
		return appErr
	}
	if len(data) == 0 {
		return nil
	}
	var job defaultChannelJob
	if err := json.Unmarshal(data, &job); err != nil {
		return err
	}
	if time.Now().UnixMilli() < job.NextAttempt {
		return nil
	}
	// Read the current persisted settings under the command's lock, including
	// after a restart or on a different node before its config hook has run.
	configData, err := json.Marshal(p.API.GetPluginConfig())
	if err != nil {
		return err
	}
	var config configuration
	if err := json.Unmarshal(configData, &config); err != nil {
		return err
	}
	if !config.DefaultChannels_OnOffBool {
		return nil // Pause queued work while the module is disabled.
	}
	channel, appErr := p.API.GetChannel(job.ChannelID)
	if appErr != nil && appErr.StatusCode != http.StatusNotFound {
		return appErr
	}
	if appErr != nil || !eligibleDefaultChannel(channel, job.TeamID) || !slices.Contains(config.defaultChannelIDs(), job.ChannelID) {
		if err := p.API.KVDelete(key); err != nil {
			return err
		}
		return nil
	}
	if !job.Scanned {
		users, err := p.API.GetUsersInTeam(job.TeamID, job.Page, 100)
		if err != nil {
			return err
		}
		for _, user := range users {
			if user.DeleteAt == 0 && !slices.Contains(job.Pending, user.Id) {
				job.Pending = append(job.Pending, user.Id)
			}
		}
		job.Page++
		job.Scanned = len(users) < 100
		if err := p.saveDefaultChannelJob(key, &job); err != nil {
			return err
		}
	}
	// Bound each pass so failed users don't block later pages or commands.
	pending := append([]string(nil), job.Pending...)
	for _, userID := range pending[:min(100, len(pending))] {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := p.addDefaultChannelMember(&job, userID); err != nil {
			p.API.LogError("Add default channel member failed", "channel_id", job.ChannelID, "user_id", userID, "error", err.Error())
			// Rotate failed users behind members not yet attempted.
			job.Pending = append(slices.DeleteFunc(job.Pending, func(id string) bool { return id == userID }), userID)
		} else {
			job.Pending = slices.DeleteFunc(job.Pending, func(id string) bool { return id == userID })
		}
		if err := p.saveDefaultChannelJob(key, &job); err != nil {
			return err
		}
	}
	if job.Scanned && len(job.Pending) == 0 {
		if job.RequesterID != "" {
			if p.API.SendEphemeralPost(job.RequesterID, &model.Post{
				UserId: p.pluginBot.UserId, ChannelId: job.ReplyChannelID,
				Message: "Default channel setup complete. All current active team members have been added to ~" + channel.Name + ".",
			}) == nil {
				return fmt.Errorf("could not deliver default channel completion message")
			}
		}
		if err := p.API.KVDelete(key); err != nil {
			return err
		}
		return nil
	}
	if job.Scanned {
		job.NextAttempt = time.Now().Add(30 * time.Second).UnixMilli()
	}
	if err := p.saveDefaultChannelJob(key, &job); err != nil {
		return err
	}
	return nil
}

func (p *Plugin) addDefaultChannelMember(job *defaultChannelJob, userID string) *model.AppError {
	member, err := p.API.GetTeamMember(job.TeamID, userID)
	if err != nil {
		if err.StatusCode == http.StatusNotFound {
			return nil
		}
		return err
	}
	if member.DeleteAt != 0 {
		return nil
	}
	user, err := p.API.GetUser(userID)
	if err != nil {
		if err.StatusCode == http.StatusNotFound {
			return nil
		}
		return err
	}
	if user.DeleteAt != 0 {
		return nil
	}
	_, err = p.API.AddUserToChannel(job.ChannelID, userID, p.pluginBot.UserId)
	return err
}
