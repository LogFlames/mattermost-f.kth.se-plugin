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

func (p *Plugin) EnsureReactionsBot() error {
	reactionsBot := model.Bot{
		Username:    "reactions",
		DisplayName: "Tagga @reactions först i meddelandet.",
		Description: "Tagga alla som har reagerat på första meddelandet.",
	}

	botId, err := p.API.KVGet("reactionsBotId")
	if err != nil {
		p.API.LogError(err.Error())
		return err
	}

	if botId == nil {
		createdBot, err := p.API.CreateBot(&reactionsBot)
		if err != nil {
			// Bot may already exist from a previous install — look it up by username
			user, userErr := p.API.GetUserByUsername(reactionsBot.Username)
			if userErr != nil {
				p.API.LogError("EnsureReactionsBot: CreateBot failed and could not find existing user: " + err.Error())
				return err
			}
			p.reactionsBotUserId = user.Id
		} else {
			p.reactionsBotUserId = createdBot.UserId
		}

		if err := p.API.KVSet("reactionsBotId", []byte(p.reactionsBotUserId)); err != nil {
			p.API.LogError(err.Error())
			return err
		}
	} else {
		p.reactionsBotUserId = string(botId)

		_, err := p.API.PatchBot(p.reactionsBotUserId, &model.BotPatch{
			Username:    &reactionsBot.Username,
			DisplayName: &reactionsBot.DisplayName,
			Description: &reactionsBot.Description,
		})

		if err != nil {
			p.API.LogError(err.Error())
			return err
		}
	}

	return nil
}
