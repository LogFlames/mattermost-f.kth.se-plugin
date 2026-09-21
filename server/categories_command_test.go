package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestForceSyncCategoriesCommand(t *testing.T) {
	for _, tc := range []struct {
		name, user, team, command, want string
		failSave, queued                bool
	}{
		{"admin", "admin", "team", "/force_sync_categories", "Queued", false, true},
		{"member", "user", "team", "/force_sync_categories", "Only team admins", false, false},
		{"other team", "admin", "other", "/force_sync_categories", "Only team admins", false, false},
		{"no team", "admin", "", "/force_sync_categories", "from a team", false, false},
		{"arguments", "admin", "team", "/force_sync_categories extra", "Usage:", false, false},
		{"save failure", "admin", "team", "/force_sync_categories", "Could not queue", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, api := newCategoryTestPlugin(t)
			api.adminTeam, api.failSave = "team", tc.failSave
			response, err := p.ExecuteCommand(nil, &model.CommandArgs{UserId: tc.user, TeamId: tc.team, ChannelId: "invoking-channel", Command: tc.command})
			if err != nil || response.ResponseType != model.CommandResponseTypeEphemeral || !strings.Contains(response.Text, tc.want) {
				t.Fatalf("response=%+v err=%v", response, err)
			}
			if (len(api.kv) == 1) != tc.queued || api.updates != 0 || len(api.sessions) != 0 {
				t.Fatal("command must only enqueue authorized requests without doing synchronous work")
			}
			for key, data := range api.kv {
				var job categoryJob
				if err := json.Unmarshal(data, &job); err != nil {
					t.Fatal(err)
				}
				if !strings.HasPrefix(key, categoryJobPrefix) || job.TeamID != "team" || job.ChannelID != "" || job.CreatedAt == 0 ||
					job.RequesterID != "admin" || job.ReplyChannelID != "invoking-channel" {
					t.Fatalf("incorrect team job: %+v", job)
				}
			}
		})
	}
}

