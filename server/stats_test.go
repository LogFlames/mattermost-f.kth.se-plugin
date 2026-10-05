package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

func validStatsRequest() statsRequest {
	return statsRequest{Scope: "all", UsersMode: "choose", UserIDs: []string{strings.Repeat("a", 26)}, Start: "2026-03-28", End: "2026-03-29"}
}

func TestStatsValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*statsRequest)
		ok   bool
	}{
		{"valid", func(q *statsRequest) {}, true},
		{"all time", func(q *statsRequest) { q.AllTime, q.Start, q.End = true, "", "" }, true},
		{"all time with dates", func(q *statsRequest) { q.AllTime = true }, false},
		{"missing dates", func(q *statsRequest) { q.Start, q.End = "", "" }, false},
		{"one day", func(q *statsRequest) { q.End = q.Start }, true},
		{"366 days", func(q *statsRequest) { q.Start, q.End = "2024-01-01", "2024-12-31" }, true},
		{"367 days", func(q *statsRequest) { q.Start, q.End = "2024-01-01", "2025-01-01" }, false},
		{"reversed", func(q *statsRequest) { q.End = "2026-03-27" }, false},
		{"subday", func(q *statsRequest) { q.Start += "T12:00:00Z" }, false},
		{"bad date", func(q *statsRequest) { q.Start = "2026-02-30" }, false},
		{"scope", func(q *statsRequest) { q.Scope = "private" }, false},
		{"missing channel", func(q *statsRequest) { q.Scope = "channel" }, false},
		{"missing team", func(q *statsRequest) { q.Scope = "team" }, false},
		{"team DMs", func(q *statsRequest) { q.Scope, q.TeamID, q.IncludeDMs = "team", strings.Repeat("t", 26), true }, false},
		{"users mode", func(q *statsRequest) { q.UsersMode = "everyone" }, false},
		{"empty users", func(q *statsRequest) { q.UserIDs = nil }, false},
		{"too many users", func(q *statsRequest) { q.UserIDs = make([]string, 201) }, false},
		{"SQL injection", func(q *statsRequest) { q.UserIDs = []string{"' OR 1=1 --"} }, false},
		{"missing member channel", func(q *statsRequest) { q.UsersMode = "channel" }, false},
		{"thread permalink", func(q *statsRequest) {
			q.UsersMode, q.Thread = "thread", "https://chat.example/subpath/team/pl/"+strings.Repeat("r", 26)+"?foo=1"
		}, true},
		{"wrong URL", func(q *statsRequest) {
			q.UsersMode, q.Thread = "thread", "https://example.com/"+strings.Repeat("r", 26)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := validStatsRequest()
			tc.edit(&q)
			if err := q.validate(); (err == nil) != tc.ok {
				t.Fatalf("validate=%v, want valid=%v", err, tc.ok)
			}
		})
	}
	q := validStatsRequest()
	if err := q.validate(); err != nil || q.endTime.Sub(q.startTime) != 48*time.Hour || q.endTime.Format(time.RFC3339) != "2026-03-30T00:00:00Z" {
		t.Fatalf("Dates must be inclusive UTC days, even across DST: %+v %v", q, err)
	}
}

type statsTestAPI struct {
	plugin.API
	readable string
	post     *model.Post
}

func (a *statsTestAPI) HasPermissionToChannel(user, channel string, permission *model.Permission) bool {
	return user == "viewer" && channel == a.readable && permission == model.PermissionReadChannel
}

func (a *statsTestAPI) GetPost(id string) (*model.Post, *model.AppError) {
	return a.post, nil
}

func (a *statsTestAPI) GetChannel(id string) (*model.Channel, *model.AppError) {
	kind := model.ChannelTypePrivate
	if id == a.readable {
		kind = model.ChannelTypeOpen
	}
	return &model.Channel{Id: id, Type: kind}, nil
}

