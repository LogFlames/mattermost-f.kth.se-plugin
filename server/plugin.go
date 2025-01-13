package main

import (
	"sync"
	"regexp"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// Plugin implements the interface expected by the Mattermost server to communicate between the server and plugin processes.
type Plugin struct {
	plugin.MattermostPlugin

	configurationLock sync.RWMutex
	configuration *configuration

	// Global
	pluginBot model.Bot

	// Join-Leave-Free
	join_leave_free_channel_ids map[string]bool

	// emoji regexes
	emoji_regexes []*regexp.Regexp
}

// See https://developers.mattermost.com/extend/plugins/server/reference/
func (p *Plugin) FilterPost(post *model.Post) (*model.Post, string) {
	configuration := p.getConfiguration()

	if post.ChannelId == configuration.LoggingChannelID {
		return post, ""
	}

	if configuration.ConvertToTextEmojies {
		post, rejectReason := p.InsertZeroWidthSpaces(post, configuration);
		if rejectReason != "" {
			return post, rejectReason
		}
	}

	if configuration.JoinLeaveFree_OnOffBool {
		post, rejectReason := p.JoinLeaveFree_Filter(post, configuration);
		if rejectReason != "" {
			return post, rejectReason
		}
	}

	return post, ""
}

func (p *Plugin) MessageWillBePosted(_ *plugin.Context, post *model.Post) (*model.Post, string) {
	return p.FilterPost(post)
}

func (p *Plugin) MessageWillBeUpdated(_ *plugin.Context, newPost *model.Post, _ *model.Post) (*model.Post, string) {
	return p.FilterPost(newPost)
}

func (p *Plugin) UserHasJoinedChannel(_ *plugin.Context, channelMember *model.ChannelMember, actor *model.User) {
	configuration := p.getConfiguration();

	if configuration.JoinLeaveFree_OnOffBool {
		p.JoinLeaveFree_UserJoinedChannel(channelMember, configuration);
	}
}

func (p *Plugin) UserHasLeftChannel(_ *plugin.Context, channelMember *model.ChannelMember, actor *model.User) {
	configuration := p.getConfiguration();

	if configuration.JoinLeaveFree_OnOffBool {
		p.JoinLeaveFree_UserLeftChannel(channelMember, configuration);
	}
}

func (p *Plugin) OnActivate() error {
	configuration := p.getConfiguration();

	p.emoji_regexes = p.compileEmojiRegexes();

	if err := p.EnsureBot(); err != nil {
		return err;
	}

	if configuration.JoinLeaveFree_OnOffBool {
		if err := p.JoinLeaveFree_SetupChannelIds(); err != nil {
			return err;
		}
	}

	return nil;
}