func TestTeamCategorySyncPaginationAndMembers(t *testing.T) {
	p, api := newCategoryTestPlugin(t)
	api.adminTeam = "team"
	// Neither the invoking admin nor the bot belongs to these channels.
	api.channelMembers = map[string]model.ChannelMembers{
		"public":  {{UserId: "alice"}, {UserId: "bob"}},
		"private": {{UserId: "bob"}},
		"last":    {{UserId: "alice"}},
	}
	channels := model.ChannelListWithTeamData{
		{Channel: model.Channel{Id: "public", TeamId: "team", Type: model.ChannelTypeOpen, DefaultCategoryName: "News"}},
		{Channel: model.Channel{Id: "private", TeamId: "team", Type: model.ChannelTypePrivate, DefaultCategoryName: "Secret"}},
		{Channel: model.Channel{Id: "archived", TeamId: "team", Type: model.ChannelTypeOpen, DefaultCategoryName: "Ignore", DeleteAt: 1}},
		{Channel: model.Channel{Id: "other-team", TeamId: "other", Type: model.ChannelTypeOpen, DefaultCategoryName: "Ignore"}},
		{Channel: model.Channel{Id: "dm", TeamId: "team", Type: model.ChannelTypeDirect, DefaultCategoryName: "Ignore"}},
	}
	for len(channels) < 100 {
		channels = append(channels, &model.ChannelWithTeamData{Channel: model.Channel{
			Id: fmt.Sprintf("no-default-%d", len(channels)), TeamId: "team", Type: model.ChannelTypeOpen,
		}})
	}
	channels = append(channels, &model.ChannelWithTeamData{Channel: model.Channel{
		Id: "last", TeamId: "team", Type: model.ChannelTypePrivate, DefaultCategoryName: "Later",
	}})
	for _, channel := range channels {
		api.channels[channel.Id] = channel.Channel
	}
	api.categories["alice"] = []*model.SidebarCategoryWithChannels{
		testCategory("personal", "Personal", model.SidebarCategoryCustom, "public", "last"),
		testCategory("untouched", "Untouched", model.SidebarCategoryCustom, "no-default-5"),
	}
	api.categories["bob"] = []*model.SidebarCategoryWithChannels{
		testCategory("favorites", "Favorites", model.SidebarCategoryFavorites, "public", "private"),
	}
	var pages []int
	api.searchChannels = func(search *model.ChannelSearch) (*model.ChannelsWithCount, *model.AppError) {
		if !slices.Equal(search.TeamIds, []string{"team"}) || !search.Public || !search.Private || search.IncludeDeleted ||
			search.Page == nil || search.PerPage == nil || *search.PerPage != 100 {
			t.Errorf("incorrect admin search: %+v", search)
			return nil, testAppError(http.StatusBadRequest)
		}
		page := *search.Page
		pages = append(pages, page)
		return &model.ChannelsWithCount{
			Channels: channels[min(page*100, len(channels)):min((page+1)*100, len(channels))], TotalCount: 101,
		}, nil
	}
	if _, err := p.ExecuteCommand(nil, &model.CommandArgs{UserId: "admin", TeamId: "team", ChannelId: "invoking-channel", Command: "/force_sync_categories"}); err != nil {
		t.Fatal(err)
	}
	p.runCategoryJobs(context.Background())
	if len(api.kv) != 3 || api.updates != 0 {
		t.Fatalf("first pass should queue two children and retain parent: jobs=%d updates=%d", len(api.kv), api.updates)
	}
	// A new plugin object resumes only from persisted state.
	restarted := &Plugin{pluginBot: p.pluginBot}
	restarted.SetAPI(api)
	restarted.runCategoryJobs(context.Background())
	restarted.runCategoryJobs(context.Background())
	if len(api.reports) != 0 {
		t.Fatal("report sent before all child jobs finished")
	}
	restarted.runCategoryJobs(context.Background())
	if len(api.kv) != 0 || !slices.Equal(pages, []int{0, 1}) || api.updates != 4 {
		t.Fatalf("sync incomplete: jobs=%d pages=%v updates=%d", len(api.kv), pages, api.updates)
	}
	for user, expected := range map[string]map[string][]string{
		"alice": {"News": {"public"}, "Later": {"last"}, "Untouched": {"no-default-5"}},
		"bob":   {"News": {"public"}, "Secret": {"private"}, "Favorites": {}},
	} {
		if len(api.categories[user]) != len(expected) {
			t.Fatalf("unexpected categories for %s: %+v", user, api.categories[user])
		}
		for _, category := range api.categories[user] {
			want, ok := expected[category.DisplayName]
			if !ok || !slices.Equal(category.Channels, want) {
				t.Errorf("wrong placement for %s: %+v", user, category)
			}
		}
	}
	if !slices.Equal(api.deleted, []string{"personal"}) || len(api.sessions) != len(api.revoked) {
		t.Fatalf("cleanup/session lifecycle incorrect: deleted=%v sessions=%d revoked=%d", api.deleted, len(api.sessions), len(api.revoked))
	}
	want := "Category synchronization complete.\n- Channels moved: 3 (4 member sidebar moves)\n- Categories created: 4\n- Categories deleted: 1"
	if len(api.reports) != 1 || api.reports[0].Message != want || api.reports[0].ChannelId != "invoking-channel" ||
		api.reports[0].UserId != "bot" || !slices.Equal(api.recipients, []string{"admin"}) {
		t.Fatalf("incorrect completion report: %+v recipients=%v", api.reports, api.recipients)
	}
}

