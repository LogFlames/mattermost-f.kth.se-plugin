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
		ChannelId: LoggingChannelId,
		Message:   msg,
	}

	p.API.CreatePost(post)
}
