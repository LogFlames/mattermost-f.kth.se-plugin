package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestForceSyncCleansArchivedDefaultCategories(t *testing.T) {
	p, api := newCategoryTestPlugin(t)
	api.adminTeam = "team"
	for i := 0; i <= categoryPageSize; i++ {
		api.teamMembers = append(api.teamMembers, &model.TeamMember{TeamId: "team", UserId: fmt.Sprintf("user-%03d", i)})
	}
	archived := model.Channel{Id: "archived", TeamId: "team", Type: model.ChannelTypeOpen, DeleteAt: 1, DefaultCategoryName: "Old"}
	api.channels[archived.Id] = archived
	api.categories["user-100"] = []*model.SidebarCategoryWithChannels{
		testCategory("empty", "old", model.SidebarCategoryCustom),
		testCategory("archived-only", "Old", model.SidebarCategoryCustom, "archived"),
		testCategory("nonempty", "Old", model.SidebarCategoryCustom, "archived", "unrelated-channel"),
		testCategory("personal", "Personal", model.SidebarCategoryCustom),
		testCategory("favorites", "Old", model.SidebarCategoryFavorites),
	}
	api.searchChannels = func(search *model.ChannelSearch) (*model.ChannelsWithCount, *model.AppError) {
		if !search.IncludeDeleted {
			t.Error("archived channels must be included")
		}
		return &model.ChannelsWithCount{Channels: model.ChannelListWithTeamData{{Channel: archived}}}, nil
	}
	if _, err := p.ExecuteCommand(nil, &model.CommandArgs{UserId: "admin", TeamId: "team", ChannelId: "invoking-channel", Command: "/force_sync_categories"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10 && len(api.kv) != 0; i++ {
		p.runCategoryJobs(context.Background())
	}
	slices.Sort(api.deleted)
	if len(api.kv) != 0 || !slices.Equal(api.deleted, []string{"archived-only", "empty"}) ||
		len(api.reports) != 1 || !strings.Contains(api.reports[0].Message, "Categories deleted: 2") {
		t.Fatalf("cleanup incomplete: jobs=%d deleted=%v reports=%+v", len(api.kv), api.deleted, api.reports)
	}
	for _, id := range []string{"nonempty", "personal", "favorites"} {
		categoryByID(t, api, "user-100", id)
	}
}

func TestArchiveCategoryCleanup(t *testing.T) {
	for _, state := range []string{"archived", "nonempty", "rejected", "restored"} {
		t.Run(state, func(t *testing.T) {
			p, api := newCategoryTestPlugin(t)
			api.channel.DefaultCategoryName = "Old"
			api.categories["user"] = []*model.SidebarCategoryWithChannels{
				testCategory("old", "old", model.SidebarCategoryCustom, "channel"),
				testCategory("personal", "Personal", model.SidebarCategoryCustom),
			}
			if state == "nonempty" {
				api.categories["user"][0].Channels = append(api.categories["user"][0].Channels, "unrelated-channel")
			}
			if reason := p.ChannelWillBeArchived(nil, &api.channel); reason != "" || len(api.kv) != 1 {
				t.Fatalf("archive hook failed: %q", reason)
			}
			if state != "rejected" {
				api.channel.DeleteAt = 1
			}
			if state == "restored" {
				api.deleteStatus = http.StatusServiceUnavailable
				p.runCategoryJobs(context.Background())
				for key, data := range api.kv {
					var job categoryJob
					if err := json.Unmarshal(data, &job); err != nil {
						t.Fatal(err)
					}
					job.NextAttempt = 0
					if err := p.saveCategoryJob(key, &job); err != nil {
						t.Fatal(err)
					}
				}
				api.channel.DeleteAt = 0
				api.deleteStatus = 0
			}
			p.runCategoryJobs(context.Background())
			if (len(api.deleted) == 1) != (state == "archived") {
				t.Fatalf("wrong deletions: %v", api.deleted)
			}
			if state == "rejected" && len(api.kv) != 1 || state != "rejected" && len(api.kv) != 0 {
				t.Fatalf("unexpected jobs: %d", len(api.kv))
			}
			categoryByID(t, api, "user", "personal")
		})
	}
	p, api := newCategoryTestPlugin(t)
	api.failSave = true
	if reason := p.ChannelWillBeArchived(nil, &api.channel); reason == "" {
		t.Fatal("queue failure must reject archive")
	}
}

func TestLeftChannelCategoryCleanup(t *testing.T) {
	for _, state := range []string{"left", "stale-assignment", "nonempty", "rejoined", "left-team", "lookup-error", "unknown-content"} {
		t.Run(state, func(t *testing.T) {
			p, api := newCategoryTestPlugin(t)
			api.channel.DefaultCategoryName = "Old"
			api.members = nil
			api.categories["user"] = []*model.SidebarCategoryWithChannels{
				testCategory("old", "OLD", model.SidebarCategoryCustom),
				testCategory("personal", "Personal", model.SidebarCategoryCustom),
			}
			switch state {
			case "stale-assignment":
				api.categories["user"][0].Channels = []string{"channel"}
			case "nonempty":
				api.categories["user"][0].Channels = []string{"unrelated-channel"}
			case "rejoined":
				api.members = model.ChannelMembers{{UserId: "user", ChannelId: "channel"}}
			case "left-team":
				api.leftTeam = true
			case "lookup-error":
				api.failChannel = "channel"
			case "unknown-content":
				api.categories["user"][0].Channels = []string{"missing"}
			}
			p.UserHasLeftChannel(nil, &model.ChannelMember{ChannelId: "channel", UserId: "user"}, nil)
			if len(api.kv) != 1 {
				t.Fatal("leave did not queue cleanup")
			}
			if state == "lookup-error" {
				api.failChannel = ""
			}
			p.runCategoryJobs(context.Background())
			wantDelete := state == "left" || state == "stale-assignment" || state == "lookup-error"
			if (len(api.deleted) == 1) != wantDelete || (len(api.kv) == 1) != (state == "unknown-content") {
				t.Fatalf("wrong cleanup: deleted=%v jobs=%d", api.deleted, len(api.kv))
			}
			categoryByID(t, api, "user", "personal")
		})
	}
}

func TestArchiveCleanupPaginatesAndRetries(t *testing.T) {
	p, api := newCategoryTestPlugin(t)
	api.channel.DefaultCategoryName = "Old"
	api.members = nil
	for i := 0; i <= categoryPageSize; i++ {
		user := fmt.Sprintf("user-%03d", i)
		api.members = append(api.members, model.ChannelMember{ChannelId: "channel", UserId: user})
		api.categories[user] = []*model.SidebarCategoryWithChannels{testCategory(user, "Old", model.SidebarCategoryCustom)}
	}
	api.categories["user-000"][0].Channels = []string{"missing"}
	if reason := p.ChannelWillBeArchived(nil, &api.channel); reason != "" {
		t.Fatal(reason)
	}
	api.channel.DeleteAt = 1
	p.runCategoryJobs(context.Background())
	var key string
	var job categoryJob
	for k, data := range api.kv {
		key = k
		if err := json.Unmarshal(data, &job); err != nil {
			t.Fatal(err)
		}
	}
	if !job.Scanned || len(job.Pending) != 1 || len(api.deleted) != categoryPageSize || !slices.Equal(api.pages, []int{0, 1}) {
		t.Fatalf("failed member stopped archive cleanup: job=%+v deleted=%d pages=%v", job, len(api.deleted), api.pages)
	}
	api.categories["user-000"][0].Channels = nil
	job.NextAttempt = 0
	if err := p.saveCategoryJob(key, &job); err != nil {
		t.Fatal(err)
	}
	p.runCategoryJobs(context.Background())
	if len(api.kv) != 0 || len(api.deleted) != categoryPageSize+1 {
		t.Fatalf("archive cleanup did not resume: jobs=%d deleted=%d", len(api.kv), len(api.deleted))
	}
}

func TestSweepWaitsForMoves(t *testing.T) {
	p, api := newCategoryTestPlugin(t)
	api.adminTeam = "team"
	api.teamMembers = []*model.TeamMember{{TeamId: "team", UserId: "user"}}
	archived := model.Channel{Id: "archived", TeamId: "team", Type: model.ChannelTypeOpen, DeleteAt: 1, DefaultCategoryName: "New"}
	api.channels[archived.Id] = archived
	api.categories["user"] = []*model.SidebarCategoryWithChannels{
		testCategory("target", "New", model.SidebarCategoryCustom),
		testCategory("channels", "Channels", model.SidebarCategoryChannels, "channel"),
		testCategory("old", "Old", model.SidebarCategoryCustom, "archived"),
	}
	// Only the target is tied to an archived default.
	api.searchChannels = func(*model.ChannelSearch) (*model.ChannelsWithCount, *model.AppError) {
		return &model.ChannelsWithCount{Channels: model.ChannelListWithTeamData{{Channel: api.channel}, {Channel: archived}}}, nil
	}
	if _, err := p.ExecuteCommand(nil, &model.CommandArgs{UserId: "admin", TeamId: "team", ChannelId: "invoking-channel", Command: "/force_sync_categories"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10 && len(api.kv) != 0; i++ {
		p.runCategoryJobs(context.Background())
	}
	if len(api.kv) != 0 || len(api.deleted) != 0 || !slices.Contains(categoryByID(t, api, "user", "target").Channels, "channel") {
		t.Fatal("cleanup removed the destination before the active channel was moved")
	}
}

func TestSweepRetryAndRestoredDefault(t *testing.T) {
	for _, restored := range []bool{false, true} {
		t.Run(fmt.Sprintf("restored-%t", restored), func(t *testing.T) {
			p, api := newCategoryTestPlugin(t)
			api.adminTeam = "team"
			api.teamMembers = []*model.TeamMember{{TeamId: "team", UserId: "user"}}
			archived := model.Channel{Id: "archived", TeamId: "team", Type: model.ChannelTypeOpen, DeleteAt: 1, DefaultCategoryName: "Old"}
			api.channels[archived.Id] = archived
			api.categories["user"] = []*model.SidebarCategoryWithChannels{testCategory("old", "Old", model.SidebarCategoryCustom)}
			api.searchChannels = func(*model.ChannelSearch) (*model.ChannelsWithCount, *model.AppError) {
				return &model.ChannelsWithCount{Channels: model.ChannelListWithTeamData{{Channel: archived}}}, nil
			}
			api.deleteStatus = http.StatusServiceUnavailable
			if _, err := p.ExecuteCommand(nil, &model.CommandArgs{UserId: "admin", TeamId: "team", ChannelId: "invoking-channel", Command: "/force_sync_categories"}); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 6; i++ {
				p.runCategoryJobs(context.Background())
			}
			if len(api.reports) != 0 || len(api.kv) != 2 {
				t.Fatalf("failed cleanup should block report: jobs=%d reports=%d", len(api.kv), len(api.reports))
			}
			for key, data := range api.kv {
				var job categoryJob
				if err := json.Unmarshal(data, &job); err != nil {
					t.Fatal(err)
				}
				job.NextAttempt = 0
				if err := p.saveCategoryJob(key, &job); err != nil {
					t.Fatal(err)
				}
			}
			api.deleteStatus = 0
			if restored {
				archived.DeleteAt = 0
				api.channels[archived.Id] = archived
			}
			for i := 0; i < 5 && len(api.kv) != 0; i++ {
				p.runCategoryJobs(context.Background())
			}
			if len(api.kv) != 0 || len(api.reports) != 1 || (len(api.deleted) == 1) == restored {
				t.Fatalf("unexpected retry: jobs=%d reports=%d deleted=%v", len(api.kv), len(api.reports), api.deleted)
			}
		})
	}
}

func TestCleanupRejectedArchiveExpires(t *testing.T) {
	p, api := newCategoryTestPlugin(t)
	job := &categoryJob{Action: categoryCleanupArchive, ChannelID: "channel", TeamID: "team", OldName: "New", CreatedAt: time.Now().Add(-2 * time.Minute).UnixMilli()}
	if done, err := p.runCategoryCleanupJob(context.Background(), &categoryREST{plugin: p}, "job", job); !done || err != nil || len(api.deleted) != 0 {
		t.Fatalf("rejected archive was not discarded: done=%t err=%v", done, err)
	}
}
