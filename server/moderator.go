package main

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

func (p *Plugin) Moderator_MessageHasBeenPosted(post *model.Post, configuration *configuration) {
	if !p.moderator_channels[post.ChannelId] {
		return
	}

	if post.UserId == p.pluginBot.UserId {
		return
	}

	if !strings.HasPrefix(post.Message, "#") || strings.HasPrefix(post.Message, "######") {
		p.API.SendEphemeralPost(post.UserId, &model.Post{
			UserId:    p.pluginBot.UserId,
			ChannelId: post.ChannelId,
			Message: fmt.Sprintf("### Please include titles in your messages\n" +
				"To increase the readability of posts sent in ~evenemang and ~general, it is very helpful to include a title describing the subject of the message.\n" +
				"\n" +
				"In the future, please include a title on the following format with preferably three '#'s:\n" +
				"\n" +
				"```markdown\n" +
				"### Title here\n" +
				"\n" +
				"Text here...\n" +
				"```\n" +
				"\n" +
				"Consider editing in a title in the message you just sent.\n"),
		})

		p.debug("Moderator_MessageHasBeenPosted: Successfully sent DM to user: " + post.UserId)
	}
}
