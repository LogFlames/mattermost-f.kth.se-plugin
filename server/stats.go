package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

const statsMaxUsers = 200

type statsRequest struct {
	Scope         string   `json:"scope"`
	TeamID        string   `json:"team_id"`
	ChannelID     string   `json:"channel_id"`
	IncludeDMs    bool     `json:"include_dms"`
	UsersMode     string   `json:"users_mode"`
	UserIDs       []string `json:"user_ids"`
	MemberChannel string   `json:"member_channel"`
	Thread        string   `json:"thread"`
	AllTime       bool     `json:"all_time"`
	Start         string   `json:"start"`
	End           string   `json:"end"`
	startTime     time.Time
	endTime       time.Time
}

type statsUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type statsDay struct {
	UserID     string `json:"user_id"`
	Date       string `json:"date"`
	Messages   int64  `json:"messages"`
	Characters int64  `json:"characters"`
}

type statsResponse struct {
	Users []statsUser `json:"users"`
	Days  []statsDay  `json:"days"`
}

func (q *statsRequest) validate() error {
	if q.AllTime {
		if q.Start != "" || q.End != "" {
			return errors.New("All time cannot be combined with start or end dates.")
		}
	} else {
		var err error
		q.startTime, err = time.Parse(time.DateOnly, q.Start)
		if err != nil {
			return errors.New("Choose a valid start date.")
		}
		q.endTime, err = time.Parse(time.DateOnly, q.End)
		if err != nil || q.endTime.Before(q.startTime) || q.endTime.Sub(q.startTime) >= 366*24*time.Hour {
			return errors.New("Choose a date range of 1–366 days.")
		}
		q.endTime = q.endTime.AddDate(0, 0, 1)
	}
	switch q.Scope {
	case "all":
	case "team":
		if !model.IsValidId(q.TeamID) {
			return errors.New("Choose a team.")
		}
	case "channel":
		if !model.IsValidId(q.ChannelID) {
			return errors.New("Choose a channel.")
		}
	default:
		return errors.New("Choose All, Specific team, or Specific channel.")
	}
	if q.Scope != "all" && q.IncludeDMs {
		return errors.New("Include DMs is only available with All.")
	}
	switch q.UsersMode {
	case "choose":
		if len(q.UserIDs) == 0 || len(q.UserIDs) > statsMaxUsers {
			return fmt.Errorf("Choose 1–%d users.", statsMaxUsers)
		}
		for _, id := range q.UserIDs {
			if !model.IsValidId(id) {
				return errors.New("Invalid user ID.")
			}
		}
	case "channel":
		if !model.IsValidId(q.MemberChannel) {
			return errors.New("Choose a channel for its members.")
		}
	case "thread":
		q.Thread = strings.TrimSpace(q.Thread)
		if !model.IsValidId(q.Thread) {
			u, err := url.Parse(q.Thread)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				return errors.New("Enter a root message ID or a message permalink.")
			}
			parts := strings.Split(strings.TrimRight(u.Path, "/"), "/")
			if len(parts) < 3 || parts[len(parts)-2] != "pl" || !model.IsValidId(parts[len(parts)-1]) {
				return errors.New("Enter a root message ID or a message permalink.")
			}
			q.Thread = parts[len(parts)-1]
		}
	default:
		return errors.New("Choose users, channel members, or thread members.")
	}
	return nil
}

func (p *Plugin) authorizeStats(viewer string, q statsRequest) error {
	channels := []string{}
	if q.Scope == "channel" {
		channels = append(channels, q.ChannelID)
	}
	if q.UsersMode == "channel" {
		channels = append(channels, q.MemberChannel)
	}
	for _, channelID := range channels {
		channel, err := p.API.GetChannel(channelID)
		if err != nil || channel == nil || channel.Type != model.ChannelTypeOpen || channel.DeleteAt != 0 {
			return errors.New("Choose an active public channel.")
		}
	}
	if q.UsersMode == "thread" {
		post, err := p.API.GetPost(q.Thread)
		if err != nil || post == nil || post.DeleteAt != 0 || post.RootId != "" || !p.API.HasPermissionToChannel(viewer, post.ChannelId, model.PermissionReadChannel) {
			return errors.New("That root message is unavailable or you cannot view its thread.")
		}
	}
	return nil
}

