package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func (a *categoryTestAPI) GetUserByUsername(string) (*model.User, *model.AppError) {
	if a.bot == nil {
		return nil, testAppError(http.StatusNotFound)
	}
	return a.GetUser(a.bot.UserId)
}

func (a *categoryTestAPI) GetBot(id string, _ bool) (*model.Bot, *model.AppError) {
	if a.bot == nil || a.bot.UserId != id {
		return nil, testAppError(http.StatusNotFound)
	}
	copy := *a.bot
	return &copy, nil
}

func (a *categoryTestAPI) CreateBot(bot *model.Bot) (*model.Bot, *model.AppError) {
	copy := *bot
	copy.UserId = "bot"
	a.bot = &copy
	return a.GetBot("bot", true)
}

func (a *categoryTestAPI) PatchBot(id string, patch *model.BotPatch) (*model.Bot, *model.AppError) {
	a.bot.Username, a.bot.DisplayName, a.bot.Description = *patch.Username, *patch.DisplayName, *patch.Description
	return a.GetBot(id, true)
}

func (a *categoryTestAPI) UpdateBotActive(id string, _ bool) (*model.Bot, *model.AppError) {
	a.bot.DeleteAt = 0
	return a.GetBot(id, true)
}

func (a *categoryTestAPI) UpdateUserRoles(_ string, roles string) (*model.User, *model.AppError) {
	a.user.Roles = roles
	return a.GetUser(a.user.Id)
}

func TestEnsureAdministrativeBot(t *testing.T) {
	for _, state := range []string{"new", "saved-id", "missing-id", "stale-id", "inactive"} {
		t.Run(state, func(t *testing.T) {
			p, api := newCategoryTestPlugin(t)
			api.user.Roles = "system_user custom_role"
			if state != "new" {
				api.bot = &model.Bot{UserId: "bot", Username: api.user.Username, OwnerId: "plugin-id"}
			}
			if state == "saved-id" {
				api.kv["pluginBotId"] = []byte("bot")
			}
			if state == "stale-id" {
				api.kv["pluginBotId"] = []byte("removed-bot")
			}
			if state == "inactive" {
				api.bot.DeleteAt = 123
			}
			if err := p.EnsureBot(); err != nil {
				t.Fatal(err)
			}
			if p.pluginBot.UserId != "bot" || string(api.kv["pluginBotId"]) != "bot" || api.bot.DeleteAt != 0 || api.user.Roles != "system_user custom_role system_admin" {
				t.Fatalf("bot was not ensured/elevated correctly: bot=%+v roles=%s", p.pluginBot, api.user.Roles)
			}
			if err := p.EnsureBot(); err != nil || strings.Count(api.user.Roles, "system_admin") != 1 {
				t.Fatalf("bot setup not idempotent: %v", err)
			}
		})
	}
}

func TestEnsureBotRejectsOtherOwnersAndHumans(t *testing.T) {
	for _, human := range []bool{false, true} {
		p, api := newCategoryTestPlugin(t)
		api.bot = &model.Bot{UserId: "bot", Username: api.user.Username, OwnerId: "someone-else"}
		api.user.IsBot = !human
		api.user.Roles = "system_user"
		if err := p.EnsureBot(); err == nil {
			t.Fatal("should not adopt/elevate another account")
		}
		if api.user.Roles != "system_user" || api.bot.DisplayName != "" {
			t.Fatal("foreign account was modified")
		}
	}
}
