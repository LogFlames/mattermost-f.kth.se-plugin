package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

const (
	internationalStudentsUsersKey = "internationalstudents_users"
	internationalStudentLocale    = "en"
)

var internationalStudentsConfigLock sync.Mutex

func (p *Plugin) InternationalStudents_UserCreated(user *model.User) error {
	if user == nil || !user.IsSAMLUser() {
		return nil
	}

	if user.Locale != internationalStudentLocale {
		return fmt.Errorf("international student setup ignored for user %q because their language was %q", user.Username, user.Locale)
	}

	configuration := p.getConfiguration()
	englishTeams, err := p.internationalStudentsTeams(configuration.InternationalStudents_DefaultEnglishTeams)
	if err != nil {
		return err
	}
	swedishTeams, err := p.internationalStudentsTeams(configuration.InternationalStudents_DefaultSwedishTeams)
	if err != nil {
		return err
	}

	teams := append(englishTeams, swedishTeams...)
	var firstErr error
	for _, team := range teams {
		if _, err := p.API.GetTeamMember(team.Id, user.Id); err != nil {
			if err.StatusCode != http.StatusNotFound {
				if firstErr == nil {
					firstErr = fmt.Errorf("check membership in team %q: %w", team.Name, err)
				}
				continue
			}
			if _, err := p.API.CreateTeamMember(team.Id, user.Id); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("add user to team %q: %w", team.Name, err)
			}
		}
	}

	if err := p.addInternationalStudentToChannels(user.Id, configuration.InternationalStudents_DefaultChannels); err != nil {
		firstErr = firstError(firstErr, err)
	}
	if err := p.addInternationalStudent(user.Id); err != nil {
		firstErr = firstError(firstErr, err)
	}

	if err := p.muteExistingDefaultChannels(user.Id); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
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

func (p *Plugin) internationalStudentsTeams(teamIDs []string) ([]*model.Team, error) {
	teams := make([]*model.Team, 0, len(teamIDs))
	for _, teamID := range teamIDs {
		team, err := p.API.GetTeam(teamID)
		if err != nil {
			return nil, fmt.Errorf("get configured default team %q: %w", teamID, err)
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

func (p *Plugin) addInternationalStudent(userID string) error {
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
	teams, err := p.internationalStudentsTeams(configuration.InternationalStudents_DefaultSwedishTeams)
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