func TestStatsHTTPAccess(t *testing.T) {
	for _, scenario := range []string{"anonymous", "method", "malformed", "extra field", "trailing JSON", "hidden channel", "hidden members", "hidden thread", "missing thread", "reply ID", "deleted root", "readable channel", "readable members", "readable thread", "all", "all DMs", "team"} {
		t.Run(scenario, func(t *testing.T) {
			q := validStatsRequest()
			readable, hidden := strings.Repeat("r", 26), strings.Repeat("h", 26)
			api := &statsTestAPI{readable: readable, post: &model.Post{Id: strings.Repeat("p", 26), ChannelId: readable}}
			p := &Plugin{}
			p.SetAPI(api)
			p.statsLock.Lock()
			defer p.statsLock.Unlock()
			viewer, method, want := "viewer", http.MethodPost, http.StatusTooManyRequests
			switch scenario {
			case "anonymous":
				viewer, want = "", http.StatusUnauthorized
			case "method":
				method, want = http.MethodGet, http.StatusMethodNotAllowed
			case "hidden channel":
				q.Scope, q.ChannelID, want = "channel", hidden, http.StatusForbidden
			case "hidden members":
				q.UsersMode, q.MemberChannel, want = "channel", hidden, http.StatusForbidden
			case "hidden thread", "missing thread", "reply ID", "deleted root", "readable thread":
				q.UsersMode, q.Thread = "thread", api.post.Id
				want = http.StatusForbidden
				switch scenario {
				case "hidden thread":
					api.post.ChannelId = hidden
				case "missing thread":
					api.post = nil
				case "reply ID":
					api.post.RootId = hidden
				case "deleted root":
					api.post.DeleteAt = 1
				case "readable thread":
					want = http.StatusTooManyRequests
				}
			case "readable channel":
				q.Scope, q.ChannelID = "channel", readable
			case "readable members":
				q.UsersMode, q.MemberChannel = "channel", readable
			case "all DMs":
				q.IncludeDMs = true
			case "team":
				q.Scope, q.TeamID = "team", hidden
			}
			body, _ := json.Marshal(q)
			switch scenario {
			case "malformed":
				body, want = []byte("{"), http.StatusBadRequest
			case "extra field":
				body, want = []byte(`{"viewer_id":"admin"}`), http.StatusBadRequest
			case "trailing JSON":
				body, want = append(body, []byte(` {}`)...), http.StatusBadRequest
			}
			r := httptest.NewRequest(method, "/stats", strings.NewReader(string(body)))
			r.Header.Set("Mattermost-User-Id", viewer)
			w := httptest.NewRecorder()
			p.ServeHTTP(nil, w, r)
			if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body)
			}
			var result map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result["status_code"] != float64(want) {
				t.Fatalf("client must receive the error status: %s", w.Body)
			}
		})
	}
}

type statsChannelsAPI struct {
	statsTestAPI
	channel *model.Channel
	fail    bool
}

func (a *statsChannelsAPI) GetChannel(id string) (*model.Channel, *model.AppError) {
	return a.channel, nil
}

func (a *statsChannelsAPI) GetTeams() ([]*model.Team, *model.AppError) {
	return []*model.Team{{Id: "other", DisplayName: "Other team"}, {Id: "deleted", DeleteAt: 1}, {Id: "team", DisplayName: "Physics"}}, nil
}

func (a *statsChannelsAPI) GetPublicChannelsForTeam(team string, page, perPage int) ([]*model.Channel, *model.AppError) {
	if team == "deleted" {
		panic("deleted teams must be skipped")
	}
	if a.fail {
		return nil, &model.AppError{Message: "unavailable"}
	}
	if team == "other" {
		return []*model.Channel{{Id: "unjoined", DisplayName: "Unjoined", Type: model.ChannelTypeOpen}}, nil
	}
	if page == 0 {
		channels := []*model.Channel{{Id: "private", Type: model.ChannelTypePrivate}, {Id: "archived", Type: model.ChannelTypeOpen, DeleteAt: 1}, {Id: "dm", Type: model.ChannelTypeDirect}}
		for i := len(channels); i < perPage; i++ {
			channels = append(channels, &model.Channel{Id: fmt.Sprint(i), DisplayName: fmt.Sprint(i), Type: model.ChannelTypeOpen})
		}
		return channels, nil
	}
	return []*model.Channel{{Id: "last", DisplayName: "Last page", Type: model.ChannelTypeOpen}}, nil
}

