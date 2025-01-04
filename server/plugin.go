package main

import (
	"fmt"
	"strings"
	"sync"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// Plugin implements the interface expected by the Mattermost server to communicate between the server and plugin processes.
type Plugin struct {
	plugin.MattermostPlugin

	// configurationLock synchronizes access to the configuration.
	configurationLock sync.RWMutex

	// configuration is the active plugin configuration. Consult getConfiguration and
	// setConfiguration for usage.
	configuration *configuration
}

// See https://developers.mattermost.com/extend/plugins/server/reference/
func (p *Plugin) FilterPost(post *model.Post) (*model.Post, string) {
	configuration := p.getConfiguration()

	/* Emoji replacement */
	if configuration.ConvertToTextEmojies {
		p.debug(fmt.Sprintf("Inserting zero-width spaces into text-emojis in '%s'", post.Id))
		newMessage := strings.ReplaceAll(post.Message, ":)", ":\u200b)")
		newMessage = strings.ReplaceAll(newMessage, ":D", ":\u200bD")
		newMessage = strings.ReplaceAll(newMessage, ":p", ":\u200bp")
		newMessage = strings.ReplaceAll(newMessage, ":(", ":\u200b(")
		newMessage = strings.ReplaceAll(newMessage, ":o", ":\u200bo")
		newMessage = strings.ReplaceAll(newMessage, ":O", ":\u200bO")
		newMessage = strings.ReplaceAll(newMessage, ";)", ";\u200b)")

		post.Message = newMessage
	}

	return post, ""
}

func (p *Plugin) MessageWillBePosted(_ *plugin.Context, post *model.Post) (*model.Post, string) {

	if post.ChannelId == LoggingChannelId {
		return post, ""
	}

	return p.FilterPost(post)
}

func (p *Plugin) MessageWillBeUpdated(_ *plugin.Context, newPost *model.Post, _ *model.Post) (*model.Post, string) {

	if newPost.ChannelId == LoggingChannelId {
		return newPost, ""
	}

	return p.FilterPost(newPost)
}
