package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

const internationalStudentsUsersKey = "internationalstudents_users"

var internationalStudentsConfigLock sync.Mutex

func (p *Plugin) InternationalStudents_UserCreated(user *model.User) error {
	if user == nil || !user.IsSAMLUser() {
		return nil
	}

	// if the user is not actually international, do nothing
	// note: if the Keycloak check fails, treat account as international
	actuallyInternational, err := p.isActuallyInternational(user.Email)
	firstErr := err
	if err != nil {
		firstErr = fmt.Errorf("check Keycloak international status for %q: %w", user.Email, err)
	} else if !actuallyInternational {
		return nil
	}

	// add international students to the correct teams and channels and mute Swedish channels
	configuration := p.getConfiguration()
	englishTeams, err := p.getTeamsFromTeamIDs(configuration.InternationalStudents_DefaultEnglishTeams)
	if err != nil {
		return err
	}
	swedishTeams, err := p.getTeamsFromTeamIDs(configuration.InternationalStudents_DefaultSwedishTeams)
	if err != nil {
		return err
	}

	teams := make([]*model.Team, 0, len(englishTeams)+len(swedishTeams))
	teams = append(teams, englishTeams...)
	teams = append(teams, swedishTeams...)
	firstErr = firstError(firstErr, p.addUserToTeams(user.Id, teams))

	if err := p.addInternationalStudentToChannels(user.Id, configuration.InternationalStudents_DefaultChannels); err != nil {
		firstErr = firstError(firstErr, err)
	}
	if err := p.addInternationalStudentToConfig(user.Id); err != nil {
		firstErr = firstError(firstErr, err)
	}

	if err := p.muteExistingDefaultChannels(user.Id); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (p *Plugin) isActuallyInternational(email string) (bool, error) {
	if email == "" {
		return false, fmt.Errorf("user has no email address")
	}

	configuration := p.getConfiguration()
	if configuration.KeycloakClientID == "" || configuration.KeycloakClientSecret == "" {
		return false, fmt.Errorf("Keycloak client credentials are not configured")
	}

	token, err := p.getKeycloakAccessToken(configuration.KeycloakClientID, configuration.KeycloakClientSecret)
	if err != nil {
		return false, err
	}

	endpoint := fmt.Sprintf("%s/admin/realms/%s/users", keycloakURL, url.PathEscape(keycloakRealm))
	query := url.Values{"email": {email}, "exact": {"true"}}
	request, err := http.NewRequest(http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return false, fmt.Errorf("create Keycloak request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return false, fmt.Errorf("call Keycloak: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("Keycloak returned HTTP %d", response.StatusCode)
	}

	// note: Keycloak is configured to have username=email, so duplicate users are impossible
	var users []struct {
		Attributes map[string][]string `json:"attributes"`
	}
	if err := json.NewDecoder(response.Body).Decode(&users); err != nil {
		return false, fmt.Errorf("decode Keycloak response: %w", err)
	}

	for _, keycloakUser := range users {
		if slices.Contains(keycloakUser.Attributes["isActuallyInternational"], "Yes") {
			return true, nil
		}
	}
	return false, nil
}

func (p *Plugin) addUserToTeams(userID string, teams []*model.Team) error {
	var firstErr error
	for _, team := range teams {
		if team == nil {
			firstErr = firstError(firstErr, fmt.Errorf("configured team lookup returned no team"))
			continue
		}
		if _, err := p.API.GetTeamMember(team.Id, userID); err != nil {
			if err.StatusCode != http.StatusNotFound {
				firstErr = firstError(firstErr, fmt.Errorf("check membership in team %q: %w", team.Name, err))
				continue
			}
			if _, err := p.API.CreateTeamMember(team.Id, userID); err != nil {
				firstErr = firstError(firstErr, fmt.Errorf("add user to team %q: %w", team.Name, err))
			}
		}
	}
	return firstErr
}

func (p *Plugin) forceSyncInternationals(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	if args.TeamId == "" {
		response.Text = "Run /force_sync_internationals from a team."
		return response, nil
	}
	if !p.API.HasPermissionToTeam(args.UserId, args.TeamId, model.PermissionManageTeam) {
		response.Text = "Only team admins can run /force_sync_internationals."
		return response, nil
	}
	if len(strings.Fields(args.Command)) != 1 {
		response.Text = "Usage: /force_sync_internationals (no arguments)."
		return response, nil
	}

	configuration := p.getConfiguration()
	tracked := make(map[string]struct{}, len(configuration.InternationalStudents_Users))
	for _, userID := range configuration.InternationalStudents_Users {
		tracked[userID] = struct{}{}
	}

	added := 0
	var firstErr error
	for page := 0; ; page++ {
		members, err := p.API.GetTeamMembers(args.TeamId, page, 100)
		if err != nil {
			firstErr = firstError(firstErr, fmt.Errorf("list team members: %w", err))
			break
		}
		for _, member := range members {
			if member == nil || member.DeleteAt != 0 {
				continue
			}
			if _, ok := tracked[member.UserId]; !ok {
				continue
			}
			if err := p.addInternationalStudentToChannels(member.UserId, configuration.InternationalStudents_DefaultChannels); err != nil {
				firstErr = firstError(firstErr, fmt.Errorf("sync user %q: %w", member.UserId, err))
			} else {
				added++
			}
		}
		if len(members) < 100 {
			break
		}
	}

	if firstErr != nil {
		p.API.LogError("International student channel synchronization failed", "team_id", args.TeamId, "error", firstErr.Error())
		response.Text = fmt.Sprintf("International student channel synchronization completed with errors after processing %d users. Check the server logs.", added)
	} else {
		response.Text = fmt.Sprintf("Re-added %d international students in this team to the configured international channels.", added)
	}
	return response, nil
}

func (p *Plugin) addInternationalStudentToChannels(userID string, channelIDs []string) error {
	var firstErr error
	for _, channelID := range channelIDs {
		if _, err := p.API.GetChannelMember(channelID, userID); err != nil {
			if err.StatusCode != http.StatusNotFound {
				firstErr = firstError(firstErr, fmt.Errorf("check membership in channel %q: %w", channelID, err))
				continue
			}
			if _, err := p.API.AddChannelMember(channelID, userID); err != nil {
				firstErr = firstError(firstErr, fmt.Errorf("add user to channel %q: %w", channelID, err))
			}
		}
	}
	return firstErr
}

func firstError(current, candidate error) error {
	if current != nil {
		return current
	}
	return candidate
}

func (p *Plugin) getTeamsFromTeamIDs(teamIDs []string) ([]*model.Team, error) {
	teams := make([]*model.Team, 0, len(teamIDs))
	for _, teamID := range teamIDs {
		team, err := p.API.GetTeam(teamID)
		if err != nil {
			return nil, fmt.Errorf("get configured default team %q: %w", teamID, err)
		}
		if team == nil {
			return nil, fmt.Errorf("get configured default team %q returned no team", teamID)
		}
		teams = append(teams, team)
	}
	return teams, nil
}

func (p *Plugin) InternationalStudents_UserJoinedChannel(channelMember *model.ChannelMember, configuration *configuration) {
	if channelMember == nil || !slices.Contains(configuration.InternationalStudents_Users, channelMember.UserId) {
		return
	}

	channel, err := p.API.GetChannel(channelMember.ChannelId)
	if err != nil {
		p.API.LogError("Could not inspect international student channel membership", "channel_id", channelMember.ChannelId, "user_id", channelMember.UserId, "error", err.Error())
		return
	}
	if !p.isDefaultPublicInternationalStudentChannel(channel, configuration) {
		return
	}
	if err := p.muteChannelForUser(channelMember.ChannelId, channelMember.UserId); err != nil {
		p.API.LogError("Could not mute international student channel", "channel_id", channelMember.ChannelId, "user_id", channelMember.UserId, "error", err.Error())
	}
}

func (p *Plugin) addInternationalStudentToConfig(userID string) error {
	internationalStudentsConfigLock.Lock()
	defer internationalStudentsConfigLock.Unlock()

	lock, err := cluster.NewMutex(p.API, "international_students_config")
	if err != nil {
		return err
	}
	lock.Lock()
	defer lock.Unlock()

	var current configuration
	settings := p.API.GetPluginConfig()
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	if slices.Contains(current.InternationalStudents_Users, userID) {
		return nil
	}

	current.InternationalStudents_Users = append(current.InternationalStudents_Users, userID)
	updated := clonePluginSettings(settings)
	updated[internationalStudentsUsersKey] = stringsToAny(current.InternationalStudents_Users)
	return p.API.SavePluginConfig(updated)
}

func (p *Plugin) muteExistingDefaultChannels(userID string) error {
	configuration := p.getConfiguration()
	teams, err := p.getTeamsFromTeamIDs(configuration.InternationalStudents_DefaultSwedishTeams)
	if err != nil {
		return err
	}
	for _, team := range teams {
		for page := 0; ; page++ {
			channels, err := p.API.GetPublicChannelsForTeam(team.Id, page, 100)
			if err != nil {
				return fmt.Errorf("get public channels for team %q: %w", team.Name, err)
			}
			for _, channel := range channels {
				if !p.isDefaultPublicInternationalStudentChannel(channel, configuration) {
					continue
				}
				if _, err := p.API.GetChannelMember(channel.Id, userID); err != nil {
					if err.StatusCode == http.StatusNotFound {
						continue
					}
					return fmt.Errorf("get membership in channel %q: %w", channel.Name, err)
				}
				if err := p.muteChannelForUser(channel.Id, userID); err != nil {
					return fmt.Errorf("mute channel %q: %w", channel.Name, err)
				}
			}
			if len(channels) < 100 {
				break
			}
		}
	}
	return nil
}

func (p *Plugin) isDefaultPublicInternationalStudentChannel(channel *model.Channel, configuration *configuration) bool {
	if channel == nil || channel.Type != model.ChannelTypeOpen || channel.DeleteAt != 0 {
		return false
	}
	team, err := p.API.GetTeam(channel.TeamId)
	if err != nil || team == nil {
		return false
	}
	if !slices.Contains(configuration.InternationalStudents_DefaultSwedishTeams, team.Id) {
		return false
	}
	if channel.Name == model.DefaultChannelName {
		return !slices.Contains(configuration.InternationalStudents_EnglishChannels, channel.Id)
	}
	return slices.Contains(configuration.DefaultChannels_Custom, channel.Id) &&
		!slices.Contains(configuration.InternationalStudents_EnglishChannels, channel.Id)
}

func (p *Plugin) muteChannelForUser(channelID, userID string) *model.AppError {
	member, err := p.API.GetChannelMember(channelID, userID)
	if err != nil {
		return err
	}
	if member == nil {
		return model.NewAppError("muteChannelForUser", "channel member lookup returned no member", nil, "", http.StatusNotFound)
	}
	if member.IsChannelMuted() {
		return nil
	}
	notifyProps := make(map[string]string, len(member.NotifyProps)+1)
	for key, value := range member.NotifyProps {
		notifyProps[key] = value
	}
	notifyProps[model.MarkUnreadNotifyProp] = model.ChannelMarkUnreadMention
	_, err = p.API.UpdateChannelMemberNotifications(channelID, userID, notifyProps)
	return err
}

func clonePluginSettings(settings map[string]any) map[string]any {
	updated := make(map[string]any, len(settings)+1)
	for key, value := range settings {
		if strings.EqualFold(key, internationalStudentsUsersKey) {
			continue
		}
		updated[key] = value
	}
	return updated
}

func stringsToAny(values []string) []any {
	result := make([]any, len(values))
	for i, value := range values {
		result[i] = value
	}
	return result
}