func TestStatsPublicChannels(t *testing.T) {
	api := &statsChannelsAPI{}
	p := &Plugin{}
	p.SetAPI(api)
	for _, tc := range []struct {
		viewer, method string
		fail           bool
		status         int
	}{{"", "GET", false, 401}, {"viewer", "POST", false, 405}, {"viewer", "GET", true, 503}, {"viewer", "GET", false, 200}} {
		api.fail = tc.fail
		r := httptest.NewRequest(tc.method, "/stats/channels", nil)
		r.Header.Set("Mattermost-User-Id", tc.viewer)
		w := httptest.NewRecorder()
		p.ServeHTTP(nil, w, r)
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status=%d body=%s", w.Code, w.Body)
		}
		if tc.status == 200 {
			var channels []statsChannel
			if err := json.Unmarshal(w.Body.Bytes(), &channels); err != nil || len(channels) != 199 {
				t.Fatalf("expected all pages, no private/archived/DM channels: %d %v", len(channels), err)
			}
			if channels[0].ID != "unjoined" || channels[len(channels)-1].ID != "last" || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "archived") || strings.Contains(w.Body.String(), "\"dm\"") {
				t.Fatalf("incorrect public channel list: %s", w.Body)
			}
		}
	}
	for _, readable := range []string{"selected", "other"} {
		api.readable = readable
		for _, channel := range []*model.Channel{nil, {Type: model.ChannelTypeOpen}, {Type: model.ChannelTypePrivate}, {Type: model.ChannelTypeDirect}, {Type: model.ChannelTypeGroup}, {Type: model.ChannelTypeOpen, DeleteAt: 1}} {
			api.channel = channel
			for _, members := range []bool{false, true} {
				q := validStatsRequest()
				if members {
					q.UsersMode, q.MemberChannel = "channel", "selected"
				} else {
					q.Scope, q.ChannelID = "channel", "selected"
				}
				want := channel != nil && channel.Type == model.ChannelTypeOpen && channel.DeleteAt == 0
				if err := p.authorizeStats("viewer", q); (err == nil) != want {
					t.Fatalf("readable=%s members=%v channel=%+v err=%v", readable, members, channel, err)
				}
			}
		}
	}
}