func (p *Plugin) serveStats(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, message string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": message, "status_code": status})
	}
	viewer := r.Header.Get("Mattermost-User-Id")
	if viewer == "" {
		fail(http.StatusUnauthorized, "Sign in to view message statistics.")
		return
	}
	if r.URL.Path == "/stats/channels" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			fail(http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		channels, err := p.statsPublicChannels()
		if err != nil {
			fail(http.StatusServiceUnavailable, "Could not load public channels.")
			return
		}
		_ = json.NewEncoder(w).Encode(channels)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		fail(http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	var q statsRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&q); err != nil {
		fail(http.StatusBadRequest, "Invalid statistics request.")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		fail(http.StatusBadRequest, "Invalid statistics request.")
		return
	}
	if err := q.validate(); err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	if err := p.authorizeStats(viewer, q); err != nil {
		fail(http.StatusForbidden, err.Error())
		return
	}
	if !p.statsLock.TryLock() {
		w.Header().Set("Retry-After", "5")
		fail(http.StatusTooManyRequests, "Statistics are busy. Try again in a few seconds.")
		return
	}
	defer p.statsLock.Unlock()
	if p.statsStore == nil {
		p.statsStore = pluginapi.NewClient(p.API, p.Driver)
	}
	db, err := p.statsStore.Store.GetMasterDB()
	if err != nil {
		fail(http.StatusServiceUnavailable, "Statistics database is unavailable.")
		return
	}
	result, err := queryStats(r.Context(), db, q)
	if errors.Is(err, errStatsTooManyUsers) {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		fail(http.StatusServiceUnavailable, "Could not load statistics. Try a smaller date range or fewer users.")
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}

var errStatsTooManyUsers = fmt.Errorf("This group has more than %d users. Choose a smaller group or select users individually.", statsMaxUsers)

type statsChannel struct {
	ID              string `json:"id"`
	DisplayName     string `json:"display_name"`
	TeamDisplayName string `json:"team_display_name"`
}

func (p *Plugin) statsPublicChannels() ([]statsChannel, error) {
	teams, err := p.API.GetTeams()
	if err != nil {
		return nil, err
	}
	result := []statsChannel{}
	for _, team := range teams {
		if team.DeleteAt != 0 {
			continue
		}
		for page := 0; ; page++ {
			channels, err := p.API.GetPublicChannelsForTeam(team.Id, page, 200)
			if err != nil {
				return nil, err
			}
			for _, channel := range channels {
				if channel.Type == model.ChannelTypeOpen && channel.DeleteAt == 0 {
					result = append(result, statsChannel{channel.Id, channel.DisplayName, team.DisplayName})
				}
			}
			if len(channels) < 200 {
				break
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].TeamDisplayName+"\x00"+result[i].DisplayName+"\x00"+result[i].ID < result[j].TeamDisplayName+"\x00"+result[j].DisplayName+"\x00"+result[j].ID
	})
	return result, nil
}

func queryStats(ctx context.Context, db *sql.DB, q statsRequest) (*statsResponse, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// The plugin SQL RPC does not propagate context cancellation to PostgreSQL.
	for _, statement := range []string{"SET TRANSACTION READ ONLY", "SET LOCAL statement_timeout = '15s'"} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return nil, err
		}
	}
	result := &statsResponse{Users: []statsUser{}, Days: []statsDay{}}
	query, args := statsUsersSQL(q)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var user statsUser
		if err := rows.Scan(&user.ID, &user.Username); err != nil {
			rows.Close()
			return nil, err
		}
		result.Users = append(result.Users, user)
		ids = append(ids, user.ID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(ids) > statsMaxUsers {
		return nil, errStatsTooManyUsers
	}
	if len(ids) == 0 {
		return result, nil
	}
	query, args = statsDaysSQL(q, ids)
	rows, err = tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var day statsDay
		if err := rows.Scan(&day.UserID, &day.Date, &day.Messages, &day.Characters); err != nil {
			return nil, err
		}
		result.Days = append(result.Days, day)
	}
	return result, rows.Err()
}

func statsUsersSQL(q statsRequest) (string, []any) {
	args := []any{}
	where := ""
	switch q.UsersMode {
	case "choose":
		where = "u.Id IN (" + statsPlaceholders(q.UserIDs, &args) + ")"
	case "channel":
		where = "u.Id IN (SELECT cm.UserId FROM ChannelMembers cm JOIN Channels c ON c.Id = cm.ChannelId WHERE c.Id = $1 AND c.Type = 'O' AND c.DeleteAt = 0)"
		args = append(args, q.MemberChannel)
	case "thread":
		where = "u.Id IN (SELECT UserId FROM Posts WHERE (Id = $1 OR RootId = $1) AND DeleteAt = 0 AND LEFT(Type, 7) <> 'system_')"
		args = append(args, q.Thread)
	}
	return "SELECT u.Id, u.Username FROM Users u WHERE " + where + " ORDER BY u.Username, u.Id LIMIT 201", args
}

func statsDaysSQL(q statsRequest, ids []string) (string, []any) {
	args := []any{}
	where := "c.Type IN ('O', 'P')"
	if q.Scope == "all" && q.IncludeDMs {
		where = "c.Type IN ('O', 'P', 'D', 'G')"
	}
	if q.Scope == "channel" {
		where = "c.Type = 'O' AND c.DeleteAt = 0"
	}
	if !q.AllTime {
		args = append(args, q.startTime.UnixMilli(), q.endTime.UnixMilli())
		where += " AND p.CreateAt >= $1 AND p.CreateAt < $2"
	}
	if q.Scope == "team" {
		args = append(args, q.TeamID)
		where += fmt.Sprintf(" AND c.TeamId = $%d", len(args))
	} else if q.Scope == "channel" {
		args = append(args, q.ChannelID)
		where += fmt.Sprintf(" AND c.Id = $%d", len(args))
	}
	users := statsPlaceholders(ids, &args)
	// Server-wide aggregates intentionally include private channels, without
	// exposing per-channel breakdowns. Specific-channel access is checked above.
	return `SELECT p.UserId,
		to_char(to_timestamp(p.CreateAt / 1000.0) AT TIME ZONE 'UTC', 'YYYY-MM-DD') AS day,
		count(*), coalesce(sum(char_length(p.Message)), 0)
		FROM Posts p JOIN Channels c ON c.Id = p.ChannelId
		WHERE p.DeleteAt = 0 AND LEFT(p.Type, 7) <> 'system_'
		AND ` + where + ` AND p.UserId IN (` + users + `)
		GROUP BY p.UserId, day ORDER BY day, p.UserId`, args
}

func statsPlaceholders(ids []string, args *[]any) string {
	placeholders := make([]string, 0, len(ids))
	for _, id := range ids {
		*args = append(*args, id)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(*args)))
	}
	return strings.Join(placeholders, ",")
}
