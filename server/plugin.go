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

	/* Emoji replacement */
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

func (p *Plugin) MessageWillBePosted(_ *plugin.Context, post *model.Post) (*model.Post, string) {
	return p.FilterPost(post)
}

func (p *Plugin) MessageWillBeUpdated(_ *plugin.Context, newPost *model.Post, _ *model.Post) (*model.Post, string) {
	return p.FilterPost(newPost)
}

func (p *Plugin) OnActivate() error {
	p.pluginBot = model.Bot{
		Username:    "f.kth.se-plugin-bot",
		DisplayName: "f.kth.se-plugin",
		Description: "f.kth.se-plugin",
	};

	botId, err := p.API.KVGet("pluginBotId");
	if err != nil {
		p.API.LogError(err.Error());
		return err;
	}

	if botId == nil {
		pluginBot, err := p.API.CreateBot(&p.pluginBot);
		if err != nil {
			p.API.LogError(err.Error());
			return err;
		}

		p.pluginBot = *pluginBot;

		if err = p.API.KVSet("pluginBotId", []byte(pluginBot.UserId)); err != nil {
			p.API.LogError(err.Error());
			return err;
		}
	} else {
		p.pluginBot.UserId = string(botId);

		pluginBot, err := p.API.PatchBot(p.pluginBot.UserId, &model.BotPatch{
			Username:    &p.pluginBot.Username,
			DisplayName: &p.pluginBot.DisplayName,
			Description: &p.pluginBot.Description,
		});

		if err != nil {
			p.API.LogError(err.Error());
			return err;
		}

		p.pluginBot = *pluginBot;
	}

	return nil;
}

