package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// Plugin implements the interface expected by the Mattermost server to communicate between the server and plugin processes.
type Plugin struct {
	plugin.MattermostPlugin

	configurationLock sync.RWMutex
	configuration     *configuration

	// Global
	pluginBot model.Bot

	categoryCancel context.CancelFunc
	categoryDone   chan struct{}

	// Join-Leave-Free
	join_leave_free_channel_ids map[string]bool

	// Moderator
	moderator_channels map[string]bool

	// Reactions
	reactionsBotUserId string

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
		post, rejectReason := p.InsertZeroWidthSpaces(post, configuration)
		if rejectReason != "" {
			return post, rejectReason
		}
	}

	if configuration.JoinLeaveFree_OnOffBool {
		post, rejectReason := p.JoinLeaveFree_Filter(post, configuration)
		if rejectReason != "" {
			return post, rejectReason
		}
	}

	if configuration.Reactions_OnOffBool {
		post, rejectReason := p.Reactions_Filter(post, configuration)
		if rejectReason != "" {
			return post, rejectReason
		}
	}

	return post, ""
}

func (p *Plugin) MessageWillBePosted(_ *plugin.Context, post *model.Post) (*model.Post, string) {
	return p.FilterPost(post)
}

func (p *Plugin) MessageHasBeenPosted(_ *plugin.Context, post *model.Post) {
	if p.configuration.ModeratorBot_OnOffBool {
		p.Moderator_MessageHasBeenPosted(post, p.configuration)
	}
}

func (p *Plugin) MessageWillBeUpdated(_ *plugin.Context, newPost *model.Post, _ *model.Post) (*model.Post, string) {
	return p.FilterPost(newPost)
}

func (p *Plugin) UserHasJoinedChannel(_ *plugin.Context, channelMember *model.ChannelMember, actor *model.User) {
	configuration := p.getConfiguration()

	if configuration.JoinLeaveFree_OnOffBool {
		p.JoinLeaveFree_UserJoinedChannel(channelMember, configuration)
	}
}

func (p *Plugin) UserHasLeftChannel(_ *plugin.Context, channelMember *model.ChannelMember, actor *model.User) {
	configuration := p.getConfiguration()

	if configuration.JoinLeaveFree_OnOffBool {
		p.JoinLeaveFree_UserLeftChannel(channelMember, configuration)
	}
}

func (p *Plugin) ExecuteCommand(c *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	trigger := strings.TrimPrefix(strings.Fields(args.Command)[0], "/")
	switch trigger {
	case "mass_add":
		if res, err := p.MassAdd_ExecuteCommand(c, args); err != nil {
			return nil, err
		} else {
			return res, nil
		}
	case "force_sync_categories":
		return p.forceSyncCategories(args)
	case "default_channel":
		return p.defaultChannelCommand(args)
	default:
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("Unknown command: %s", args.Command),
		}, nil
	}
}

func (p *Plugin) OnActivate() error {
	configuration := p.getConfiguration()

	p.emoji_regexes = p.compileEmojiRegexes()

	if err := p.EnsureBot(); err != nil {
		return err
	}

	if err := p.EnsureReactionsBot(); err != nil {
		return err
	}

	if configuration.JoinLeaveFree_OnOffBool {
		if err := p.JoinLeaveFree_SetupChannelIds(); err != nil {
			return err
		}
	}

	command := p.MassAdd_GetCommand()
	if err := p.API.RegisterCommand(command); err != nil {
		return err
	}
	if err := p.API.RegisterCommand(&model.Command{
		Trigger:          "force_sync_categories",
		AutoComplete:     true,
		AutoCompleteDesc: "Sync default categories for all public and private channels in this team (team admins only)",
	}); err != nil {
		return err
	}
	if err := p.API.RegisterCommand(&model.Command{
		Trigger:          "default_channel",
		AutoComplete:     true,
		AutoCompleteHint: "set|unset|list",
		AutoCompleteDesc: "Manage this team's default channels (team admins only)",
	}); err != nil {
		return err
	}

	return p.startCategoryWorker()
}
