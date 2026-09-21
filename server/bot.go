package main

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

func (p *Plugin) EnsureBot() error {
	lock, err := cluster.NewMutex(p.API, "plugin_bot_setup")
	if err != nil {
		return err
	}
	lock.Lock()
	defer lock.Unlock()

	desired := model.Bot{
		Username:    "f.kth.se-plugin-bot",
		DisplayName: "f.kth.se-plugin",
		Description: "f.kth.se-plugin",
		OwnerId:     p.API.GetPluginID(),
	}

	botID, appErr := p.API.KVGet("pluginBotId")
	if appErr != nil {
		return fmt.Errorf("read plugin bot ID: %w", appErr)
	}

	var bot *model.Bot
	if len(botID) != 0 {
		bot, appErr = p.API.GetBot(string(botID), true)
		if appErr != nil && appErr.StatusCode != http.StatusNotFound {
			return fmt.Errorf("get plugin bot: %w", appErr)
		}
	}
	if bot == nil {
		user, lookupErr := p.API.GetUserByUsername(desired.Username)
		switch {
		case lookupErr == nil:
			if !user.IsBot {
				return fmt.Errorf("plugin bot username belongs to a non-bot user")
			}
			bot, appErr = p.API.GetBot(user.Id, true)
		case lookupErr.StatusCode == http.StatusNotFound:
			bot, appErr = p.API.CreateBot(&desired)
		default:
			return fmt.Errorf("look up plugin bot: %w", lookupErr)
		}
		if appErr != nil {
			return fmt.Errorf("ensure plugin bot: %w", appErr)
		}
	}

	// Never elevate a bot owned by a person or another plugin.
	if bot.OwnerId != desired.OwnerId {
		return fmt.Errorf("bot %s is not owned by this plugin", bot.UserId)
	}
	if bot.DeleteAt != 0 {
		if _, appErr = p.API.UpdateBotActive(bot.UserId, true); appErr != nil {
			return fmt.Errorf("activate plugin bot: %w", appErr)
		}
	}
	bot, appErr = p.API.PatchBot(bot.UserId, &model.BotPatch{
		Username: &desired.Username, DisplayName: &desired.DisplayName, Description: &desired.Description,
	})
	if appErr != nil {
		return fmt.Errorf("update plugin bot: %w", appErr)
	}
	user, appErr := p.API.GetUser(bot.UserId)
	if appErr != nil {
		return fmt.Errorf("get plugin bot user: %w", appErr)
	}
	roles := strings.Fields(user.GetRawRoles())
	if !slices.Contains(roles, model.SystemAdminRoleId) {
		roles = append(roles, model.SystemAdminRoleId)
		if _, appErr = p.API.UpdateUserRoles(user.Id, strings.Join(roles, " ")); appErr != nil {
			return fmt.Errorf("grant plugin bot system_admin: %w", appErr)
		}
	}
	if appErr = p.API.KVSet("pluginBotId", []byte(bot.UserId)); appErr != nil {
		return fmt.Errorf("save plugin bot ID: %w", appErr)
	}
	p.pluginBot = *bot
	return nil
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
