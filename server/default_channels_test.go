package main

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

type defaultChannelTestAPI struct {
	*categoryTestAPI
	p          *Plugin
	settings   map[string]any
	users      []*model.User
	left       map[string]bool
	added      []string
	failAdd    string
	failConfig bool
	saves      int
}

func newDefaultChannelTestPlugin(t *testing.T) (*Plugin, *defaultChannelTestAPI) {
	t.Helper()
	base := &categoryTestAPI{
		kv: map[string][]byte{}, adminTeam: "team",
		channel:  model.Channel{Id: "channel", Name: "general", TeamId: "team", Type: model.ChannelTypeOpen, DefaultCategoryName: "News"},
		channels: map[string]model.Channel{},
	}
	p := &Plugin{pluginBot: model.Bot{UserId: "bot"}}
	api := &defaultChannelTestAPI{categoryTestAPI: base, p: p, left: map[string]bool{}, settings: map[string]any{
		"defaultchannels_onoffbool": true, "defaultchannels_custom": []defaultChannelEntry{}, "UnrelatedSetting": "keep",
	}}
	p.SetAPI(api)
	if err := p.OnConfigurationChange(); err != nil {
		t.Fatal(err)
	}
	return p, api
}

func (a *defaultChannelTestAPI) GetPluginConfig() map[string]any { return a.settings }
func (a *defaultChannelTestAPI) GetChannelByName(teamID, name string, includeDeleted bool) (*model.Channel, *model.AppError) {
	for _, channel := range a.channels {
		if channel.TeamId == teamID && channel.Name == name && (includeDeleted || channel.DeleteAt == 0) {
			return a.GetChannel(channel.Id)
		}
	}
	return nil, testAppError(http.StatusNotFound)
}
func (a *defaultChannelTestAPI) LoadPluginConfiguration(target any) error {
	data, err := json.Marshal(a.settings)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
func (a *defaultChannelTestAPI) SavePluginConfig(settings map[string]any) *model.AppError {
	if a.failConfig {
		return testAppError(http.StatusServiceUnavailable)
	}
	// Exercise the actual RPC argument encoding. A direct fake accepts custom
	// Go types that Mattermost's gob transport cannot send to the server.
	var wire bytes.Buffer
	if err := gob.NewEncoder(&wire).Encode(&plugin.Z_SavePluginConfigArgs{A: settings}); err != nil {
		return model.NewAppError("SavePluginConfig", "test.rpc.encode", nil, err.Error(), http.StatusInternalServerError)
	}
	var decoded plugin.Z_SavePluginConfigArgs
	if err := gob.NewDecoder(&wire).Decode(&decoded); err != nil {
		return model.NewAppError("SavePluginConfig", "test.rpc.decode", nil, err.Error(), http.StatusInternalServerError)
	}
	a.settings = decoded.A
	a.saves++
	// Exercise the reentrant config callback, as the real API does.
	if err := a.p.OnConfigurationChange(); err != nil {
		panic(err)
	}
	return nil
}
func (a *defaultChannelTestAPI) GetUsersInTeam(teamID string, page, perPage int) ([]*model.User, *model.AppError) {
	if teamID != "team" || perPage != 100 {
		panic("unexpected team pagination")
	}
	a.pages = append(a.pages, page)
	return a.users[min(page*perPage, len(a.users)):min((page+1)*perPage, len(a.users))], nil
}
func (a *defaultChannelTestAPI) GetTeamMember(teamID, userID string) (*model.TeamMember, *model.AppError) {
	if a.left[userID] {
		return nil, testAppError(http.StatusNotFound)
	}
	return &model.TeamMember{TeamId: teamID, UserId: userID}, nil
}
func (a *defaultChannelTestAPI) GetUser(userID string) (*model.User, *model.AppError) {
	for _, user := range a.users {
		if user.Id == userID {
			return user, nil
		}
	}
	return nil, testAppError(http.StatusNotFound)
}
func (a *defaultChannelTestAPI) AddUserToChannel(channelID, userID, actorID string) (*model.ChannelMember, *model.AppError) {
	if actorID != "bot" {
		panic("wrong actor")
	}
	if userID == a.failAdd {
		return nil, testAppError(http.StatusServiceUnavailable)
	}
	a.added = append(a.added, channelID+":"+userID)
	return &model.ChannelMember{ChannelId: channelID, UserId: userID}, nil
}

func defaultCommand(t *testing.T, p *Plugin, action string) string {
	t.Helper()
	response, err := p.ExecuteCommand(nil, &model.CommandArgs{UserId: "admin", TeamId: "team", ChannelId: "channel", Command: "/default_channel " + action})
	if err != nil || response.ResponseType != model.CommandResponseTypeEphemeral {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	return response.Text
}

func TestDefaultChannelAuthorization(t *testing.T) {
	for _, action := range []string{"set", "unset", "list"} {
		for _, scope := range []string{"member", "other-team", "no-team"} {
			t.Run(action+"/"+scope, func(t *testing.T) {
				p, api := newDefaultChannelTestPlugin(t)
				args := &model.CommandArgs{UserId: "admin", TeamId: "team", ChannelId: "channel", Command: "/default_channel " + action}
				want := "Only team admins"
				switch scope {
				case "member":
					args.UserId = "member"
				case "other-team":
					args.TeamId = "other"
				case "no-team":
					args.TeamId = ""
					want = "from a team"
				}
				response, err := p.ExecuteCommand(nil, args)
				if err != nil || !strings.Contains(response.Text, want) || api.saves != 0 || len(api.kv) != 0 {
					t.Fatalf("unauthorized side effect: response=%+v err=%v", response, err)
				}
			})
		}
	}
}

func TestDefaultChannelValidationAndFailures(t *testing.T) {
	for _, scenario := range []string{"disabled", "arguments", "unknown", "archived", "direct", "other-team", "save-config", "save-job"} {
		t.Run(scenario, func(t *testing.T) {
			p, api := newDefaultChannelTestPlugin(t)
			action, want := "set", "active public or private"
			wantError := false
			switch scenario {
			case "disabled":
				api.settings["defaultchannels_onoffbool"] = false
				want = "disabled"
			case "arguments":
				action, want = "set extra", "Usage:"
			case "unknown":
				action, want = "wrong", "Usage:"
			case "archived":
				api.channel.DeleteAt = 1
			case "direct":
				api.channel.Type = model.ChannelTypeDirect
			case "other-team":
				api.channel.TeamId = "other"
			case "save-config":
				api.failConfig, wantError = true, true
			case "save-job":
				api.failSave, wantError = true, true
			}
			response, err := p.ExecuteCommand(nil, &model.CommandArgs{UserId: "admin", TeamId: "team", ChannelId: "channel", Command: "/default_channel " + action})
			if (err != nil) != wantError || (!wantError && !strings.Contains(response.Text, want)) {
				t.Fatalf("response=%+v err=%v", response, err)
			}
			if api.saves != 0 || len(api.kv) != 0 || len(api.added) != 0 || len(p.getConfiguration().defaultChannelIDs()) != 0 {
				t.Fatal("invalid/failed command mutated state")
			}
		})
	}
}

func TestDefaultChannelSetPaginationRestartAndRetry(t *testing.T) {
	p, api := newDefaultChannelTestPlugin(t)
	api.channel.Type = model.ChannelTypePrivate
	for i := 0; i < 103; i++ {
		api.users = append(api.users, &model.User{Id: fmt.Sprintf("user-%03d", i)})
	}
	api.users[4].DeleteAt = 1
	api.left["user-006"] = true
	api.failAdd = "user-002"
	if text := defaultCommand(t, p, "set"); !strings.Contains(text, "background") || len(api.added) != 0 || api.saves != 1 {
		t.Fatal("set must save and queue, not add synchronously")
	}
	p.runDefaultChannelJobs(context.Background())
	if len(api.added) != 97 || len(api.reports) != 0 {
		t.Fatalf("first page: added=%d reports=%d", len(api.added), len(api.reports))
	}
	// Restart from persisted jobs, without relying on an in-memory config.
	restarted := &Plugin{pluginBot: p.pluginBot}
	restarted.SetAPI(api)
	restarted.runDefaultChannelJobs(context.Background())
	if len(api.added) != 100 || len(api.reports) != 0 || !slices.Equal(api.pages, []int{0, 1}) {
		t.Fatalf("later users blocked by failure: added=%d pages=%v", len(api.added), api.pages)
	}
	for key, data := range api.kv {
		var job defaultChannelJob
		if err := json.Unmarshal(data, &job); err != nil {
			t.Fatal(err)
		}
		if !job.Scanned || !slices.Equal(job.Pending, []string{"user-002"}) || job.NextAttempt == 0 {
			t.Fatalf("incorrect retry state: %+v", job)
		}
		job.NextAttempt = 0
		if err := restarted.saveDefaultChannelJob(key, &job); err != nil {
			t.Fatal(err)
		}
	}
	api.failAdd = ""
	restarted.runDefaultChannelJobs(context.Background())
	if len(api.added) != 101 || len(api.kv) != 0 || len(api.reports) != 1 || !slices.Equal(api.recipients, []string{"admin"}) || api.reports[0].ChannelId != "channel" {
		t.Fatalf("retry/completion incorrect: added=%d jobs=%d reports=%v", len(api.added), len(api.kv), api.reports)
	}
	var want []string
	for i := 0; i < 103; i++ {
		if i != 4 && i != 6 {
			want = append(want, fmt.Sprintf("channel:user-%03d", i))
		}
	}
	slices.Sort(api.added)
	if !slices.Equal(api.added, want) {
		t.Fatalf("added the wrong members: %v", api.added)
	}
	if text := defaultCommand(t, p, "set"); !strings.Contains(text, "already") || api.saves != 1 || len(api.kv) != 0 {
		t.Fatal("repeated set must not re-add users")
	}
	if api.settings["UnrelatedSetting"] != "keep" {
		t.Fatal("lost unrelated configuration")
	}
}

func TestDefaultChannelListAndUnset(t *testing.T) {
	p, api := newDefaultChannelTestPlugin(t)
	api.channels["b"] = model.Channel{Id: "b", Name: "beta", TeamId: "team", Type: model.ChannelTypePrivate}
	api.channels["foreign"] = model.Channel{Id: "foreign", Name: "secret", TeamId: "other", Type: model.ChannelTypeOpen}
	api.channels["archived"] = model.Channel{Id: "archived", Name: "old", TeamId: "team", Type: model.ChannelTypeOpen, DeleteAt: 1}
	api.settings["defaultchannels_custom"] = []defaultChannelEntry{
		{String1: "Legacy category", ChannelIDs: []string{"channel", "foreign", "b"}},
		{ChannelIDs: []string{"channel", "archived", "missing"}},
	}
	want := defaultChannelDescription + "\n\n* ~beta (category: Channels)\n* ~general (category: News)"
	if got := defaultCommand(t, p, "list"); got != want {
		t.Fatalf("list=%q want=%q", got, want)
	}
	if got := defaultCommand(t, p, "unset"); !strings.Contains(got, "have not been removed") {
		t.Fatal(got)
	}
	config := p.getConfiguration()
	if slices.Contains(config.defaultChannelIDs(), "channel") || !slices.Contains(config.defaultChannelIDs(), "foreign") || config.DefaultChannels_Custom[0].String1 != "Legacy category" {
		t.Fatal("unset failed to remove duplicates or damaged other configuration")
	}
	clone := config.Clone()
	clone.DefaultChannels_Custom[0].ChannelIDs[0] = "changed"
	if config.DefaultChannels_Custom[0].ChannelIDs[0] != "foreign" {
		t.Fatal("configuration clone shares channel ID slices")
	}
	if got := defaultCommand(t, p, "unset"); !strings.Contains(got, "not a default") || api.saves != 1 || len(api.added) != 0 {
		t.Fatal("unset was not idempotent")
	}
}

func TestDefaultChannelListSeparatesTownSquare(t *testing.T) {
	for _, tc := range []struct {
		name, category, want string
		configured           bool
		otherDefaults        bool
	}{
		{"no configured defaults", "", "Channels", false, false},
		{"native category", "Information", "Information", false, false},
		{"already configured", "Information", "Information", true, false},
		{"alongside configured defaults", "Information", "Information", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, api := newDefaultChannelTestPlugin(t)
			api.channels["town"] = model.Channel{Id: "town", Name: "town-square", TeamId: "team", Type: model.ChannelTypeOpen, DefaultCategoryName: tc.category}
			api.channels["other-town"] = model.Channel{Id: "other-town", Name: "town-square", TeamId: "other", Type: model.ChannelTypeOpen, DefaultCategoryName: "Wrong team"}
			var ids []string
			if tc.configured {
				ids = append(ids, "town")
			}
			want := defaultChannelDescription + "\n\nNo default channels are configured for this team."
			if tc.otherDefaults {
				ids = append(ids, "channel")
				want = defaultChannelDescription + "\n\n* ~general (category: News)"
			}
			api.settings["defaultchannels_custom"] = []defaultChannelEntry{{ChannelIDs: ids}}
			want += "\n\n**Default channel (town-square):** ~town-square (category: " + tc.want + ")"
			if got := defaultCommand(t, p, "list"); got != want {
				t.Fatalf("list=%q want=%q", got, want)
			}
			if api.saves != 0 || len(api.kv) != 0 || len(api.added) != 0 {
				t.Fatal("listing Town Square changed config or membership")
			}
		})
	}
}

func TestDefaultChannelJoinAndCancellation(t *testing.T) {
	for _, state := range []string{"normal", "unset", "disabled", "archived", "left", "deactivated"} {
		t.Run(state, func(t *testing.T) {
			p, api := newDefaultChannelTestPlugin(t)
			api.users = []*model.User{{Id: "new-user"}}
			api.channels["foreign"] = model.Channel{Id: "foreign", TeamId: "other", Type: model.ChannelTypeOpen}
			api.settings["defaultchannels_custom"] = []defaultChannelEntry{{ChannelIDs: []string{"channel", "foreign", "channel"}}}
			if err := p.OnConfigurationChange(); err != nil {
				t.Fatal(err)
			}
			p.UserHasJoinedTeam(nil, &model.TeamMember{TeamId: "team", UserId: "new-user"}, nil)
			if len(api.kv) != 2 || len(api.added) != 0 {
				t.Fatal("join must deduplicate and persist before adding")
			}
			switch state {
			case "unset":
				defaultCommand(t, p, "unset")
			case "disabled":
				api.settings["defaultchannels_onoffbool"] = false
			case "archived":
				api.channel.DeleteAt = 1
			case "left":
				api.left["new-user"] = true
			case "deactivated":
				api.users[0].DeleteAt = 1
			}
			p.runDefaultChannelJobs(context.Background())
			var want []string
			if state == "normal" {
				want = []string{"channel:new-user"}
			}
			if !slices.Equal(api.added, want) || len(api.pages) != 0 || len(api.reports) != 0 {
				t.Fatalf("incorrect join handling: added=%v pages=%v reports=%v", api.added, api.pages, api.reports)
			}
			if state != "disabled" && len(api.kv) != 0 {
				t.Fatal("completed/cancelled jobs retained")
			}
		})
	}
}

func TestDefaultChannelResetReplacesPendingBulkPass(t *testing.T) {
	p, api := newDefaultChannelTestPlugin(t)
	api.users = []*model.User{{Id: "alice"}}
	defaultCommand(t, p, "set")
	defaultCommand(t, p, "unset")
	defaultCommand(t, p, "set")
	p.runDefaultChannelJobs(context.Background())
	if !slices.Equal(api.added, []string{"channel:alice"}) || len(api.reports) != 1 || len(api.kv) != 0 {
		t.Fatal("reset resurrected an old bulk pass")
	}
}

func TestDefaultChannelJobPaginationAndDisabledHook(t *testing.T) {
	p, api := newDefaultChannelTestPlugin(t)
	api.users = []*model.User{{Id: "alice"}}
	defaultCommand(t, p, "set")
	api.kv["unrelated"] = []byte("keep")
	for i := 0; i < 103; i++ {
		if err := p.saveDefaultChannelJob(fmt.Sprintf("%sjoin-%03d", defaultChannelJobPrefix, i), &defaultChannelJob{
			TeamID: "team", ChannelID: "channel", Scanned: true, Pending: []string{"alice"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	p.runDefaultChannelJobs(context.Background())
	if len(api.kv) != 1 || string(api.kv["unrelated"]) != "keep" || len(api.added) != 104 {
		t.Fatal("job deletion skipped a page or changed unrelated KV")
	}
	api.settings["defaultchannels_onoffbool"] = false
	if err := p.OnConfigurationChange(); err != nil {
		t.Fatal(err)
	}
	p.UserHasJoinedTeam(nil, &model.TeamMember{TeamId: "team", UserId: "alice"}, nil)
	if len(api.kv) != 1 {
		t.Fatal("disabled module queued a join")
	}
}

func TestDefaultChannelConfigKeyCasing(t *testing.T) {
	for _, key := range []string{"defaultchannels_custom", "DefaultChannels_Custom"} {
		t.Run(key, func(t *testing.T) {
			p, api := newDefaultChannelTestPlugin(t)
			delete(api.settings, "defaultchannels_custom")
			api.settings[key] = []defaultChannelEntry{{ChannelIDs: []string{"other"}}}
			defaultCommand(t, p, "set")
			if !slices.Equal(p.getConfiguration().defaultChannelIDs(), []string{"other", "channel"}) {
				t.Fatal("lost saved channel IDs")
			}
			for savedKey := range api.settings {
				if strings.EqualFold(savedKey, "defaultchannels_custom") && savedKey != "defaultchannels_custom" {
					t.Fatal("saved a conflicting key spelling")
				}
			}
		})
	}
}