func TestStatsPostgres(t *testing.T) {
	dsn := os.Getenv("STATS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("Set STATS_TEST_DATABASE_URL to a disposable PostgreSQL database; uses connection-local temporary tables.")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`SET TIME ZONE 'America/Los_Angeles';
		CREATE TEMP TABLE Users (Id text PRIMARY KEY, Username text);
		CREATE TEMP TABLE Channels (Id text PRIMARY KEY, TeamId text, Type text, DeleteAt bigint DEFAULT 0);
		CREATE TEMP TABLE ChannelMembers (ChannelId text, UserId text);
		CREATE TEMP TABLE Posts (Id text, RootId text, UserId text, ChannelId text, CreateAt bigint, DeleteAt bigint, Type text, Message text);
		INSERT INTO Users VALUES ('alice', 'alice'), ('bob', 'bob'), ('idle', 'idle');
		INSERT INTO Channels (Id, TeamId, Type) VALUES ('public', 'teamA', 'O'), ('private', 'teamA', 'P'), ('other', 'teamB', 'P'), ('dm', '', 'D'), ('group', '', 'G');
		INSERT INTO ChannelMembers VALUES ('public', 'alice'), ('public', 'idle');`)
	q := validStatsRequest()
	if err := q.validate(); err != nil {
		t.Fatal(err)
	}
	q.UserIDs = []string{"alice", "bob", "idle"}
	start := q.startTime.UnixMilli()
	post := func(id, root, user, channel, message, kind string, at, deleted int64) {
		exec("INSERT INTO Posts VALUES ($1,$2,$3,$4,$5,$6,$7,$8)", id, root, user, channel, at, deleted, kind, message)
	}
	post("root", "", "alice", "public", "å🙂", "", start, 0)
	post("private", "", "alice", "private", "abc", "", start+86400000-1, 0)
	post("other", "", "alice", "other", "outside", "", start+100, 0)
	post("dm", "", "alice", "dm", "hello", "", start+100, 0)
	post("group", "", "alice", "group", "xy", "", start+100, 0)
	post("next", "", "alice", "public", "123456789", "", start+86400000, 0)
	post("reply", "root", "bob", "public", "", "", start+1, 0)
	post("custom", "", "bob", "public", "abcd", "custom_plugin", start+2, 0)
	post("deleted", "root", "idle", "public", "exclude", "", start+1, 100)
	post("system", "root", "idle", "public", "exclude", "system_join_channel", start+1, 0)
	post("before", "", "alice", "public", "exclude", "", start-1, 0)
	post("after", "", "alice", "public", "exclude", "", q.endTime.UnixMilli(), 0)

	for _, tc := range []struct {
		name  string
		edit  func(*statsRequest)
		users []statsUser
		days  []statsDay
	}{
		{"all includes unseen private", func(q *statsRequest) {}, []statsUser{{"alice", "alice"}, {"bob", "bob"}, {"idle", "idle"}}, []statsDay{{"alice", "2026-03-28", 3, 12}, {"bob", "2026-03-28", 2, 4}, {"alice", "2026-03-29", 1, 9}}},
		{"all DMs includes nonviewer conversations", func(q *statsRequest) { q.IncludeDMs = true }, []statsUser{{"alice", "alice"}, {"bob", "bob"}, {"idle", "idle"}}, []statsDay{{"alice", "2026-03-28", 5, 19}, {"bob", "2026-03-28", 2, 4}, {"alice", "2026-03-29", 1, 9}}},
		{"team includes private but not other team", func(q *statsRequest) { q.Scope, q.TeamID = "team", "teamA" }, []statsUser{{"alice", "alice"}, {"bob", "bob"}, {"idle", "idle"}}, []statsDay{{"alice", "2026-03-28", 2, 5}, {"bob", "2026-03-28", 2, 4}, {"alice", "2026-03-29", 1, 9}}},
		{"channel", func(q *statsRequest) { q.Scope, q.ChannelID = "channel", "public" }, []statsUser{{"alice", "alice"}, {"bob", "bob"}, {"idle", "idle"}}, []statsDay{{"alice", "2026-03-28", 1, 2}, {"bob", "2026-03-28", 2, 4}, {"alice", "2026-03-29", 1, 9}}},
		{"members select users not message scope", func(q *statsRequest) { q.UsersMode, q.MemberChannel = "channel", "public" }, []statsUser{{"alice", "alice"}, {"idle", "idle"}}, []statsDay{{"alice", "2026-03-28", 3, 12}, {"alice", "2026-03-29", 1, 9}}},
		{"thread authors exclude deleted and system", func(q *statsRequest) { q.UsersMode, q.Thread = "thread", "root" }, []statsUser{{"alice", "alice"}, {"bob", "bob"}}, []statsDay{{"alice", "2026-03-28", 3, 12}, {"bob", "2026-03-28", 2, 4}, {"alice", "2026-03-29", 1, 9}}},
		{"empty cohort", func(q *statsRequest) { q.UserIDs = []string{"missing"} }, []statsUser{}, []statsDay{}},
		{"selected user", func(q *statsRequest) { q.UserIDs = []string{"bob", "bob"} }, []statsUser{{"bob", "bob"}}, []statsDay{{"bob", "2026-03-28", 2, 4}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := q
			tc.edit(&request)
			got, err := queryStats(context.Background(), db, request)
			want := &statsResponse{Users: tc.users, Days: tc.days}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("got=%+v want=%+v err=%v", got, want, err)
			}
		})
	}
	old := time.Date(2010, 1, 2, 0, 0, 0, 0, time.UTC).UnixMilli()
	post("old-private", "", "bob", "private", "old", "", old, 0)
	post("old-other", "", "bob", "other", "other", "", old, 0)
	post("old-dm", "", "bob", "dm", "dm", "", old, 0)
	for _, tc := range []struct {
		scope string
		dms   bool
		days  []statsDay
	}{
		{"all", false, []statsDay{{"bob", "2010-01-02", 2, 8}, {"bob", "2026-03-28", 2, 4}}},
		{"all", true, []statsDay{{"bob", "2010-01-02", 3, 10}, {"bob", "2026-03-28", 2, 4}}},
		{"team", false, []statsDay{{"bob", "2010-01-02", 1, 3}, {"bob", "2026-03-28", 2, 4}}},
		{"channel", false, []statsDay{}},
	} {
		t.Run("all time "+tc.scope+fmt.Sprint(tc.dms), func(t *testing.T) {
			request := q
			request.AllTime, request.Start, request.End = true, "", ""
			request.Scope, request.IncludeDMs = tc.scope, tc.dms
			request.TeamID, request.ChannelID = "teamA", "private"
			request.UserIDs = []string{"bob"}
			got, err := queryStats(context.Background(), db, request)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Days, tc.days) {
				t.Fatalf("got=%+v want=%+v", got.Days, tc.days)
			}
		})
	}

	exec(`UPDATE Channels SET Type = 'P' WHERE Id = 'public';`)
	q.UsersMode, q.MemberChannel = "channel", "public"
	if got, err := queryStats(context.Background(), db, q); err != nil || len(got.Users) != 0 {
		t.Fatalf("private channel cohort must not return members: %+v %v", got, err)
	}

	exec(`INSERT INTO Channels (Id, TeamId, Type) VALUES ('large', 'teamA', 'O');
		INSERT INTO Users SELECT 'extra' || n, 'extra' || n FROM generate_series(1, 201) n;
		INSERT INTO ChannelMembers SELECT 'large', Id FROM Users WHERE Id LIKE 'extra%';`)
	q.UsersMode, q.MemberChannel = "channel", "large"
	if _, err := queryStats(context.Background(), db, q); !errors.Is(err, errStatsTooManyUsers) {
		t.Fatalf("large cohort was silently truncated: %v", err)
	}
}
