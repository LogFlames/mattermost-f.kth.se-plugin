package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// Model the API's independent snapshots and explicit source/destination writes.
// Embedding API makes any unexpected call fail instead of silently succeeding.
type categoryTestAPI struct {
	plugin.API
	kv             map[string][]byte
	channel        model.Channel
	channels       map[string]model.Channel
	members        model.ChannelMembers
	categories     map[string][]*model.SidebarCategoryWithChannels
	bot            *model.Bot
	user           *model.User
	siteURL        string
	created        int
	updates        int
	deleted        []string
	sessions       []*model.Session
	revoked        []string
	pages          []int
	failSave       bool
	failUser       string
	failChannel    string
	deleteStatus   int
	afterUpdate    func()
	adminTeam      string
	channelMembers map[string]model.ChannelMembers
	searchChannels func(*model.ChannelSearch) (*model.ChannelsWithCount, *model.AppError)
	reports        []*model.Post
	recipients     []string
	failReport     bool
	failDeleteKey  string
}

func newCategoryTestPlugin(t *testing.T) (*Plugin, *categoryTestAPI) {
	t.Helper()
	api := &categoryTestAPI{
		kv: make(map[string][]byte), categories: make(map[string][]*model.SidebarCategoryWithChannels),
		channel: model.Channel{Id: "channel", TeamId: "team", Type: model.ChannelTypeOpen, DefaultCategoryName: "New", UpdateAt: 20},
		members: model.ChannelMembers{{UserId: "user", ChannelId: "channel"}},
		user:    &model.User{Id: "bot", Username: "f.kth.se-plugin-bot", IsBot: true, Roles: "system_user system_admin"},
		channels: map[string]model.Channel{
			"unrelated-channel":  {Id: "unrelated-channel"},
			"added-concurrently": {Id: "added-concurrently"},
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		if !strings.EqualFold(scheme, "Bearer") || token != "test-secret" {
			t.Errorf("unexpected REST request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/v4/channels/search" && api.searchChannels != nil {
			var search model.ChannelSearch
			if err := json.NewDecoder(r.Body).Decode(&search); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			result, err := api.searchChannels(&search)
			w.Header().Set("Content-Type", "application/json")
			if err != nil {
				w.WriteHeader(err.StatusCode)
				_ = json.NewEncoder(w).Encode(err)
			} else {
				_ = json.NewEncoder(w).Encode(result)
			}
			return
		}
		parts := strings.Split(r.URL.Path, "/")
		if r.Method != http.MethodDelete || len(parts) != 10 || parts[1] != "api" || parts[2] != "v4" || parts[3] != "users" || parts[5] != "teams" || parts[6] != "team" || parts[7] != "channels" || parts[8] != "categories" {
			t.Errorf("incorrect category deletion URL: %s", r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if api.deleteStatus != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(api.deleteStatus)
			_ = json.NewEncoder(w).Encode(&model.AppError{StatusCode: api.deleteStatus, Message: "delete failed"})
			return
		}
		userID, categoryID := parts[4], parts[9]
		for _, category := range api.categories[userID] {
			if category.Id != categoryID {
				continue
			}
			if category.Type != model.SidebarCategoryCustom {
				t.Errorf("attempt to delete protected category: %+v", category)
			}
			for _, id := range category.Channels {
				channel, err := api.GetChannel(id)
				if err != nil || channel.DeleteAt == 0 {
					t.Errorf("attempt to delete category with active or unknown channel %s", id)
				}
			}
		}
		api.deleted = append(api.deleted, categoryID)
		api.categories[userID] = slices.DeleteFunc(api.categories[userID], func(c *model.SidebarCategoryWithChannels) bool { return c.Id == categoryID })
		_, _ = w.Write([]byte(`{"status":"OK"}`))
	}))
	t.Cleanup(server.Close)
	api.siteURL = server.URL
	p := &Plugin{pluginBot: model.Bot{UserId: "bot"}}
	p.SetAPI(api)
	return p, api
}

func (a *categoryTestAPI) LogError(string, ...any) {}
func (a *categoryTestAPI) GetPluginID() string     { return "plugin-id" }
func (a *categoryTestAPI) HasPermissionToTeam(userID, teamID string, permission *model.Permission) bool {
	return userID == "admin" && teamID == a.adminTeam && permission == model.PermissionManageTeam
}
func (a *categoryTestAPI) SendEphemeralPost(userID string, post *model.Post) *model.Post {
	if a.failReport {
		return nil
	}
	a.recipients = append(a.recipients, userID)
	a.reports = append(a.reports, post)
	return post
}
func (a *categoryTestAPI) KVGet(key string) ([]byte, *model.AppError) {
	return bytes.Clone(a.kv[key]), nil
}
func (a *categoryTestAPI) KVSet(key string, value []byte) *model.AppError {
	if a.failSave {
		return testAppError(http.StatusInternalServerError)
	}
	a.kv[key] = bytes.Clone(value)
	return nil
}
func (a *categoryTestAPI) KVSetWithOptions(key string, value []byte, options model.PluginKVSetOptions) (bool, *model.AppError) {
	if options.Atomic && !bytes.Equal(a.kv[key], options.OldValue) {
		return false, nil
	}
	if value == nil {
		delete(a.kv, key)
	} else {
		a.kv[key] = bytes.Clone(value)
	}
	return true, nil
}
func (a *categoryTestAPI) KVDelete(key string) *model.AppError {
	if key == a.failDeleteKey {
		return testAppError(http.StatusServiceUnavailable)
	}
	delete(a.kv, key)
	return nil
}
func (a *categoryTestAPI) KVList(page, perPage int) ([]string, *model.AppError) {
	keys := make([]string, 0, len(a.kv))
	for key := range a.kv {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys[min(page*perPage, len(keys)):min((page+1)*perPage, len(keys))], nil
}
func (a *categoryTestAPI) GetChannel(id string) (*model.Channel, *model.AppError) {
	if id == a.failChannel {
		return nil, testAppError(http.StatusInternalServerError)
	}
	if id == a.channel.Id {
		channel := a.channel
		return &channel, nil
	}
	if channel, ok := a.channels[id]; ok {
		return &channel, nil
	}
	return nil, testAppError(http.StatusNotFound)
}
func (a *categoryTestAPI) GetChannelMembers(channelID string, page, perPage int) (model.ChannelMembers, *model.AppError) {
	a.pages = append(a.pages, page)
	members := a.members
	if a.channelMembers != nil {
		members = a.channelMembers[channelID]
	}
	return members[min(page*perPage, len(members)):min((page+1)*perPage, len(members))], nil
}
func (a *categoryTestAPI) GetChannelMember(channelID, userID string) (*model.ChannelMember, *model.AppError) {
	members := a.members
	if a.channelMembers != nil {
		members = a.channelMembers[channelID]
	}
	for _, member := range members {
		if member.UserId == userID {
			return &member, nil
		}
	}
	return nil, testAppError(http.StatusNotFound)
}
func cloneCategory(category *model.SidebarCategoryWithChannels) *model.SidebarCategoryWithChannels {
	copy := *category
	copy.Channels = slices.Clone(category.Channels)
	return &copy
}
func (a *categoryTestAPI) GetChannelSidebarCategories(userID, _ string) (*model.OrderedSidebarCategories, *model.AppError) {
	result := &model.OrderedSidebarCategories{}
	for _, category := range a.categories[userID] {
		result.Categories = append(result.Categories, cloneCategory(category))
	}
	return result, nil
}
func (a *categoryTestAPI) CreateChannelSidebarCategory(userID, _ string, category *model.SidebarCategoryWithChannels) (*model.SidebarCategoryWithChannels, *model.AppError) {
	a.created++
	category = cloneCategory(category)
	category.Id = fmt.Sprintf("created-%d", a.created)
	a.categories[userID] = append(a.categories[userID], cloneCategory(category))
	return category, nil
}
func (a *categoryTestAPI) UpdateChannelSidebarCategories(userID, _ string, categories []*model.SidebarCategoryWithChannels) ([]*model.SidebarCategoryWithChannels, *model.AppError) {
	if userID == a.failUser {
		return nil, testAppError(http.StatusInternalServerError)
	}
	a.updates++
	for _, updated := range categories {
		for i, category := range a.categories[userID] {
			if category.Id == updated.Id {
				a.categories[userID][i] = cloneCategory(updated)
			}
		}
	}
	if a.afterUpdate != nil {
		a.afterUpdate()
	}
	return categories, nil
}
func (a *categoryTestAPI) GetConfig() *model.Config {
	return &model.Config{ServiceSettings: model.ServiceSettings{SiteURL: &a.siteURL}}
}
func (a *categoryTestAPI) GetUser(string) (*model.User, *model.AppError) {
	copy := *a.user
	return &copy, nil
}
func (a *categoryTestAPI) CreateSession(session *model.Session) (*model.Session, *model.AppError) {
	session.Id = fmt.Sprintf("session-%d", len(a.sessions))
	session.Token = "test-secret"
	a.sessions = append(a.sessions, session)
	return session, nil
}
func (a *categoryTestAPI) RevokeSession(id string) *model.AppError {
	a.revoked = append(a.revoked, id)
	return nil
}
func testAppError(status int) *model.AppError {
	return &model.AppError{StatusCode: status, Message: "test error"}
}
func testCategory(id, name string, kind model.SidebarCategoryType, channels ...string) *model.SidebarCategoryWithChannels {
	return &model.SidebarCategoryWithChannels{
		SidebarCategory: model.SidebarCategory{Id: id, UserId: "user", TeamId: "team", DisplayName: name, Type: kind}, Channels: channels,
	}
}
func testCategoryJob() *categoryJob {
	return &categoryJob{ChannelID: "channel", OldName: "Old", NewName: "New", OldUpdateAt: 10, CreatedAt: time.Now().UnixMilli(), Pending: map[string][]string{"user": nil}}
}
func categoryByID(t *testing.T, a *categoryTestAPI, userID, id string) *model.SidebarCategoryWithChannels {
	t.Helper()
	for _, category := range a.categories[userID] {
		if category.Id == id {
			return category
		}
	}
	t.Fatalf("category %q missing", id)
	return nil
}

func TestCategoryMoveAndCleanup(t *testing.T) {
	for _, keepOld := range []bool{false, true} {
		t.Run(fmt.Sprintf("keep-old-%t", keepOld), func(t *testing.T) {
			p, api := newCategoryTestPlugin(t)
			old := testCategory("old", "Old", model.SidebarCategoryCustom, "channel")
			if keepOld {
				old.Channels = append(old.Channels, "unrelated-channel")
			}
			target := testCategory("target", "nEW", model.SidebarCategoryCustom, "first", "second")
			target.Muted, target.Collapsed, target.SortOrder = true, true, 70
			target.Sorting = model.SidebarCategorySortManual
			api.categories["user"] = []*model.SidebarCategoryWithChannels{old, target, testCategory("unrelated", "Unrelated", model.SidebarCategoryCustom)}
			job := testCategoryJob()
			rest := &categoryREST{plugin: p}
			defer rest.close()
			if err := p.syncMemberCategory(context.Background(), rest, "job", job, "team", "user"); err != nil {
				t.Fatal(err)
			}
			got := categoryByID(t, api, "user", "target")
			if !slices.Equal(got.Channels, []string{"channel", "first", "second"}) || got.SidebarCategory != target.SidebarCategory || api.created != 0 {
				t.Fatalf("target contents/settings changed incorrectly: %+v", got)
			}
			categoryByID(t, api, "user", "unrelated")
			if keepOld {
				if !slices.Equal(categoryByID(t, api, "user", "old").Channels, []string{"unrelated-channel"}) || len(api.deleted) != 0 {
					t.Fatal("nonempty old category was not preserved")
				}
			} else if !slices.Equal(api.deleted, []string{"old"}) {
				t.Fatalf("wrong deletions: %v", api.deleted)
			}
			if err := p.syncMemberCategory(context.Background(), rest, "job", job, "team", "user"); err != nil || api.updates != 1 {
				t.Fatalf("repeated move is not idempotent: updates=%d, err=%v", api.updates, err)
			}
		})
	}
}

func TestCategoryCreateFavoritesAndClear(t *testing.T) {
	for _, sourceType := range []model.SidebarCategoryType{model.SidebarCategoryFavorites, model.SidebarCategoryCustom} {
		for _, name := range []string{"New", ""} {
			t.Run(string(sourceType)+"/"+name, func(t *testing.T) {
				p, api := newCategoryTestPlugin(t)
				api.categories["user"] = []*model.SidebarCategoryWithChannels{
					testCategory("source", "Personal", sourceType, "channel"),
					testCategory("channels", "Channels", model.SidebarCategoryChannels, "other"),
					testCategory("dm", "Direct Messages", model.SidebarCategoryDirectMessages, "direct"),
				}
				job := testCategoryJob()
				job.NewName = name
				rest := &categoryREST{plugin: p}
				defer rest.close()
				if err := p.syncMemberCategory(context.Background(), rest, "job", job, "team", "user"); err != nil {
					t.Fatal(err)
				}
				if name == "" {
					if api.created != 0 || !slices.Equal(categoryByID(t, api, "user", "channels").Channels, []string{"channel", "other"}) {
						t.Fatal("clearing did not move to built-in Channels")
					}
				} else if api.created != 1 || !slices.Equal(categoryByID(t, api, "user", "created-1").Channels, []string{"channel"}) {
					t.Fatal("destination was not created/populated")
				}
				if sourceType == model.SidebarCategoryFavorites && len(categoryByID(t, api, "user", "source").Channels) != 0 {
					t.Fatal("favorite was not moved")
				}
				if !slices.Equal(categoryByID(t, api, "user", "dm").Channels, []string{"direct"}) {
					t.Fatal("DMs changed")
				}
			})
		}
	}
}

func TestCategoryCleanupIgnoresArchivedChannels(t *testing.T) {
	for _, state := range []string{"archived-only", "mixed", "lookup-error", "missing"} {
		t.Run(state, func(t *testing.T) {
			p, api := newCategoryTestPlugin(t)
			archived := model.Channel{Id: "archived", DeleteAt: 123}
			api.channels[archived.Id] = archived
			source := testCategory("old", "Old", model.SidebarCategoryCustom, "channel", "archived")
			switch state {
			case "mixed":
				source.Channels = append(source.Channels, "unrelated-channel")
			case "lookup-error":
				source.Channels = append(source.Channels, "unavailable")
				api.failChannel = "unavailable"
			case "missing":
				source.Channels = append(source.Channels, "missing")
			}
			api.categories["user"] = []*model.SidebarCategoryWithChannels{
				source,
				testCategory("unrelated", "Unrelated", model.SidebarCategoryCustom, "archived"),
				testCategory("favorites", "Favorites", model.SidebarCategoryFavorites, "archived"),
			}
			rest := &categoryREST{plugin: p}
			defer rest.close()
			err := p.syncMemberCategory(context.Background(), rest, "job", testCategoryJob(), "team", "user")
			wantError := state == "lookup-error" || state == "missing"
			if (err != nil) != wantError {
				t.Fatalf("error = %v, want error = %t", err, wantError)
			}
			if state == "archived-only" {
				if !slices.Equal(api.deleted, []string{"old"}) {
					t.Fatalf("archived-only source was not deleted: %v", api.deleted)
				}
			} else if len(api.deleted) != 0 || len(categoryByID(t, api, "user", "old").Channels) != 2 {
				t.Fatal("source with active or unknown channels was not preserved")
			}
			if !reflect.DeepEqual(api.channels["archived"], archived) {
				t.Fatal("cleanup modified the archived channel")
			}
			categoryByID(t, api, "user", "unrelated")
			categoryByID(t, api, "user", "favorites")
		})
	}
}

func TestCategoryDeletionRechecksAndRetries(t *testing.T) {
	t.Run("concurrent addition preserves source", func(t *testing.T) {
		p, api := newCategoryTestPlugin(t)
		api.categories["user"] = []*model.SidebarCategoryWithChannels{testCategory("old", "Old", model.SidebarCategoryCustom, "channel")}
		api.afterUpdate = func() { categoryByID(t, api, "user", "old").Channels = []string{"added-concurrently"} }
		if err := p.syncMemberCategory(context.Background(), &categoryREST{plugin: p}, "job", testCategoryJob(), "team", "user"); err != nil {
			t.Fatal(err)
		}
		if len(api.deleted) != 0 || len(api.sessions) != 0 {
			t.Fatal("deleted a source that became nonempty")
		}
	})
	t.Run("restart after deletion failure", func(t *testing.T) {
		p, api := newCategoryTestPlugin(t)
		// A personal category cannot be recovered by matching OldName alone.
		api.categories["user"] = []*model.SidebarCategoryWithChannels{testCategory("personal", "Personal", model.SidebarCategoryCustom, "channel")}
		api.deleteStatus = http.StatusServiceUnavailable
		rest := &categoryREST{plugin: p}
		if err := p.syncMemberCategory(context.Background(), rest, "job", testCategoryJob(), "team", "user"); err == nil {
			t.Fatal("expected deletion failure")
		}
		rest.close()
		var resumed categoryJob
		if err := json.Unmarshal(api.kv["job"], &resumed); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(resumed.Pending["user"], []string{"personal"}) {
			t.Fatalf("cleanup IDs not saved before move: %+v", resumed)
		}
		api.deleteStatus = 0
		restarted := &Plugin{pluginBot: p.pluginBot}
		restarted.SetAPI(api)
		rest = &categoryREST{plugin: restarted}
		defer rest.close()
		if err := restarted.syncMemberCategory(context.Background(), rest, "job", &resumed, "team", "user"); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(api.deleted, []string{"personal"}) || api.updates != 1 {
			t.Fatal("cleanup did not resume independently of the move")
		}
	})
	t.Run("native actor move already happened", func(t *testing.T) {
		p, api := newCategoryTestPlugin(t)
		api.categories["user"] = []*model.SidebarCategoryWithChannels{
			testCategory("old", "OLD", model.SidebarCategoryCustom), testCategory("new", "New", model.SidebarCategoryCustom, "channel"),
		}
		rest := &categoryREST{plugin: p}
		defer rest.close()
		if err := p.syncMemberCategory(context.Background(), rest, "job", testCategoryJob(), "team", "user"); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(api.deleted, []string{"old"}) || api.updates != 0 {
			t.Fatal("old default should be cleaned even when native code already moved the actor")
		}
	})
}

func TestCategoryHookAndCommitConfirmation(t *testing.T) {
	p, api := newCategoryTestPlugin(t)
	old := api.channel
	old.DefaultCategoryName, old.UpdateAt = "Old", 10
	if replacement, reason := p.ChannelWillBeUpdated(nil, &api.channel, &old); replacement != nil || reason != "" {
		t.Fatalf("unexpected hook response: %v %s", replacement, reason)
	}
	if len(api.kv) != 1 || api.updates != 0 || len(api.sessions) != 0 {
		t.Fatal("hook must only enqueue work")
	}
	var job categoryJob
	for _, data := range api.kv {
		if err := json.Unmarshal(data, &job); err != nil {
			t.Fatal(err)
		}
	}
	api.channel = old
	rest := &categoryREST{plugin: p}
	if done, err := p.runCategoryJob(context.Background(), rest, "job", &job); done || err != nil || api.created != 0 {
		t.Fatalf("uncommitted update must wait: done=%t err=%v", done, err)
	}
	job.CreatedAt = time.Now().Add(-2 * time.Minute).UnixMilli()
	if done, err := p.runCategoryJob(context.Background(), rest, "job", &job); !done || err != nil || api.updates != 0 {
		t.Fatal("rejected update was not discarded without side effects")
	}
	before := len(api.kv)
	p.ChannelWillBeUpdated(nil, &old, &old)
	if len(api.kv) != before {
		t.Fatal("unchanged default queued a job")
	}
	api.failSave = true
	changed := old
	changed.DefaultCategoryName = "New"
	if _, reason := p.ChannelWillBeUpdated(nil, &changed, &old); reason == "" {
		t.Fatal("queue failure must be surfaced to the caller")
	}
}

func TestCategoryPaginationAndFailedMember(t *testing.T) {
	p, api := newCategoryTestPlugin(t)
	api.members = nil
	for i := 0; i < categoryPageSize+3; i++ {
		id := fmt.Sprintf("user-%03d", i)
		api.members = append(api.members, model.ChannelMember{UserId: id, ChannelId: "channel"})
		api.categories[id] = []*model.SidebarCategoryWithChannels{testCategory("channels", "Channels", model.SidebarCategoryChannels, "channel")}
	}
	api.failUser = "user-002"
	job := testCategoryJob()
	job.Pending = make(map[string][]string)
	rest := &categoryREST{plugin: p}
	if done, err := p.runCategoryJob(context.Background(), rest, "job", job); done || err == nil {
		t.Fatalf("failed member must leave retryable work: done=%t err=%v", done, err)
	}
	if !slices.Equal(api.pages, []int{0, 1}) || api.updates != categoryPageSize+2 || len(job.Pending) != 1 {
		t.Fatalf("failure stopped later members/pages: pages=%v updates=%d pending=%v", api.pages, api.updates, job.Pending)
	}
	if job.Created != 103 || job.Moved != 102 || job.Deleted != 0 {
		t.Fatalf("failed move counted as successful: %+v", job)
	}
	// A successful member rearranges their sidebar before the failed user retries.
	api.categories["user-102"][0].Channels = []string{"channel"}
	api.categories["user-102"][1].Channels = nil
	api.failUser = ""
	var resumed categoryJob
	if err := json.Unmarshal(api.kv["job"], &resumed); err != nil {
		t.Fatal(err)
	}
	if done, err := p.runCategoryJob(context.Background(), rest, "job", &resumed); !done || err != nil {
		t.Fatalf("retry failed: done=%t err=%v", done, err)
	}
	if api.updates != categoryPageSize+3 || len(resumed.Pending) != 0 || !slices.Equal(api.pages, []int{0, 1}) ||
		!slices.Equal(api.categories["user-102"][0].Channels, []string{"channel"}) {
		t.Fatal("retry duplicated moves or lost failed member")
	}
	if resumed.Created != 103 || resumed.Moved != 103 || resumed.Deleted != 0 {
		t.Fatalf("retry lost or double-counted operations: %+v", resumed)
	}
}

func TestCategorySupersededJobUsesCommittedDefault(t *testing.T) {
	p, api := newCategoryTestPlugin(t)
	job := testCategoryJob()
	job.Confirmed, job.Page = true, 3
	api.channel.DefaultCategoryName = "Latest"
	api.categories["user"] = []*model.SidebarCategoryWithChannels{testCategory("old", "Old", model.SidebarCategoryCustom, "channel")}
	rest := &categoryREST{plugin: p}
	defer rest.close()
	if done, err := p.runCategoryJob(context.Background(), rest, "job", job); !done || err != nil {
		t.Fatalf("latest change did not finish: done=%t err=%v", done, err)
	}
	if job.NewName != "Latest" || job.Page != 0 || categoryByID(t, api, "user", "created-1").DisplayName != "Latest" {
		t.Fatal("stale job overwrote the latest default")
	}
}

func TestCategorySessions(t *testing.T) {
	p, api := newCategoryTestPlugin(t)
	rest := &categoryREST{plugin: p}
	if deleted, err := rest.deleteCategory(context.Background(), "user", "team", "empty"); err != nil || !deleted {
		t.Fatal(err)
	}
	session := api.sessions[0]
	if session.UserId != "bot" || session.Roles != "system_user system_admin" || session.Props[model.SessionPropIsBot] != model.SessionPropIsBotValue || session.ExpiresAt <= time.Now().UnixMilli() || session.ExpiresAt > time.Now().Add(11*time.Minute).UnixMilli() {
		t.Fatalf("invalid session: %+v", session)
	}
	if deleted, err := rest.deleteCategory(context.Background(), "user", "team", "another"); err != nil || !deleted || len(api.sessions) != 1 {
		t.Fatal("session not reused")
	}
	session.ExpiresAt = time.Now().UnixMilli()
	api.deleteStatus = http.StatusNotFound
	if deleted, err := rest.deleteCategory(context.Background(), "user", "team", "gone"); err != nil || deleted || len(api.sessions) != 2 || !reflect.DeepEqual(api.revoked, []string{"session-0"}) {
		t.Fatalf("session renewal/404 handling failed: %v", err)
	}
	rest.close()
	if !slices.Equal(api.revoked, []string{"session-0", "session-1"}) || rest.client != nil || rest.session != nil {
		t.Fatal("session was not revoked and cleared")
	}
	for _, data := range api.kv {
		if bytes.Contains(data, []byte("test-secret")) {
			t.Fatal("token persisted")
		}
	}
}

func TestCategoryWorkerRetriesAndRevokesSession(t *testing.T) {
	p, api := newCategoryTestPlugin(t)
	api.categories["user"] = []*model.SidebarCategoryWithChannels{testCategory("old", "Old", model.SidebarCategoryCustom, "channel")}
	key := categoryJobPrefix + "test"
	job := testCategoryJob()
	// Millisecond timestamps can be equal even though the category changed.
	job.OldUpdateAt = api.channel.UpdateAt
	if err := p.saveCategoryJob(key, job); err != nil {
		t.Fatal(err)
	}
	api.deleteStatus = http.StatusServiceUnavailable
	p.runCategoryJobs(context.Background())
	if err := json.Unmarshal(api.kv[key], job); err != nil {
		t.Fatal(err)
	}
	if job.Attempts != 1 || job.NextAttempt <= time.Now().UnixMilli() || !job.Scanned || len(api.revoked) != 1 || len(job.Pending) != 1 {
		t.Fatalf("job/session did not survive the failure correctly: %+v", job)
	}
	job.NextAttempt = 0
	if err := p.saveCategoryJob(key, job); err != nil {
		t.Fatal(err)
	}
	api.deleteStatus = 0
	p.runCategoryJobs(context.Background())
	if len(api.kv) != 0 || api.updates != 1 || len(api.revoked) != 2 || !slices.Equal(api.deleted, []string{"old"}) {
		t.Fatal("retry did not finish cleanup and revoke its session")
	}
}

func TestCategoryWorkerKeyPaginationAndShutdown(t *testing.T) {
	p, api := newCategoryTestPlugin(t)
	api.kv["unrelated-setting"] = []byte("keep")
	for i := 0; i < categoryPageSize+3; i++ {
		job := testCategoryJob()
		job.NewName = "Rejected"
		job.CreatedAt = time.Now().Add(-2 * time.Minute).UnixMilli()
		if err := p.saveCategoryJob(fmt.Sprintf("%s%03d", categoryJobPrefix, i), job); err != nil {
			t.Fatal(err)
		}
	}
	p.runCategoryJobs(context.Background())
	if len(api.kv) != 1 || string(api.kv["unrelated-setting"]) != "keep" || api.updates != 0 {
		t.Fatal("queue pagination skipped jobs or changed unrelated state")
	}
	if err := p.startCategoryWorker(); err != nil {
		t.Fatal(err)
	}
	if err := p.OnDeactivate(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.categoryDone:
	default:
		t.Fatal("worker did not shut down")
	}
}
