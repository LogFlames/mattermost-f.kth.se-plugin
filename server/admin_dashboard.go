package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

const adminPreviewPrefix = "admin_preview_"

type adminInput struct {
	Action    string `json:"action"`
	TeamID    string `json:"team_id"`
	ChannelID string `json:"channel_id"`
	Email     string `json:"email"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type adminChange struct {
	Kind      string `json:"kind"`
	UserID    string `json:"user_id,omitempty"`
	Username  string `json:"username,omitempty"`
	ChannelID string `json:"channel_id,omitempty"`
	Channel   string `json:"channel,omitempty"`
	Before    string `json:"before"`
	After     string `json:"after"`
}

type adminPreview struct {
	ID        string        `json:"id,omitempty"`
	ExpiresAt int64         `json:"expires_at,omitempty"`
	Summary   string        `json:"summary"`
	Warnings  []string      `json:"warnings"`
	Changes   []adminChange `json:"changes"`
}

type adminPlan struct {
	UserID      string     `json:"user_id"`
	ExpiresAt   int64      `json:"expires_at"`
	Input       adminInput `json:"input"`
	Fingerprint string     `json:"fingerprint"`
}

func adminError(status int, message string) *model.AppError {
	return model.NewAppError("adminDashboard", "plugin.admin_dashboard", nil, message, status)
}

func (p *Plugin) serveAdminDashboard(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, message string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
	}
	userID := r.Header.Get("Mattermost-User-Id")
	if userID == "" {
		fail(http.StatusUnauthorized, "Sign in to use the admin dashboard.")
		return
	}
	if !p.API.HasPermissionTo(userID, model.PermissionManageSystem) {
		fail(http.StatusForbidden, "Only system admins can access the admin dashboard.")
		return
	}
	if r.URL.Path == "/admin/options" && r.Method == http.MethodGet {
		options, err := p.adminOptions(r.Context(), userID, r.URL.Query().Get("team_id"))
		if err != nil {
			fail(err.StatusCode, err.DetailedError)
			return
		}
		_ = json.NewEncoder(w).Encode(options)
		return
	}
	if r.URL.Path != "/admin/preview" && r.URL.Path != "/admin/execute" {
		fail(http.StatusNotFound, "Not found.")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		fail(http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	var request struct {
		adminInput
		PreviewID string `json:"preview_id"`
		Password  string `json:"password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		fail(http.StatusBadRequest, "Invalid dashboard request.")
		return
	}
	if r.URL.Path == "/admin/preview" {
		preview, err := p.buildAdminPreview(r.Context(), userID, request.adminInput)
		if err != nil {
			fail(err.StatusCode, err.DetailedError)
			return
		}
		preview.ID = model.NewId()
		preview.ExpiresAt = time.Now().Add(5 * time.Minute).UnixMilli()
		plan := adminPlan{UserID: userID, ExpiresAt: preview.ExpiresAt, Input: request.adminInput, Fingerprint: adminFingerprint(preview)}
		data, _ := json.Marshal(plan)
		if err := p.API.KVSetWithExpiry(adminPreviewPrefix+preview.ID, data, 300); err != nil {
			fail(http.StatusInternalServerError, "Could not save the preview. Nothing was changed.")
			return
		}
		_ = json.NewEncoder(w).Encode(preview)
		return
	}
	if !model.IsValidId(request.PreviewID) {
		fail(http.StatusBadRequest, "Review a preview before applying changes.")
		return
	}
	key := adminPreviewPrefix + request.PreviewID
	data, appErr := p.API.KVGet(key)
	if appErr != nil {
		fail(http.StatusInternalServerError, "Could not load the preview.")
		return
	}
	var plan adminPlan
	if json.Unmarshal(data, &plan) != nil || plan.UserID != userID || time.Now().UnixMilli() >= plan.ExpiresAt {
		fail(http.StatusConflict, "This preview expired or belongs to another user. Generate a new preview.")
		return
	}
	// Default-channel commands and the worker use this same config lock.
	if plan.Input.Action == "default_set" || plan.Input.Action == "default_unset" {
		lock, err := cluster.NewMutex(p.API, "default_channels")
		if err != nil {
			fail(http.StatusInternalServerError, "Could not lock default channels.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := lock.LockWithContext(ctx); err != nil {
			fail(http.StatusServiceUnavailable, "Default channels are busy. Please try again.")
			return
		}
		defer lock.Unlock()
	}
	preview, err := p.buildAdminPreview(r.Context(), userID, plan.Input)
	if err != nil {
		fail(err.StatusCode, err.DetailedError)
		return
	}
	if adminFingerprint(preview) != plan.Fingerprint {
		fail(http.StatusConflict, "The affected state changed. Nothing was applied. Generate a new preview.")
		return
	}
	if plan.Input.Action == "keycloak_new_user" && strings.TrimSpace(request.Password) == "" {
		fail(http.StatusBadRequest, "Enter a temporary password before creating the account.")
		return
	}
	// A lost HTTP response must not allow the same confirmation to run twice.
	consumed, consumeErr := p.API.KVSetWithOptions(key, nil, model.PluginKVSetOptions{Atomic: true, OldValue: data})
	if consumeErr != nil || !consumed {
		fail(http.StatusConflict, "This preview was already used. Generate a new preview before retrying.")
		return
	}
	result := p.applyAdminPreview(r.Context(), userID, plan.Input, request.Password, preview)
	p.API.LogInfo("Admin dashboard action applied", "action", plan.Input.Action, "requester_id", userID, "team_id", plan.Input.TeamID, "result", result)
	_ = json.NewEncoder(w).Encode(result)
}

