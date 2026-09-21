package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

const (
	categoryJobPrefix = "category_sync_"
	categoryPageSize  = 100
)

// Each proposed change has its own key: hooks must not overwrite a worker's
// progress or another concurrent channel update. No credentials are stored here.
type categoryJob struct {
	ChannelID   string `json:"channel_id"`
	OldName     string `json:"old_name"`
	NewName     string `json:"new_name"`
	OldUpdateAt int64  `json:"old_update_at"`
	CreatedAt   int64  `json:"created_at"`
	Confirmed   bool   `json:"confirmed"`
	NextAttempt int64  `json:"next_attempt"`
	Attempts    int    `json:"attempts"`
	Page        int    `json:"page"`
	Scanned     bool   `json:"scanned"`
	// A failed user remains here while subsequent members/pages are processed.
	// Save source IDs before moving so deletion can be retried after a crash.
	Pending map[string][]string `json:"pending"`
}

func (p *Plugin) ChannelWillBeUpdated(_ *plugin.Context, newChannel, oldChannel *model.Channel) (*model.Channel, string) {
	if newChannel.DefaultCategoryName == oldChannel.DefaultCategoryName || newChannel.TeamId == "" ||
		(newChannel.Type != model.ChannelTypeOpen && newChannel.Type != model.ChannelTypePrivate) {
		return nil, ""
	}
	job := &categoryJob{
		ChannelID: newChannel.Id, OldName: oldChannel.DefaultCategoryName, NewName: newChannel.DefaultCategoryName,
		OldUpdateAt: oldChannel.UpdateAt, CreatedAt: time.Now().UnixMilli(), Pending: make(map[string][]string),
	}
	if err := p.saveCategoryJob(categoryJobPrefix+model.NewId(), job); err != nil {
		p.API.LogError("Cannot queue default category synchronization", "channel_id", newChannel.Id, "error", err.Error())
		return nil, "Could not schedule the category update. Please try again."
	}
	return nil, ""
}

func (p *Plugin) saveCategoryJob(key string, job *categoryJob) error {
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	if appErr := p.API.KVSet(key, data); appErr != nil {
		return appErr
	}
	return nil
}

func (p *Plugin) categorySiteURL() (string, error) {
	siteURL := p.API.GetConfig().ServiceSettings.SiteURL
	if siteURL == nil || *siteURL == "" {
		return "", errors.New("SiteURL is required for category deletion")
	}
	u, err := url.Parse(*siteURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("SiteURL must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	return strings.TrimRight(*siteURL, "/"), nil
}

func (p *Plugin) startCategoryWorker() error {
	if _, err := p.categorySiteURL(); err != nil {
		return err
	}
	lock, err := cluster.NewMutex(p.API, "category_worker")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.categoryCancel = cancel
	p.categoryDone = make(chan struct{})
	go func() {
		defer close(p.categoryDone)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Serialize all sidebar writes, including different channels for the
				// same user/team, across every instance of this plugin.
				if err := lock.LockWithContext(ctx); err != nil {
					return
				}
				p.runCategoryJobs(ctx)
				lock.Unlock()
			}
		}
	}()
	return nil
}

func (p *Plugin) OnDeactivate() error {
	if p.categoryCancel != nil {
		p.categoryCancel()
		<-p.categoryDone
	}
	return nil
}

func (p *Plugin) runCategoryJobs(ctx context.Context) {
	// Snapshot the keys before deleting any: deleting while using offset-based
	// KV pagination would skip entries. New hooks are picked up on the next tick.
	var keys []string
	for page := 0; ; page++ {
		batch, err := p.API.KVList(page, categoryPageSize)
		if err != nil {
			p.API.LogError("List category jobs failed", "error", err.Error())
			return
		}
		for _, key := range batch {
			if strings.HasPrefix(key, categoryJobPrefix) {
				keys = append(keys, key)
			}
		}
		if len(batch) < categoryPageSize {
			break
		}
	}
	rest := &categoryREST{plugin: p}
	defer rest.close()
	for _, key := range keys {
		if ctx.Err() != nil {
			return
		}
		data, appErr := p.API.KVGet(key)
		if appErr != nil {
			p.API.LogError("Read category job failed", "error", appErr.Error())
			continue
		}
		if len(data) == 0 {
			continue
		}
		var job categoryJob
		if err := json.Unmarshal(data, &job); err != nil {
			p.API.LogError("Invalid category job", "key", key, "error", err.Error())
			continue
		}
		if time.Now().UnixMilli() < job.NextAttempt {
			continue
		}
		done, err := p.runCategoryJob(ctx, rest, key, &job)
		if err != nil {
			p.API.LogError("Category synchronization will retry", "channel_id", job.ChannelID, "error", err.Error())
			job.Attempts++
			job.NextAttempt = time.Now().Add(time.Second * time.Duration(1<<min(job.Attempts, 8))).UnixMilli()
			if saveErr := p.saveCategoryJob(key, &job); saveErr != nil {
				p.API.LogError("Save category retry failed", "error", saveErr.Error())
			}
		} else if done {
			if appErr := p.API.KVDelete(key); appErr != nil {
				p.API.LogError("Delete completed category job failed", "error", appErr.Error())
			}
		}
	}
}

