package main

import (
	"strings"
	"sync"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// Plugin implements the interface expected by the Mattermost server to communicate between the server and plugin processes.
type Plugin struct {
	plugin.MattermostPlugin

	configurationLock sync.RWMutex
	configuration *configuration

	pluginBot model.Bot
}

// See https://developers.mattermost.com/extend/plugins/server/reference/
func (p *Plugin) FilterPost(post *model.Post) (*model.Post, string) {
	configuration := p.getConfiguration()

	if post.ChannelId == configuration.LoggingChannelID {
		return post, ""
	}

	if configuration.ConvertToTextEmojies {
		post, errMsg := p.InsertZeroWidthSpaces(post, configuration);
		if errMsg != "" {
			return post, errMsg
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

func (p *Plugin) OnActivate() error {
	if err := p.EnsureBot(); err != nil {
		return err;
	}

	return nil;
}