func adminFingerprint(preview *adminPreview) string {
	copy := *preview
	copy.ID, copy.ExpiresAt = "", 0
	data, _ := json.Marshal(copy)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (p *Plugin) adminCanManageChannel(userID string, channel *model.Channel) bool {
	permission := model.PermissionManagePublicChannelMembers
	if channel.Type == model.ChannelTypePrivate {
		permission = model.PermissionManagePrivateChannelMembers
	}
	return p.API.HasPermissionToChannel(userID, channel.Id, permission)
}

func (p *Plugin) adminChannels(ctx context.Context, userID, teamID string) ([]*model.Channel, error) {
	if !p.API.HasPermissionToTeam(userID, teamID, model.PermissionManageTeam) {
		channels, err := p.API.GetChannelsForTeamForUser(teamID, userID, false)
		if err != nil {
			return nil, err
		}
		return channels, nil
	}
	rest := &categoryREST{plugin: p}
	defer rest.close()
	if err := rest.ensureSession(); err != nil {
		return nil, err
	}
	var channels []*model.Channel
	for page, size := 0, 100; ; page++ {
		result, _, err := rest.client.SearchAllChannelsPaged(ctx, &model.ChannelSearch{TeamIds: []string{teamID}, Public: true, Private: true, Page: &page, PerPage: &size})
		if err != nil {
			return nil, err
		}
		for _, channel := range result.Channels {
			channels = append(channels, &channel.Channel)
		}
		if len(result.Channels) < size {
			return channels, nil
		}
	}
}

func (p *Plugin) adminOptions(ctx context.Context, userID, teamID string) (map[string]any, *model.AppError) {
	systemAdmin := p.API.HasPermissionTo(userID, model.PermissionManageSystem)
	teams, err := p.API.GetTeamsForUser(userID)
	if systemAdmin {
		teams, err = p.API.GetTeams()
	}
	if err != nil {
		return nil, adminError(http.StatusInternalServerError, "Could not load your teams.")
	}
	teamOptions := []map[string]any{}
	allowed := false
	for _, team := range teams {
		if team.DeleteAt == 0 {
			teamOptions = append(teamOptions, map[string]any{"id": team.Id, "name": team.DisplayName, "can_manage": p.API.HasPermissionToTeam(userID, team.Id, model.PermissionManageTeam)})
			allowed = allowed || team.Id == teamID
		}
	}
	channelOptions := []map[string]any{}
	if teamID != "" {
		if !allowed {
			return nil, adminError(http.StatusForbidden, "You do not have access to this team.")
		}
		channels, err := p.adminChannels(ctx, userID, teamID)
		if err != nil {
			return nil, adminError(http.StatusInternalServerError, "Could not load this team's channels.")
		}
		config := p.getConfiguration()
		for _, channel := range channels {
			if eligibleDefaultChannel(channel, teamID) {
				channelOptions = append(channelOptions, map[string]any{
					"id": channel.Id, "name": channel.DisplayName, "private": channel.Type == model.ChannelTypePrivate,
					"category": channel.DefaultCategoryName, "can_add": p.adminCanManageChannel(userID, channel),
					"is_default": slices.Contains(config.defaultChannelIDs(), channel.Id), "built_in": channel.Name == model.DefaultChannelName,
				})
			}
		}
	}
	return map[string]any{"system_admin": systemAdmin, "teams": teamOptions, "channels": channelOptions}, nil
}

func (p *Plugin) buildAdminPreview(ctx context.Context, userID string, input adminInput) (*adminPreview, *model.AppError) {
	preview := &adminPreview{Changes: []adminChange{}, Warnings: []string{}}
	if input.Action == "keycloak_new_user" {
		if !p.API.HasPermissionTo(userID, model.PermissionManageSystem) {
			return nil, adminError(http.StatusForbidden, "Only system admins can create Keycloak accounts.")
		}
		if validateKeycloakEmail(input.Email) != nil || !model.IsValidUsername(input.Username) || strings.TrimSpace(input.FirstName) == "" || strings.TrimSpace(input.LastName) == "" {
			return nil, adminError(http.StatusBadRequest, "Enter a valid email, Mattermost username, first name, and last name.")
		}
		if err := p.checkMattermostUserConflicts(input.Email, input.Username); err != nil {
			return nil, adminError(http.StatusConflict, err.Error())
		}
		config := p.getConfiguration()
		if config.KeycloakClientID == "" || config.KeycloakClientSecret == "" {
			return nil, adminError(http.StatusBadRequest, "Configure the Keycloak client credentials in the System Console first.")
		}
		token, err := p.getKeycloakAccessToken(config.KeycloakClientID, config.KeycloakClientSecret)
		if err != nil {
			return nil, adminError(http.StatusBadGateway, "Could not authenticate with Keycloak.")
		}
		for _, field := range []struct{ name, value string }{{"email", input.Email}, {"q", "mattermostUsername:" + input.Username}} {
			conflict, err := p.keycloakUserConflict(token, field.name, field.value)
			if err != nil {
				return nil, adminError(http.StatusBadGateway, "Could not check Keycloak for an existing account.")
			}
			if conflict {
				return nil, adminError(http.StatusConflict, "A Keycloak account already uses this email or Mattermost username.")
			}
		}
		preview.Summary = "Create one non-international Keycloak account."
		preview.Warnings = []string{"Email is marked verified. The password is temporary and must be changed at first login.", "International students must create their own accounts. Mattermost account creation happens when the user signs in; migration is not implemented."}
		preview.Changes = append(preview.Changes, adminChange{Kind: "create_account", Username: input.Username, Before: "No account", After: input.FirstName + " " + input.LastName + " · " + input.Email})
		return preview, nil
	}
	if !slices.Contains([]string{"categories", "internationals", "mass_add", "default_set", "default_unset"}, input.Action) {
		return nil, adminError(http.StatusBadRequest, "Choose an implemented dashboard action.")
	}
	if input.TeamID == "" {
		return nil, adminError(http.StatusBadRequest, "Choose a team.")
	}
	if input.Action != "mass_add" && !p.API.HasPermissionToTeam(userID, input.TeamID, model.PermissionManageTeam) {
		return nil, adminError(http.StatusForbidden, "Only team admins can run this action.")
	}
	team, err := p.API.GetTeam(input.TeamID)
	if err != nil || team == nil || team.DeleteAt != 0 {
		return nil, adminError(http.StatusBadRequest, "Choose an active team.")
	}
	var config configuration
	data, _ := json.Marshal(p.API.GetPluginConfig())
	if json.Unmarshal(data, &config) != nil {
		return nil, adminError(http.StatusInternalServerError, "Could not load plugin settings.")
	}
	if input.Action == "categories" {
		return p.adminCategoryPreview(ctx, userID, input, team.DisplayName)
	}
	var channels []*model.Channel
	if input.Action == "internationals" {
		channelIDs := slices.Clone(config.InternationalStudents_DefaultChannels)
		slices.Sort(channelIDs)
		for _, id := range slices.Compact(channelIDs) {
			channel, err := p.API.GetChannel(id)
			if err != nil || channel == nil || channel.DeleteAt != 0 || (channel.Type != model.ChannelTypeOpen && channel.Type != model.ChannelTypePrivate) {
				return nil, adminError(http.StatusBadRequest, "An international default channel is unavailable. Review the System Console settings.")
			}
			if !p.API.HasPermissionToTeam(userID, channel.TeamId, model.PermissionManageTeam) && !p.adminCanManageChannel(userID, channel) {
				return nil, adminError(http.StatusForbidden, "You need permission to manage members in every configured international channel, including channels on other teams.")
			}
			channels = append(channels, channel)
		}
		preview.Summary = "Re-add tracked international students from " + team.DisplayName + " to configured international channels."
		preview.Warnings = []string{"Targets can include configured channels on other teams. Existing memberships are unchanged. This action does not re-run onboarding or category synchronization."}
	} else {
		channel, err := p.API.GetChannel(input.ChannelID)
		if err != nil || channel == nil || !eligibleDefaultChannel(channel, input.TeamID) {
			return nil, adminError(http.StatusBadRequest, "Choose an active public or private channel in this team.")
		}
		channels = []*model.Channel{channel}
		if input.Action == "mass_add" {
			if !p.adminCanManageChannel(userID, channel) {
				return nil, adminError(http.StatusForbidden, "You do not have permission to add users to this channel.")
			}
			preview.Summary = "Add current active members of " + team.DisplayName + " to " + channel.DisplayName + "."
			preview.Warnings = []string{"Includes guests and bots. Existing members and the requesting user are skipped. Nobody is removed."}
		} else {
			if !config.DefaultChannels_OnOffBool || channel.Name == model.DefaultChannelName {
				return nil, adminError(http.StatusBadRequest, "Enable Default Channels first. Town Square is built in and cannot be changed here.")
			}
			isDefault := slices.Contains(config.defaultChannelIDs(), channel.Id)
			set := input.Action == "default_set"
			preview.Summary = "Change the default-channel status of " + channel.DisplayName + "."
			if isDefault == set {
				return preview, nil
			}
			before, after := "Not a default channel", "Default channel"
			if !set {
				before, after = after, before
			}
			preview.Changes = append(preview.Changes, adminChange{Kind: "default", ChannelID: channel.Id, Channel: channel.DisplayName, Before: before, After: after})
			if !set {
				preview.Warnings = []string{"Stops automatic additions. Existing members and their sidebar placement are unchanged."}
				return preview, nil
			}
			preview.Warnings = []string{"Queues a background addition of current active team members, including guests and bots. Future team members will join automatically. Sidebar categories are unchanged."}
		}
	}
	for page := 0; ; page++ {
		if ctx.Err() != nil {
			return nil, adminError(http.StatusRequestTimeout, "Preview cancelled. Nothing was changed.")
		}
		users, err := p.API.GetUsersInTeam(input.TeamID, page, 100)
		if err != nil {
			return nil, adminError(http.StatusInternalServerError, "Could not list team members.")
		}
		for _, user := range users {
			if user.DeleteAt != 0 || (input.Action == "mass_add" && user.Id == userID) ||
				(input.Action == "internationals" && !slices.Contains(config.InternationalStudents_Users, user.Id)) {
				continue
			}
			for _, channel := range channels {
				_, err := p.API.GetChannelMember(channel.Id, user.Id)
				if err != nil && err.StatusCode != http.StatusNotFound {
					return nil, adminError(http.StatusInternalServerError, "Could not check channel membership. Nothing was changed.")
				}
				if err != nil {
					preview.Changes = append(preview.Changes, adminChange{Kind: "add_member", UserID: user.Id, Username: user.Username, ChannelID: channel.Id, Channel: channel.DisplayName, Before: "Not a member", After: "Channel member"})
				}
			}
		}
		if len(preview.Changes) > 10000 {
			return nil, adminError(http.StatusBadRequest, "This preview exceeds 10,000 changes. Narrow the scope before retrying.")
		}
		if len(users) < 100 {
			break
		}
	}
	sortAdminChanges(preview)
	return preview, nil
}

func sortAdminChanges(preview *adminPreview) {
	sort.Slice(preview.Changes, func(i, j int) bool {
		a, b := preview.Changes[i], preview.Changes[j]
		return a.ChannelID+"\x00"+a.UserID+"\x00"+a.Kind < b.ChannelID+"\x00"+b.UserID+"\x00"+b.Kind
	})
}

func (p *Plugin) adminCategoryPreview(ctx context.Context, userID string, input adminInput, teamName string) (*adminPreview, *model.AppError) {
	channels, err := p.adminChannels(ctx, userID, input.TeamID)
	if err != nil {
		return nil, adminError(http.StatusInternalServerError, "Could not list channels for category synchronization.")
	}
	preview := &adminPreview{Summary: "Sync default categories for " + teamName + ".", Changes: []adminChange{}, Warnings: []string{
		"Overrides personal sidebar placement, including Favorites. Missing destination categories are created; affected custom categories with no active channels may be deleted.",
		"Only reviewed member/channel pairs are queued. A later default-category change cancels that channel's queued work. Archived channels are skipped.",
	}}
	for _, channel := range channels {
		if !eligibleDefaultChannel(channel, input.TeamID) || channel.DefaultCategoryName == "" || (input.ChannelID != "" && input.ChannelID != channel.Id) {
			continue
		}
		for page := 0; ; page++ {
			if ctx.Err() != nil {
				return nil, adminError(http.StatusRequestTimeout, "Preview cancelled. Nothing was changed.")
			}
			members, err := p.API.GetChannelMembers(channel.Id, page, 100)
			if err != nil {
				return nil, adminError(http.StatusInternalServerError, "Could not list channel members.")
			}
			for _, member := range members {
				categories, err := p.API.GetChannelSidebarCategories(member.UserId, input.TeamID)
				if err != nil || categories == nil {
					return nil, adminError(http.StatusInternalServerError, "Could not inspect member sidebar categories.")
				}
				var before []string
				correct := false
				for _, category := range categories.Categories {
					if slices.Contains(category.Channels, channel.Id) {
						before = append(before, category.DisplayName)
						correct = category.Type == model.SidebarCategoryCustom && strings.EqualFold(category.DisplayName, channel.DefaultCategoryName)
					}
				}
				if correct && len(before) == 1 {
					continue
				}
				user, err := p.API.GetUser(member.UserId)
				if err != nil || user == nil {
					return nil, adminError(http.StatusInternalServerError, "Could not load a channel member.")
				}
				slices.Sort(before)
				from := strings.Join(before, ", ")
				if from == "" {
					from = "Uncategorized"
				}
				preview.Changes = append(preview.Changes, adminChange{Kind: "category", UserID: member.UserId, Username: user.Username, ChannelID: channel.Id, Channel: channel.DisplayName, Before: from, After: channel.DefaultCategoryName})
			}
			if len(preview.Changes) > 10000 {
				return nil, adminError(http.StatusBadRequest, "This preview exceeds 10,000 changes. Choose a single channel.")
			}
			if len(members) < 100 {
				break
			}
		}
	}
	sortAdminChanges(preview)
	return preview, nil
}

func (p *Plugin) applyAdminPreview(ctx context.Context, userID string, input adminInput, password string, preview *adminPreview) map[string]any {
	completed, failed, queued := 0, 0, 0
	message := "Reviewed changes applied."
	if len(preview.Changes) == 0 {
		return map[string]any{"completed": 0, "failed": 0, "queued": 0, "message": "Nothing to change."}
	}
	if input.Action == "keycloak_new_user" {
		if err := p.createKeycloakUser(input.Email, password, input.FirstName, input.LastName, input.Username); err != nil {
			return map[string]any{"completed": 0, "failed": 1, "queued": 0, "message": "Account creation failed or its outcome is unknown. Generate a new preview to check for an existing account before retrying."}
		}
		return map[string]any{"completed": 1, "failed": 0, "queued": 0, "message": "Keycloak account created. The temporary password must be changed at first login."}
	}
	if input.Action == "default_set" || input.Action == "default_unset" {
		settings := maps.Clone(p.API.GetPluginConfig())
		data, _ := json.Marshal(settings)
		var config configuration
		if json.Unmarshal(data, &config) != nil {
			return map[string]any{"completed": 0, "failed": 1, "queued": 0, "message": "Could not read plugin settings. Generate a new preview."}
		}
		response, err := p.setDefaultChannel(&model.CommandArgs{TeamId: input.TeamID, ChannelId: input.ChannelID}, input.Action == "default_set", settings, &config)
		if err != nil {
			return map[string]any{"completed": 0, "failed": 1, "queued": 0, "message": "Could not save the default channel. Generate a new preview to check the current state."}
		}
		return map[string]any{"completed": 1, "failed": 0, "queued": len(preview.Changes) - 1, "message": response.Text}
	}
	if input.Action == "categories" {
		jobs := map[string]*categoryJob{}
		for _, change := range preview.Changes {
			if jobs[change.ChannelID] == nil {
				jobs[change.ChannelID] = &categoryJob{TeamID: input.TeamID, ChannelID: change.ChannelID, NewName: change.After, Confirmed: true, Scanned: true, FixedScope: true, CreatedAt: time.Now().UnixMilli(), Pending: map[string][]string{}}
			}
			jobs[change.ChannelID].Pending[change.UserID] = nil
		}
		for _, job := range jobs {
			if err := p.saveCategoryJob(categoryJobPrefix+model.NewId(), job); err != nil {
				failed += len(job.Pending)
			} else {
				queued += len(job.Pending)
			}
		}
		message = "Reviewed category moves queued in the background. Later default-category changes cancel queued work for that channel."
	} else {
		for _, change := range preview.Changes {
			if ctx.Err() != nil {
				failed = len(preview.Changes) - completed
				break
			}
			member, memberErr := p.API.GetTeamMember(input.TeamID, change.UserID)
			if memberErr != nil || member == nil || member.DeleteAt != 0 {
				failed++
				continue
			}
			user, userErr := p.API.GetUser(change.UserID)
			if userErr != nil || user == nil || user.DeleteAt != 0 {
				failed++
				continue
			}
			var err *model.AppError
			var membership *model.ChannelMember
			if input.Action == "internationals" {
				if p.addInternationalStudentToChannels(change.UserID, []string{change.ChannelID}) != nil {
					failed++
					continue
				}
				membership, err = p.API.GetChannelMember(change.ChannelID, change.UserID)
			} else {
				membership, err = p.API.AddUserToChannel(change.ChannelID, change.UserID, userID)
			}
			if err != nil || membership == nil {
				failed++
			} else {
				completed++
			}
		}
	}
	if failed > 0 {
		message += " Some changes failed. Review a fresh preview before retrying."
	}
	return map[string]any{"completed": completed, "failed": failed, "queued": queued, "message": message}
}
