package main

import (
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

func (p *Plugin) InsertZeroWidthSpaces(post *model.Post, configuration *configuration) (*model.Post, string) {
	if configuration.ConvertToTextEmojies {
		p.debug("Inserting zero-width spaces into text-emojis")
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
