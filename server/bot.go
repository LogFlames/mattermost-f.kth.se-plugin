package main

import (
	"github.com/mattermost/mattermost/server/public/model"
)

func (p *Plugin) EnsureBot() error {
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