func TestTeamCategorySearchRetryPreservesChildProgress(t *testing.T) {
	p, api := newCategoryTestPlugin(t)
	key := categoryJobPrefix + model.NewId()
	job := &categoryJob{TeamID: "team", Page: 2}
	if err := p.saveCategoryJob(key, job); err != nil {
		t.Fatal(err)
	}
	childKey := key + "_" + api.channel.Id
	child := testCategoryJob()
	child.TeamID, child.Confirmed, child.Scanned = "team", true, true
	child.Pending = map[string][]string{"user": {"personal"}}
	if err := p.saveCategoryJob(childKey, child); err != nil {
		t.Fatal(err)
	}
	wantChild := string(api.kv[childKey])
	fail := true
	api.searchChannels = func(search *model.ChannelSearch) (*model.ChannelsWithCount, *model.AppError) {
		if search.Page == nil || *search.Page != 2 {
			t.Errorf("retry lost pagination: %+v", search)
		}
		if fail {
			return nil, testAppError(http.StatusServiceUnavailable)
		}
		return &model.ChannelsWithCount{Channels: model.ChannelListWithTeamData{{Channel: api.channel}}}, nil
	}
	rest := &categoryREST{plugin: p}
	defer rest.close()
	if done, err := p.runCategoryJob(context.Background(), rest, key, job); done || err == nil || job.Page != 2 {
		t.Fatalf("search failure must remain retryable: done=%t err=%v job=%+v", done, err, job)
	}
	fail = false
	if done, err := p.runCategoryJob(context.Background(), rest, key, job); !done || err != nil {
		t.Fatalf("retry failed: done=%t err=%v", done, err)
	}
	if string(api.kv[childKey]) != wantChild {
		t.Fatal("repeated page overwrote pending child cleanup")
	}
}

func TestTeamCategoryJobRechecksEligibility(t *testing.T) {
	for _, state := range []string{"cleared", "moved-team", "archived", "direct"} {
		t.Run(state, func(t *testing.T) {
			p, api := newCategoryTestPlugin(t)
			job := testCategoryJob()
			job.TeamID, job.Confirmed = "team", true
			switch state {
			case "cleared":
				api.channel.DefaultCategoryName = ""
			case "moved-team":
				api.channel.TeamId = "other"
			case "archived":
				api.channel.DeleteAt = 1
			case "direct":
				api.channel.Type = model.ChannelTypeDirect
			}
			if done, err := p.runCategoryJob(context.Background(), &categoryREST{plugin: p}, "job", job); !done || err != nil {
				t.Fatalf("ineligible channel should be skipped: done=%t err=%v", done, err)
			}
			if api.updates != 0 || api.created != 0 || len(api.sessions) != 0 {
				t.Fatal("skipped channel mutated sidebars")
			}
		})
	}
}

func TestTeamCategoryReportWaitsForRetry(t *testing.T) {
	p, api := newCategoryTestPlugin(t)
	parentKey := categoryJobPrefix + model.NewId()
	parent := &categoryJob{TeamID: "team", RequesterID: "admin", ReplyChannelID: "invoking-channel", Scanned: true}
	childKey := parentKey + "_channel"
	child := testCategoryJob()
	child.ParentKey, child.TeamID = parentKey, "team"
	api.categories["user"] = []*model.SidebarCategoryWithChannels{testCategory("old", "Old", model.SidebarCategoryCustom, "channel")}
	for key, job := range map[string]*categoryJob{parentKey: parent, childKey: child} {
		if err := p.saveCategoryJob(key, job); err != nil {
			t.Fatal(err)
		}
	}
	api.deleteStatus = http.StatusServiceUnavailable
	p.runCategoryJobs(context.Background())
	p.runCategoryJobs(context.Background())
	if len(api.reports) != 0 {
		t.Fatal("reported completion while cleanup still needs retrying")
	}
	if err := json.Unmarshal(api.kv[childKey], child); err != nil {
		t.Fatal(err)
	}
	if child.Moved != 1 || child.Created != 1 || child.Deleted != 0 || child.Done {
		t.Fatalf("incorrect persisted counts after partial failure: %+v", child)
	}
	child.NextAttempt = 0
	if err := p.saveCategoryJob(childKey, child); err != nil {
		t.Fatal(err)
	}
	api.deleteStatus = 0
	restarted := &Plugin{pluginBot: p.pluginBot}
	restarted.SetAPI(api)
	restarted.runCategoryJobs(context.Background())
	restarted.runCategoryJobs(context.Background())
	restarted.runCategoryJobs(context.Background())
	want := "Category synchronization complete.\n- Channels moved: 1 (1 member sidebar moves)\n- Categories created: 1\n- Categories deleted: 1"
	if len(api.reports) != 1 || api.reports[0].Message != want || len(api.kv) != 0 || api.updates != 1 {
		t.Fatalf("retry lost/doubled counts or report: reports=%+v jobs=%d updates=%d", api.reports, len(api.kv), api.updates)
	}
}