func (p *Plugin) runCategoryJob(ctx context.Context, rest *categoryREST, key string, job *categoryJob) (bool, error) {
	channel, appErr := p.API.GetChannel(job.ChannelID)
	if appErr != nil {
		if appErr.StatusCode == http.StatusNotFound {
			return true, nil
		}
		return false, appErr
	}
	if channel.DeleteAt != 0 {
		return true, nil
	}
	if !job.Confirmed {
		// The hook runs BEFORE persistence, and another plugin can still reject
		// the change. Never use a proposed category as if it were committed.
		// Two successful updates can share a millisecond timestamp.
		if channel.DefaultCategoryName != job.NewName || channel.UpdateAt < job.OldUpdateAt {
			return time.Now().UnixMilli()-job.CreatedAt > time.Minute.Milliseconds(), nil
		}
		job.Confirmed = true
	}
	if channel.DefaultCategoryName != job.NewName {
		// A confirmed job may have partially run before a newer change. Resume
		// against the latest committed default, retaining pending cleanup IDs.
		job.NewName = channel.DefaultCategoryName
		job.Page = 0
		job.Scanned = false
	}
	if err := p.saveCategoryJob(key, job); err != nil {
		return false, err
	}

	// Retry failed users first. Take a snapshot because successful users are
	// removed from Pending and newly fetched members are added below.
	users := make([]string, 0, len(job.Pending))
	for userID := range job.Pending {
		users = append(users, userID)
	}
	var failures error
	process := func(userID string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		current, err := p.API.GetChannel(job.ChannelID)
		if err != nil {
			return err
		}
		if current.DefaultCategoryName != job.NewName || current.DeleteAt != 0 {
			return errors.New("channel changed during category synchronization")
		}
		if err := p.syncMemberCategory(ctx, rest, key, job, channel.TeamId, userID); err != nil {
			failures = errors.Join(failures, fmt.Errorf("user %s: %w", userID, err))
			return nil
		}
		delete(job.Pending, userID)
		return p.saveCategoryJob(key, job)
	}
	for _, userID := range users {
		if err := process(userID); err != nil {
			return false, err
		}
	}
	if job.Scanned {
		return len(job.Pending) == 0, failures
	}
	for {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		members, err := p.API.GetChannelMembers(job.ChannelID, job.Page, categoryPageSize)
		if err != nil {
			return false, err
		}
		for _, member := range members {
			if _, pending := job.Pending[member.UserId]; pending {
				continue
			}
			job.Pending[member.UserId] = nil
			// Record the member before any sidebar mutation.
			if err := p.saveCategoryJob(key, job); err != nil {
				return false, err
			}
			if err := process(member.UserId); err != nil {
				return false, err
			}
		}
		if len(members) < categoryPageSize {
			job.Scanned = true
			if err := p.saveCategoryJob(key, job); err != nil {
				return false, err
			}
			return len(job.Pending) == 0, failures
		}
		job.Page++
		if err := p.saveCategoryJob(key, job); err != nil {
			return false, err
		}
	}
}

