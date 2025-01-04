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
		ChannelId: LoggingChannelID,
		Message:   msg,
	}

	p.API.LogDebug(msg)

	if _, err := p.API.CreatePost(post); err != nil {
		p.API.LogError(err.Error())
	}
}
