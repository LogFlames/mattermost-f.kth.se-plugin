package main

import (
	"fmt"
	"errors"

	"github.com/mattermost/mattermost/server/public/model"
)

func (p *Plugin) JoinLeaveFree_SetupChannelIds() error {
	p.debug("JoinLeaveFree: Setting up channels");
	configuration := p.getConfiguration();

	p.join_leave_free_channel_ids = make(map[string]bool);

	if configuration.JoinLeaveFree_BotUserId == "" {
		p.API.LogError("JoinLeaveFree: Bot User ID is not set");
		return errors.New("JoinLeaveFree: Bot User ID is not set");
	}

	teams, err := p.API.GetTeamsForUser(configuration.JoinLeaveFree_BotUserId);
	if err != nil {
		p.API.LogError("JoinLeaveFree: Failed to get teams for bot user");
		return errors.New("JoinLeaveFree: Failed to get teams for bot user");
	}

	for _, team := range teams {
		channels, err := p.API.GetChannelsForTeamForUser(team.Id, configuration.JoinLeaveFree_BotUserId, false);
		if err != nil {
			p.API.LogError("JoinLeaveFree: Failed to get channels for team");
			return errors.New("JoinLeaveFree: Failed to get channels for team");
		}

		for _, channel := range channels {
			p.join_leave_free_channel_ids[channel.Id] = true;
		}
	}

	return nil;
}

func (p *Plugin) JoinLeaveFree_Filter(post *model.Post, configuration *configuration) (*model.Post, string) {
	if !p.join_leave_free_channel_ids[post.ChannelId] {
		return post, "";
	}

	if post.Type == "system_join_channel" || post.Type == "system_leave_channel" || post.Type == "system_add_to_channel" || post.Type == "system_remove_from_channel" {
		return nil, "JoinLeaveFree: Join/Leave message in join/leave-free channel";
	}

	return post, "";
}

func (p *Plugin) JoinLeaveFree_UserJoinedChannel(channelMember *model.ChannelMember, configuration *configuration) {
	if channelMember.UserId != configuration.JoinLeaveFree_BotUserId {
		return;
	}

	p.debug(fmt.Sprintf("JoinLeaveFree: Bot joined channel, adding channel '%s'", channelMember.ChannelId));

	p.join_leave_free_channel_ids[channelMember.ChannelId] = true;
}

func (p *Plugin) JoinLeaveFree_UserLeftChannel(channelMember *model.ChannelMember, configuration *configuration) {
	if channelMember.UserId != configuration.JoinLeaveFree_BotUserId {
		return;
	}

	p.debug(fmt.Sprintf("JoinLeaveFree: Bot left channel, removing channel '%s'", channelMember.ChannelId));

	p.join_leave_free_channel_ids[channelMember.ChannelId] = false;
}