func (p *Plugin) syncMemberCategory(ctx context.Context, rest *categoryREST, key string, job *categoryJob, teamID, userID string) error {
	if _, err := p.API.GetChannelMember(job.ChannelID, userID); err != nil {
		if err.StatusCode == http.StatusNotFound {
			return nil // The member left while the job was queued.
		}
		return err
	}
	categories, appErr := p.API.GetChannelSidebarCategories(userID, teamID)
	if appErr != nil {
		return appErr
	}
	var target *model.SidebarCategoryWithChannels
	for _, category := range categories.Categories {
		if (job.NewName == "" && category.Type == model.SidebarCategoryChannels) ||
			(job.NewName != "" && category.Type == model.SidebarCategoryCustom && strings.EqualFold(category.DisplayName, job.NewName)) {
			target = category
			break
		}
	}
	if target == nil {
		if job.NewName == "" {
			return errors.New("built-in Channels category is missing")
		}
		// Create an empty destination, then move both sides in a single update.
		target, appErr = p.API.CreateChannelSidebarCategory(userID, teamID, &model.SidebarCategoryWithChannels{
			SidebarCategory: model.SidebarCategory{UserId: userID, TeamId: teamID, Type: model.SidebarCategoryCustom, DisplayName: job.NewName},
			Channels:        []string{},
		})
		if appErr != nil {
			return appErr
		}
	}
	var updates []*model.SidebarCategoryWithChannels
	for _, category := range categories.Categories {
		if category.Id == target.Id {
			continue
		}
		contains := slices.Contains(category.Channels, job.ChannelID)
		if category.Type == model.SidebarCategoryCustom && (contains || strings.EqualFold(category.DisplayName, job.OldName)) &&
			!slices.Contains(job.Pending[userID], category.Id) {
			job.Pending[userID] = append(job.Pending[userID], category.Id)
		}
		if contains {
			category.Channels = slices.DeleteFunc(category.Channels, func(id string) bool { return id == job.ChannelID })
			updates = append(updates, category)
		}
	}
	if !slices.Contains(target.Channels, job.ChannelID) {
		target.Channels = append([]string{job.ChannelID}, target.Channels...)
		updates = append(updates, target)
	}
	if err := p.saveCategoryJob(key, job); err != nil {
		return err
	}
	if len(updates) != 0 {
		if _, appErr = p.API.UpdateChannelSidebarCategories(userID, teamID, updates); appErr != nil {
			return appErr
		}
	}
	// Re-read after the move: do not delete based on the earlier snapshot.
	categories, appErr = p.API.GetChannelSidebarCategories(userID, teamID)
	if appErr != nil {
		return appErr
	}
	for _, category := range categories.Categories {
		if category.Id == target.Id || category.Type != model.SidebarCategoryCustom ||
			!slices.Contains(job.Pending[userID], category.Id) {
			continue
		}
		// Sidebar categories include archived IDs even though the sidebar hides
		// them. Deleting their category removes assignments, not the channels;
		// restored channels will fall back to the built-in Channels category.
		hasActiveChannel := false
		for _, channelID := range category.Channels {
			channel, err := p.API.GetChannel(channelID)
			if err != nil {
				return err // Keep the category when its contents cannot be verified.
			}
			if channel.DeleteAt == 0 {
				hasActiveChannel = true
				break
			}
		}
		if !hasActiveChannel {
			if err := rest.deleteCategory(ctx, userID, teamID, category.Id); err != nil {
				return err
			}
		}
	}
	return nil
}

// REST is needed only for deletion. Sessions expire even if the plugin crashes;
// normal shutdown and job completion explicitly revoke them.
type categoryREST struct {
	plugin  *Plugin
	client  *model.Client4
	session *model.Session
}

func (r *categoryREST) close() {
	if r.session != nil {
		if err := r.plugin.API.RevokeSession(r.session.Id); err != nil {
			r.plugin.API.LogError("Revoke category bot session failed", "error", err.Error())
		}
		r.session = nil
		r.client = nil
	}
}

func (r *categoryREST) deleteCategory(ctx context.Context, userID, teamID, categoryID string) error {
	if r.session == nil || r.session.ExpiresAt < time.Now().Add(time.Minute).UnixMilli() {
		r.close()
		siteURL, err := r.plugin.categorySiteURL()
		if err != nil {
			return err
		}
		user, appErr := r.plugin.API.GetUser(r.plugin.pluginBot.UserId)
		if appErr != nil {
			return appErr
		}
		session := &model.Session{UserId: user.Id, Roles: user.GetRawRoles(), ExpiresAt: time.Now().Add(10 * time.Minute).UnixMilli()}
		session.AddProp(model.SessionPropIsBot, model.SessionPropIsBotValue)
		r.session, appErr = r.plugin.API.CreateSession(session)
		if appErr != nil {
			return appErr
		}
		r.client = model.NewAPIv4Client(siteURL)
		r.client.SetToken(r.session.Token)
		r.client.HTTPClient = &http.Client{
			Timeout:       15 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	_, err := r.client.DeleteSidebarCategoryForTeamForUser(ctx, userID, teamID, categoryID)
	var appErr *model.AppError
	if errors.As(err, &appErr) && appErr.StatusCode == http.StatusNotFound {
		return nil // A concurrent user action may already have deleted it.
	}
	return err
}
