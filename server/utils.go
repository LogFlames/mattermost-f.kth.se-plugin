package main

import (
	"github.com/mattermost/mattermost/server/public/model"
)

func (p *Plugin) debug(msg string) {
	configuration := p.getConfiguration()

	if !configuration.DebugLogging {
		return
	}

	post := &model.Post{
		UserId:    p.pluginBot.UserId,
		ChannelId: configuration.LoggingChannelID,
		Message:   msg,
	}

	if _, err := p.API.CreatePost(post); err != nil {
		p.API.LogError(err.Error())
	}
}