func TestTeamCategoryReportZeros(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprintf("empty-team-%t", empty), func(t *testing.T) {
			p, api := newCategoryTestPlugin(t)
			api.adminTeam = "team"
			api.categories["user"] = []*model.SidebarCategoryWithChannels{testCategory("target", "New", model.SidebarCategoryCustom, "channel")}
			api.searchChannels = func(*model.ChannelSearch) (*model.ChannelsWithCount, *model.AppError) {
				result := &model.ChannelsWithCount{}
				if !empty {
					result.Channels = model.ChannelListWithTeamData{{Channel: api.channel}}
				}
				return result, nil
			}
			if _, err := p.ExecuteCommand(nil, &model.CommandArgs{UserId: "admin", TeamId: "team", ChannelId: "invoking-channel", Command: "/force_sync_categories"}); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 4; i++ {
				p.runCategoryJobs(context.Background())
			}
			want := "Category synchronization complete.\n- Channels moved: 0 (0 member sidebar moves)\n- Categories created: 0\n- Categories deleted: 0"
			if len(api.reports) != 1 || api.reports[0].Message != want || len(api.kv) != 0 {
				t.Fatalf("no-op sync should report zero changes: %+v", api.reports)
			}
		})
	}
}

func TestTeamCategoryReportDeliveryAndCleanupRetry(t *testing.T) {
	p, api := newCategoryTestPlugin(t)
	key := categoryJobPrefix + model.NewId()
	job := &categoryJob{TeamID: "team", RequesterID: "admin", ReplyChannelID: "invoking-channel", Scanned: true}
	// More than one KV page, and an unrelated request that must not be counted/deleted.
	for i := 0; i < 103; i++ {
		if err := p.saveCategoryJob(fmt.Sprintf("%s_%03d", key, i), &categoryJob{ParentKey: key, Done: true, Moved: 2, Created: 3, Deleted: 1}); err != nil {
			t.Fatal(err)
		}
	}
	api.kv[categoryJobPrefix+"unrelated"] = []byte(`{"moved":1000}`)
	api.failReport = true
	if done, err := p.finishTeamCategoryJob(key, job); done || err == nil || job.Reported || len(api.kv) != 104 {
		t.Fatal("delivery failure must retain all child totals")
	}
	api.failReport = false
	api.failDeleteKey = key + "_050"
	if done, err := p.finishTeamCategoryJob(key, job); done || err == nil || !job.Reported {
		t.Fatal("cleanup failure must retain the reported flag")
	}
	var resumed categoryJob
	if err := json.Unmarshal(api.kv[key], &resumed); err != nil {
		t.Fatal(err)
	}
	api.failDeleteKey = ""
	if done, err := p.finishTeamCategoryJob(key, &resumed); !done || err != nil {
		t.Fatalf("cleanup retry failed: done=%t err=%v", done, err)
	}
	want := "Category synchronization complete.\n- Channels moved: 103 (206 member sidebar moves)\n- Categories created: 309\n- Categories deleted: 103"
	if len(api.reports) != 1 || api.reports[0].Message != want || len(api.kv) != 2 {
		t.Fatalf("aggregation/cleanup duplicated report or skipped a page: reports=%+v keys=%d", api.reports, len(api.kv))
	}
}
