package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestGoogleGroupAlias(t *testing.T) {
	api := &googleGroupSyncTestAPI{
		categoryTestAPI: &categoryTestAPI{},
		teamAdmins:      map[string]bool{"admin": true},
	}
	p := &Plugin{}
	p.SetAPI(api)
	for _, suffix := range []string{
		"", " unknown", " add", " remove", " manage", " sync",
		" sync unknown", " sync now extra", " sync list extra", " sync start", " sync stop",
	} {
		t.Run(suffix, func(t *testing.T) {
			original := &model.CommandArgs{Command: "/google group" + suffix, UserId: "admin", TeamId: "team"}
			alias := &model.CommandArgs{Command: "/ggroup" + suffix, UserId: "admin", TeamId: "team"}
			want, wantErr := p.ExecuteCommand(nil, original)
			got, gotErr := p.ExecuteCommand(nil, alias)
			if wantErr != nil || gotErr != nil || want == nil || got == nil {
				t.Fatalf("original=%+v, %v; alias=%+v, %v", want, wantErr, got, gotErr)
			}
			expected := strings.Replace(want.Text, "/google group", "/ggroup", 1)
			if !strings.HasPrefix(want.Text, "Usage: ") || got.Text != expected || got.ResponseType != want.ResponseType {
				t.Errorf("original=%+v; alias=%+v; expected text %q", want, got, expected)
			}
			if alias.Command != "/ggroup"+suffix {
				t.Errorf("alias command was mutated: %q", alias.Command)
			}
		})
	}

	for _, command := range []string{"/google group sync now", "/ggroup sync now"} {
		response, err := p.ExecuteCommand(nil, &model.CommandArgs{Command: command, UserId: "user", TeamId: "team"})
		if err != nil || response == nil || response.Text != "Only system or team admins can manage Google Group syncs." {
			t.Errorf("%s: response=%+v, error=%v", command, response, err)
		}
	}
}

func TestGoogleGroupConfirmationRequesterDisplay(t *testing.T) {
	for _, tc := range []struct {
		name           string
		requesterEmail string
		actingEmail    string
		want           string
	}{
		{"caller", "", "caller@example.org", "`caller@example.org`"},
		{"admin delegation", "caller@example.org", "admin@example.org", "`caller@example.org`"},
		{"fallback", "", "FALLBACK@example.org", "an external account"},
		{"fallback alternate domain", "", "fallback@alternate.example", "an external account"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := &googleGroupSyncTestAPI{categoryTestAPI: &categoryTestAPI{
				kv: map[string][]byte{}, user: &model.User{Id: "caller", Email: "caller@example.org"},
			}}
			p := &Plugin{configuration: &configuration{GoogleGroupsSyncDomain: "example.org", GoogleGroupsSyncAlternateDomain: "alternate.example", GoogleGroupsSyncFallbackEmail: "fallback@example.org"}}
			p.SetAPI(api)
			response, err := p.openGoogleGroupConfirmationText(
				&model.CommandArgs{UserId: "caller", TriggerId: "trigger"},
				googleGroupConfirmation{UserID: "caller", RequesterEmail: tc.requesterEmail, ActingEmail: tc.actingEmail},
				"Review the operation.",
			)
			if err != nil || response == nil || !strings.Contains(response.Text, "confirm") {
				t.Fatalf("opening confirmation: %+v, %v", response, err)
			}
			want := "Review the operation.\n\nYou are performing this action as " + tc.want
			if api.dialog.Dialog.IntroductionText != want {
				t.Fatalf("introduction = %q; want %q", api.dialog.Dialog.IntroductionText, want)
			}
		})
	}
}

func TestNormalizeGoogleGroupEmail(t *testing.T) {
	p := &Plugin{configuration: &configuration{GoogleGroupsSyncDomain: "fysiksektionen.se"}}
	for _, tc := range []struct {
		input string
		want  string
		ok    bool
	}{
		{input: " team@fysiksektionen.se ", want: "team@fysiksektionen.se", ok: true},
		{input: " TeStGroup_261024.Mattermost ", want: "testgroup_261024.mattermost@fysiksektionen.se", ok: true},
		{input: "team@other.example", want: "team@other.example", ok: true},
		{input: "", ok: false},
		{input: "team@", ok: false},
		{input: "bad group", ok: false},
		{input: "Name <team@fysiksektionen.se>", ok: false},
		{input: "team @fysiksektionen.se", ok: false},
	} {
		got, err := p.normalizeGoogleGroupEmail(tc.input)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("normalizeGoogleGroupEmail(%q) = %q, %v; want %q, ok=%t", tc.input, got, err, tc.want, tc.ok)
		}
	}
	p = &Plugin{configuration: &configuration{GoogleGroupsSyncDomain: "example.org"}}
	if got, err := p.normalizeGoogleGroupEmail("team"); err != nil || got != "team@example.org" {
		t.Fatalf("configured domain was not used: %q, %v", got, err)
	}
	p = &Plugin{}
	if _, err := p.normalizeGoogleGroupEmail("team"); err == nil || !strings.Contains(err.Error(), "configure") {
		t.Fatalf("missing domain was not reported: %v", err)
	}
	if got, err := p.normalizeGoogleGroupEmail("team@other.example"); err != nil || got != "team@other.example" {
		t.Fatalf("full address rejected without a configured domain: %q, %v", got, err)
	}
}

func TestParseGoogleGroupDeadlineInclusiveUTCDate(t *testing.T) {
	now := time.Date(2026, 11, 23, 14, 0, 0, 0, time.FixedZone("test", 3600))
	got, err := parseGoogleGroupDeadline("2026-11-23", now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 11, 23, 23, 59, 59, 999_000_000, time.UTC).UnixMilli()
	if got != want {
		t.Fatalf("got %s, want inclusive UTC deadline %s", time.UnixMilli(got), time.UnixMilli(want))
	}
	for _, invalid := range []string{"2026-11-22", "2026-2-23", "not-a-date"} {
		if _, err := parseGoogleGroupDeadline(invalid, now); err == nil {
			t.Errorf("parseGoogleGroupDeadline(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestGoogleGroupConfirmationShowsAlphabeticalFive(t *testing.T) {
	snapshot := &googleDirectoryGroupSnapshot{
		Group: googleDirectoryGroup{Email: "team@fysiksektionen.se", Name: "Team"},
		Members: []googleDirectoryGroupMember{
			{Email: "f@fysiksektionen.se"},
			{Email: "e@fysiksektionen.se"},
			{Email: "c@fysiksektionen.se"},
			{Email: "a@fysiksektionen.se"},
			{Email: "d@fysiksektionen.se"},
			{Email: "b@fysiksektionen.se"},
		},
	}
	introduction := formatGoogleGroupConfirmationIntroduction(snapshot, "general", "Confirm?", snapshot.Members, 0)
	if !strings.HasPrefix(introduction, "Confirm?\n\n> **Google Group details**\n>") {
		t.Fatalf("confirmation does not quote group details after the action: %s", introduction)
	}
	for _, line := range strings.Split(strings.SplitN(introduction, "\n\n", 2)[1], "\n") {
		if !strings.HasPrefix(line, ">") {
			t.Fatalf("group detail is outside the block quote: %q", line)
		}
	}
	for _, want := range []string{"Team", "team@fysiksektionen.se", "Members:** 6", "a@fysiksektionen.se", "e@fysiksektionen.se", "~general"} {
		if !strings.Contains(introduction, want) {
			t.Errorf("confirmation introduction does not contain %q: %s", want, introduction)
		}
	}
	if strings.Contains(introduction, "f@fysiksektionen.se") {
		t.Fatalf("confirmation shows more than the first five members: %s", introduction)
	}
	lastPosition := -1
	for _, email := range []string{"a@", "b@", "c@", "d@", "e@"} {
		position := strings.Index(introduction, email)
		if position <= lastPosition {
			t.Fatalf("confirmation members were not sorted alphabetically: %s", introduction)
		}
		lastPosition = position
	}
	snapshot.Group.Name = ""
	introduction = formatGoogleGroupConfirmationIntroduction(snapshot, "", "", snapshot.Members, 0)
	if strings.Count(introduction, "team@fysiksektionen.se") != 1 {
		t.Fatalf("group address was repeated when no name is available: %s", introduction)
	}
}

func TestGoogleGroupConfirmationHidesUnmatchedGroupAddresses(t *testing.T) {
	api := &googleGroupSyncTestAPI{
		categoryTestAPI: &categoryTestAPI{kv: map[string][]byte{}},
		users: map[string]*model.User{
			"nested-user@example.org": {Id: "nested-user", Email: "nested-user@example.org"},
		},
	}
	p := &Plugin{configuration: &configuration{GoogleGroupsSyncDomain: "example.org"}}
	p.SetAPI(api)
	snapshot := &googleDirectoryGroupSnapshot{
		Group: googleDirectoryGroup{Email: "team@example.org", Name: "Team"},
		Members: []googleDirectoryGroupMember{
			{Email: "nested-missing@example.org", Type: "GROUP"},
			{Email: "another-nested-missing@example.org", Type: "GROUP"},
			{Email: "nested-user@example.org", Type: "GROUP"},
			{Email: "external@outside.test", Type: "USER"},
			{Email: "missing-user@outside.test", Type: "USER"},
		},
	}
	introduction, err := p.googleGroupConfirmationIntroduction(snapshot, "general", "Confirm?")
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"nested-user@example.org", "external@outside.test", "missing-user@outside.test"} {
		if !strings.Contains(introduction, address) {
			t.Errorf("confirmation omitted reportable address %q: %s", address, introduction)
		}
	}
	for _, address := range []string{"nested-missing@example.org", "another-nested-missing@example.org"} {
		if strings.Contains(introduction, address) {
			t.Fatalf("confirmation exposed a group address with no Mattermost user: %s", introduction)
		}
	}
	if !strings.Contains(introduction, "(and some subgroups)") {
		t.Fatalf("confirmation did not disclose omitted subgroups: %s", introduction)
	}
}

func TestGoogleGroupActingEmailUsesCallerOrFallback(t *testing.T) {
	base := &categoryTestAPI{user: &model.User{Id: "user", Email: "person@fysiksektionen.se"}}
	p := &Plugin{configuration: &configuration{GoogleGroupsSyncDomain: "fysiksektionen.se", GoogleGroupsSyncFallbackEmail: "lonely-mattermost-utility-account@fysiksektionen.se"}}
	api := &googleGroupSyncTestAPI{categoryTestAPI: base}
	p.SetAPI(api)

	got, err := p.googleGroupActingEmail("user")
	if err != nil || got != "person@fysiksektionen.se" {
		t.Fatalf("domain user delegation = %q, %v", got, err)
	}
	base.user.Email = "external@example.org"
	got, err = p.googleGroupActingEmail("user")
	if err != nil || got != "lonely-mattermost-utility-account@fysiksektionen.se" {
		t.Fatalf("external user delegation = %q, %v", got, err)
	}
}

func TestGoogleOrganizationEmailMapsAlternateDomain(t *testing.T) {
	p, api := newDefaultChannelTestPlugin(t)
	api.settings["GoogleGroupsSyncDomain"] = "example.org"
	api.settings["GoogleGroupsSyncAlternateDomain"] = " alternate.example "
	if err := p.OnConfigurationChange(); err != nil {
		t.Fatal(err)
	}
	if got, ok := p.googleOrganizationEmail("person@ALTERNATE.EXAMPLE"); !ok || got != "person@example.org" {
		t.Fatalf("alternate address was not mapped to the configured organization: %q, ok=%t", got, ok)
	}
	if _, ok := p.googleOrganizationEmail("person@f.kth.se"); ok {
		t.Fatal("unconfigured alternate domain was accepted")
	}
	api.settings["GoogleGroupsSyncAlternateDomain"] = ""
	if err := p.OnConfigurationChange(); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.googleOrganizationEmail("person@alternate.example"); ok {
		t.Fatal("cleared alternate domain was accepted")
	}
}

func TestGoogleGroupDelegatesAlternateDomainUser(t *testing.T) {
	api := &googleGroupSyncTestAPI{categoryTestAPI: &categoryTestAPI{user: &model.User{Id: "user", Email: "person@alternate.example"}}}
	p := &Plugin{configuration: &configuration{GoogleGroupsSyncDomain: "example.org", GoogleGroupsSyncAlternateDomain: "alternate.example", GoogleGroupsSyncFallbackEmail: "fallback@example.org"}}
	p.SetAPI(api)
	for _, getEmail := range []func(string) (string, error){p.googleGroupActingEmail, p.googleGroupOwnActingEmail} {
		if got, err := getEmail("user"); err != nil || got != "person@example.org" {
			t.Fatalf("alternate-domain delegation = %q, %v", got, err)
		}
	}
	p.configuration.GoogleGroupsSyncFallbackEmail = "person@example.org"
	if _, err := p.googleGroupOwnActingEmail("user"); err == nil {
		t.Fatal("alternate address for fallback account was accepted for management")
	}
}

func TestGoogleGroupDelegationConfiguration(t *testing.T) {
	base := &categoryTestAPI{user: &model.User{Id: "user", Email: "person@example.org"}}
	config := &configuration{GoogleGroupsSyncDomain: "example.org", GoogleGroupsSyncFallbackEmail: "fallback@example.org", GoogleGroupsSyncAdminEmail: "admin@example.org", GoogleGroupsSyncAdminUsers: []string{"user"}}
	p := &Plugin{configuration: config}
	p.SetAPI(&googleGroupSyncTestAPI{categoryTestAPI: base})
	if got, err := p.googleGroupActingEmail("user"); err != nil || got != "admin@example.org" {
		t.Fatalf("selected admin delegation = %q, %v", got, err)
	}
	if got, err := p.googleGroupOwnActingEmail("user"); err != nil || got != "person@example.org" {
		t.Fatalf("management must use caller's account = %q, %v", got, err)
	}
	config.GoogleGroupsSyncAdminEmail = ""
	if _, err := p.googleGroupActingEmail("user"); err == nil {
		t.Fatal("selected user must not silently fall back when admin email is missing")
	}
	config.GoogleGroupsSyncAdminUsers = nil
	base.user.Email = "outside@other.org"
	if got, err := p.googleGroupActingEmail("user"); err != nil || got != "fallback@example.org" {
		t.Fatalf("configured fallback delegation = %q, %v", got, err)
	}
	if _, err := p.googleGroupOwnActingEmail("user"); err == nil {
		t.Fatal("management used fallback for external caller")
	}
	config.GoogleGroupsSyncDomain = ""
	if _, err := p.googleGroupActingEmail("user"); err == nil {
		t.Fatal("missing organization domain should not use a hardcoded default")
	}
}

func TestGoogleUtilityAccountIsNeverAGroupMember(t *testing.T) {
	p := &Plugin{configuration: &configuration{GoogleGroupsSyncDomain: "fysiksektionen.se", GoogleGroupsSyncFallbackEmail: "another-fallback@fysiksektionen.se"}}
	if p.isGoogleFallbackEmail("lonely-mattermost-utility-account@fysiksektionen.se") {
		t.Fatal("only the configured fallback account should be protected")
	}
	if !p.isGoogleFallbackEmail("another-fallback@fysiksektionen.se") {
		t.Fatal("the configured fallback account must be protected")
	}
}

func TestGoogleGroupSyncOnlyAddsEligibleUsers(t *testing.T) {
	base := &categoryTestAPI{kv: map[string][]byte{}}
	api := &googleGroupSyncTestAPI{
		categoryTestAPI: base,
		users: map[string]*model.User{
			"existing@fysiksektionen.se": {Id: "existing", Email: "existing@fysiksektionen.se"},
			"add@fysiksektionen.se":      {Id: "add", Email: "add@fysiksektionen.se"},
			"deleted@fysiksektionen.se":  {Id: "deleted", Email: "deleted@fysiksektionen.se", DeleteAt: 1},
			"no-team@fysiksektionen.se":  {Id: "no-team", Email: "no-team@fysiksektionen.se"},
			"nested@fysiksektionen.se":   {Id: "nested", Email: "nested@fysiksektionen.se"},
			"unknown@fysiksektionen.se":  {Id: "unknown", Email: "unknown@fysiksektionen.se"},
		},
		teamMembers: map[string]*model.TeamMember{
			"existing": {TeamId: "team", UserId: "existing"},
			"add":      {TeamId: "team", UserId: "add"},
			"deleted":  {TeamId: "team", UserId: "deleted"},
			"nested":   {TeamId: "team", UserId: "nested"},
			"unknown":  {TeamId: "team", UserId: "unknown"},
		},
		channelMembers: map[string]*model.ChannelMember{
			"existing": {ChannelId: "channel", UserId: "existing"},
		},
	}
	p := &Plugin{configuration: &configuration{GoogleGroupsSyncDomain: "fysiksektionen.se", GoogleGroupsSyncFallbackEmail: "lonely-mattermost-utility-account@fysiksektionen.se"}}
	p.SetAPI(api)
	snapshot := &googleDirectoryGroupSnapshot{Members: []googleDirectoryGroupMember{
		{Email: "existing@fysiksektionen.se", Type: "USER"},
		{Email: "add@fysiksektionen.se", Type: "USER"},
		{Email: "deleted@fysiksektionen.se", Type: "USER"},
		{Email: "no-team@fysiksektionen.se", Type: "USER"},
		{Email: "missing@fysiksektionen.se", Type: "USER"},
		{Email: "nested@fysiksektionen.se", Type: "GROUP"},
		{Email: "unknown@fysiksektionen.se"},
		{Email: "lonely-mattermost-utility-account@fysiksektionen.se", Type: "USER"},
	}}

	added, err := p.syncGoogleGroupMembers(snapshot, googleGroupSync{TeamID: "team", ChannelID: "channel"})
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 || !reflect.DeepEqual(api.added, []string{"add"}) {
		t.Fatalf("added count/users = %d/%v; want 1/[add]", added, api.added)
	}
	if _, remains := api.channelMembers["existing"]; !remains {
		t.Fatal("sync removed an existing channel member")
	}
}

func TestGoogleGroupTeamSyncAddsOnlyEligibleUsers(t *testing.T) {
	api := &googleGroupSyncTestAPI{
		categoryTestAPI: &categoryTestAPI{kv: map[string][]byte{}},
		users: map[string]*model.User{
			"existing@example.org": {Id: "existing", Email: "existing@example.org"},
			"add@example.org":      {Id: "add", Email: "add@example.org"},
			"deleted@example.org":  {Id: "deleted", Email: "deleted@example.org", DeleteAt: 1},
			"bot@example.org":      {Id: "bot", Email: "bot@example.org", IsBot: true},
			"fallback@example.org": {Id: "fallback", Email: "fallback@example.org"},
		},
		teamMembers: map[string]*model.TeamMember{
			"existing": {TeamId: "team", UserId: "existing"},
		},
	}
	p := &Plugin{configuration: &configuration{
		GoogleGroupsSyncDomain: "example.org", GoogleGroupsSyncFallbackEmail: "fallback@example.org",
	}}
	p.SetAPI(api)
	snapshot := &googleDirectoryGroupSnapshot{Members: []googleDirectoryGroupMember{
		{Email: "existing@example.org", Type: "USER"},
		{Email: "add@example.org", Type: "USER"},
		{Email: "deleted@example.org", Type: "USER"},
		{Email: "bot@example.org", Type: "USER"},
		{Email: "missing@example.org", Type: "USER"},
		{Email: "fallback@example.org", Type: "USER"},
		{Email: "group@example.org", Type: "GROUP"},
	}}
	added, err := p.syncGoogleGroupTeamMembers(snapshot, "team")
	if err != nil || added != 1 || !reflect.DeepEqual(api.teamAdded, []string{"add"}) {
		t.Fatalf("added count/users = %d/%v, err=%v; want 1/[add]", added, api.teamAdded, err)
	}

	api.teamMembers["remove"] = &model.TeamMember{TeamId: "team", UserId: "remove"}
	api.users["remove@example.org"] = &model.User{Id: "remove", Email: "remove@example.org"}
	api.teamMembers["requester"] = &model.TeamMember{TeamId: "team", UserId: "requester"}
	api.users["requester@example.org"] = &model.User{Id: "requester", Email: "requester@example.org"}
	removeSnapshot := &googleDirectoryGroupSnapshot{Members: []googleDirectoryGroupMember{
		{Email: "remove@example.org", Type: "USER"},
		{Email: "requester@example.org", Type: "USER"},
	}}
	removed, err := p.removeGoogleGroupTeamMembers(removeSnapshot, "team", "requester")
	if err != nil || removed != 1 || !reflect.DeepEqual(api.teamRemoved, []string{"remove"}) {
		t.Fatalf("removed count/users = %d/%v, err=%v; want 1/[remove]", removed, api.teamRemoved, err)
	}
}

func TestGoogleGroupSyncScheduleUses23rdMinute(t *testing.T) {
	tests := []struct {
		name       string
		lastSync   time.Time
		now        time.Time
		wantNext   time.Time
		wantWorker time.Time
	}{
		{
			name:       "sync before minute 23",
			lastSync:   time.Date(2026, time.January, 2, 16, 5, 0, 0, time.UTC),
			now:        time.Date(2026, time.January, 2, 16, 10, 0, 0, time.UTC),
			wantNext:   time.Date(2026, time.January, 2, 16, 23, 0, 0, time.UTC),
			wantWorker: time.Date(2026, time.January, 2, 16, 23, 0, 0, time.UTC),
		},
		{
			name:       "sync at minute 23 queues next hour",
			lastSync:   time.Date(2026, time.January, 2, 16, 23, 5, 0, time.UTC),
			now:        time.Date(2026, time.January, 2, 16, 24, 0, 0, time.UTC),
			wantNext:   time.Date(2026, time.January, 2, 17, 23, 0, 0, time.UTC),
			wantWorker: time.Date(2026, time.January, 2, 17, 23, 0, 0, time.UTC),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			job := googleGroupSync{LastSyncedAt: tc.lastSync.UnixMilli(), DeadlineAt: tc.now.Add(24 * time.Hour).UnixMilli()}
			if got := googleGroupNextSyncAt(job, tc.now); got != tc.wantNext.UnixMilli() {
				t.Errorf("next sync = %s; want %s", time.UnixMilli(got).UTC(), tc.wantNext)
			}
			if got := nextGoogleGroupSyncRun(tc.now); !got.Equal(tc.wantWorker) {
				t.Errorf("worker run = %s; want %s", got, tc.wantWorker)
			}
		})
	}
}

func TestFormatGoogleGroupSyncTimeUsesStockholmDST(t *testing.T) {
	location, err := time.LoadLocation(googleGroupSyncDisplayTimeZone)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		time time.Time
		want string
	}{
		{
			name: "winter",
			time: time.Date(2026, time.January, 2, 16, 23, 0, 0, time.UTC),
			want: "2026-01-02 17:23 CET",
		},
		{
			name: "summer",
			time: time.Date(2026, time.July, 2, 16, 23, 0, 0, time.UTC),
			want: "2026-07-02 18:23 CEST",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatGoogleGroupSyncTime(tc.time.UnixMilli(), location); got != tc.want {
				t.Errorf("formatted sync time = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestGoogleGroupTeamCommandAuthorizationAndChannelRequirements(t *testing.T) {
	base := &categoryTestAPI{
		kv:      map[string][]byte{},
		channel: model.Channel{Id: "channel", Name: "general", TeamId: "team", Type: model.ChannelTypeOpen},
	}
	api := &googleGroupSyncTestAPI{
		categoryTestAPI: base,
		teamAdmins:      map[string]bool{"requester": true},
		channelMembers:  map[string]*model.ChannelMember{"requester": {ChannelId: "channel", UserId: "requester"}},
	}
	p := &Plugin{configuration: &configuration{GoogleGroupsSyncDomain: "example.org"}}
	p.SetAPI(api)

	args := &model.CommandArgs{Command: "/google group team add group@example.org", UserId: "requester", TeamId: "team", ChannelId: "channel"}
	response, appErr := p.googleGroupTeamCommand(args)
	if appErr != nil || !strings.Contains(response.Text, "permission to add members") {
		t.Fatalf("team add without add permission: response=%+v err=%v", response, appErr)
	}

	args.Command = "/google group team remove group@example.org"
	response, appErr = p.googleGroupTeamCommand(args)
	if appErr != nil || !strings.Contains(response.Text, "permission to remove members") {
		t.Fatalf("team remove without remove permission: response=%+v err=%v", response, appErr)
	}

	api.teamPermissions = map[string]map[*model.Permission]bool{
		"requester": {model.PermissionAddUserToTeam: true},
	}
	args.Command = "/google group team add group@example.org"
	args.ChannelId = ""
	response, appErr = p.googleGroupTeamCommand(args)
	if appErr != nil || !strings.Contains(response.Text, "active public or private team channel") {
		t.Fatalf("team add outside a channel: response=%+v err=%v", response, appErr)
	}

	args.Command = "/google group team sync start group@example.org " + time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
	args.ChannelId = "channel"
	api.teamPermissions["requester"][model.PermissionAddUserToTeam] = false
	response, appErr = p.googleGroupTeamCommand(args)
	if appErr != nil || !strings.Contains(response.Text, "permission to add members") {
		t.Fatalf("team sync without add permission: response=%+v err=%v", response, appErr)
	}

	args.Command = "/google group team sync start"
	response, appErr = p.googleGroupTeamCommand(args)
	summaryIndex := strings.Index(response.Text, "Usage: `/google group team sync start|stop|list`")
	detailIndex := strings.Index(response.Text, "Usage: `/google group team sync start <group-email>")
	if appErr != nil || summaryIndex < 0 || detailIndex <= summaryIndex {
		t.Fatalf("team sync usage should show available subcommands before details: response=%+v err=%v", response, appErr)
	}
}

func TestGoogleGroupTeamSyncStartAndPairedChannelSync(t *testing.T) {
	key := setupGoogleTestTransport(t, func(request *http.Request) (int, string) {
		if request.URL.Host == "oauth2.googleapis.com" {
			return http.StatusOK, `{"access_token":"token"}`
		}
		if strings.HasSuffix(request.URL.Path, "/members") {
			return http.StatusOK, `{"members":[{"email":"member@example.org","type":"USER"}]}`
		}
		groupEmail := strings.TrimPrefix(request.URL.Path, "/admin/directory/v1/groups/")
		return http.StatusOK, fmt.Sprintf(`{"id":"id","email":%q,"name":"Example Group"}`, groupEmail)
	}, func(request *http.Request) (int, string) {
		if request.URL.Path == "/v1/groups:lookup" {
			return http.StatusOK, `{"name":"groups/id"}`
		}
		return http.StatusOK, `{"memberships":[]}`
	})
	api := &googleGroupSyncTestAPI{
		categoryTestAPI: &categoryTestAPI{
			kv:      map[string][]byte{},
			user:    &model.User{Id: "requester", Username: "requester", Email: "requester@example.org"},
			channel: model.Channel{Id: "channel", Name: "general", TeamId: "team", Type: model.ChannelTypeOpen},
		},
		teamAdmins: map[string]bool{"requester": true},
		teamPermissions: map[string]map[*model.Permission]bool{
			"requester": {model.PermissionAddUserToTeam: true},
		},
		users: map[string]*model.User{
			"member@example.org": {Id: "member", Email: "member@example.org"},
		},
		teamMembers:    map[string]*model.TeamMember{},
		channelMembers: map[string]*model.ChannelMember{"requester": {ChannelId: "channel", UserId: "requester"}},
	}
	p := &Plugin{configuration: &configuration{
		GoogleGroupsSyncDomain: "example.org", GoogleGroupsSyncServiceAccountKey: key,
		GoogleGroupChannelTeamAdminThreshold: 30, GoogleGroupChannelSystemAdminThreshold: 100,
	}}
	p.SetAPI(api)
	deadline := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
	args := &model.CommandArgs{
		Command: "/google group team sync start group@example.org " + deadline,
		UserId:  "requester", TeamId: "team", ChannelId: "channel", TriggerId: "trigger",
	}
	response, appErr := p.googleGroupTeamCommand(args)
	if appErr != nil || !strings.Contains(response.Text, "confirm") {
		t.Fatalf("team sync preview: response=%+v err=%v", response, appErr)
	}
	var plan googleGroupConfirmation
	if err := json.Unmarshal(api.kv[googleGroupConfirmationPrefix+api.dialog.Dialog.State], &plan); err != nil {
		t.Fatal(err)
	}
	if !plan.TargetTeam {
		t.Fatal("team sync plan did not target the team")
	}
	if message, err := p.applyGoogleGroupConfirmation(plan); err != nil || !strings.Contains(message, "Added 1 team members") {
		t.Fatalf("start team sync: message=%q err=%v", message, err)
	}
	teamKey := googleGroupSyncKey("team", "", "group@example.org")
	var teamJob googleGroupSync
	if err := json.Unmarshal(api.kv[teamKey], &teamJob); err != nil || teamJob.ChannelID != "" || teamJob.LastSyncedAt == 0 {
		t.Fatalf("team sync record = %+v, err=%v", teamJob, err)
	}

	args.Command = "/google group sync start paired@example.org " + deadline + " --also-invite-to-team"
	response, appErr = p.googleGroupSyncCommand(args)
	if appErr != nil || !strings.Contains(response.Text, "confirm") {
		t.Fatalf("paired sync preview: response=%+v err=%v", response, appErr)
	}
	plan = googleGroupConfirmation{}
	if err := json.Unmarshal(api.kv[googleGroupConfirmationPrefix+api.dialog.Dialog.State], &plan); err != nil {
		t.Fatal(err)
	}
	if !plan.AlsoInviteToTeam || plan.TargetTeam {
		t.Fatalf("paired sync plan has incorrect targets: %+v", plan)
	}
	if message, err := p.applyGoogleGroupConfirmation(plan); err != nil ||
		!strings.Contains(message, "Added 1 channel members") ||
		!strings.Contains(message, "Added 0 team members") {
		t.Fatalf("start paired sync: message=%q err=%v", message, err)
	}
	if len(api.teamAdded) != 1 || !reflect.DeepEqual(api.added, []string{"member"}) {
		t.Fatalf("initial team sync must precede channel sync: teamAdded=%v channelAdded=%v", api.teamAdded, api.added)
	}
	if len(api.kv[googleGroupSyncKey("team", "", "paired@example.org")]) == 0 ||
		len(api.kv[googleGroupSyncKey("team", "channel", "paired@example.org")]) == 0 {
		t.Fatal("paired start did not persist both sync targets")
	}

	list, appErr := p.googleGroupSyncCommand(&model.CommandArgs{
		Command: "/google group sync list", UserId: "requester", TeamId: "team", ChannelId: "channel",
	})
	if appErr != nil || !strings.Contains(list.Text, "**Team sync:") || !strings.Contains(list.Text, "**Channel sync:") ||
		!strings.Contains(list.Text, "Started by: @requester") || strings.Contains(list.Text, "creator's Google account") {
		t.Fatalf("sync list omitted one target: response=%+v err=%v", list, appErr)
	}

	for _, key := range []string{
		teamKey,
		googleGroupSyncKey("team", "", "paired@example.org"),
		googleGroupSyncKey("team", "channel", "paired@example.org"),
	} {
		var job googleGroupSync
		if err := json.Unmarshal(api.kv[key], &job); err != nil {
			t.Fatal(err)
		}
		job.LastSyncedAt = 0
		api.kv[key], _ = json.Marshal(job)
	}
	delete(api.teamMembers, "member")
	delete(api.channelMembers, "member")
	api.teamAdded = nil
	api.added = nil
	p.runGoogleGroupSyncJobs(context.Background())
	if len(api.teamAdded) == 0 || !reflect.DeepEqual(api.added, []string{"member"}) {
		t.Fatalf("scheduled team invites must precede channel syncs: teamAdded=%v channelAdded=%v", api.teamAdded, api.added)
	}
}

func TestGoogleGroupSyncFindsAlternateMattermostAddress(t *testing.T) {
	api := &googleGroupSyncTestAPI{
		categoryTestAPI: &categoryTestAPI{kv: map[string][]byte{}},
		users: map[string]*model.User{
			"member@alternate.example": {Id: "member", Email: "member@alternate.example"},
		},
		teamMembers: map[string]*model.TeamMember{
			"member": {TeamId: "team", UserId: "member"},
		},
		channelMembers: map[string]*model.ChannelMember{},
	}
	p := &Plugin{configuration: &configuration{GoogleGroupsSyncDomain: "example.org", GoogleGroupsSyncAlternateDomain: "alternate.example"}}
	p.SetAPI(api)
	snapshot := &googleDirectoryGroupSnapshot{Members: []googleDirectoryGroupMember{
		{Email: "member@example.org", Type: "USER"},
	}}

	added, err := p.syncGoogleGroupMembers(snapshot, googleGroupSync{TeamID: "team", ChannelID: "channel"})
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 || !reflect.DeepEqual(api.added, []string{"member"}) {
		t.Fatalf("added count/users = %d/%v; want 1/[member]", added, api.added)
	}
	removed, err := p.removeGoogleGroupChannelMembers(snapshot, "channel", "requester")
	if err != nil || removed != 1 || !reflect.DeepEqual(api.removed, []string{"member"}) {
		t.Fatalf("removed count/users = %d/%v, err=%v; want 1/[member]", removed, api.removed, err)
	}
	api.users["member@example.org"] = &model.User{Id: "exact"}
	if user, appErr := p.googleGroupMattermostUser("member@example.org"); appErr != nil || user == nil || user.Id != "exact" {
		t.Fatalf("exact address should take precedence: user=%+v err=%v", user, appErr)
	}
	delete(api.users, "member@example.org")
	p.configuration.GoogleGroupsSyncAlternateDomain = ""
	added, err = p.syncGoogleGroupMembers(snapshot, googleGroupSync{TeamID: "team", ChannelID: "channel"})
	if err != nil || added != 0 {
		t.Fatalf("cleared alternate domain matched a user: added=%d err=%v", added, err)
	}
}

func TestGoogleGroupSizePermissionBoundary(t *testing.T) {
	p := &Plugin{configuration: &configuration{GoogleGroupChannelTeamAdminThreshold: 30, GoogleGroupChannelSystemAdminThreshold: 100}}
	api := &googleGroupSyncTestAPI{categoryTestAPI: &categoryTestAPI{adminTeam: "team"}, systemAdmins: map[string]bool{"system": true}}
	p.SetAPI(api)
	if got := p.googleGroupChannelPermissionMessage("member", "team", 30); got != "" {
		t.Fatalf("30-member group should not require admin: %q", got)
	}
	if got := p.googleGroupChannelPermissionMessage("member", "team", 31); got == "" {
		t.Fatal("31-member group should require team admin")
	}
	if got := p.googleGroupChannelPermissionMessage("admin", "team", 51); got != "" {
		t.Fatalf("team admin should manage large group: %q", got)
	}
	if got := p.googleGroupChannelPermissionMessage("system", "team", 51); got != "" {
		t.Fatalf("system admin should manage large group: %q", got)
	}
}

func TestGoogleGroupThresholdConfiguration(t *testing.T) {
	p, api := newDefaultChannelTestPlugin(t)
	api.settings["GoogleGroupsSyncLargeThresholdTeamAdmin"] = 2
	api.settings["GoogleGroupsSyncLargeThresholdSystemAdmin"] = 4
	if err := p.OnConfigurationChange(); err != nil {
		t.Fatal(err)
	}
	if cfg := p.getConfiguration(); cfg.GoogleGroupChannelTeamAdminThreshold != 30 || cfg.GoogleGroupChannelSystemAdminThreshold != 100 {
		t.Fatalf("unexpected default thresholds: %+v", cfg)
	}
	api.settings["GoogleGroupChannelTeamAdminThreshold"] = 2
	api.settings["GoogleGroupChannelSystemAdminThreshold"] = 4
	if err := p.OnConfigurationChange(); err != nil {
		t.Fatal(err)
	}
	api.settings["GoogleGroupChannelTeamAdminThreshold"] = 5
	if err := p.OnConfigurationChange(); err == nil {
		t.Fatal("accepted system admin threshold below team admin threshold")
	}
	if p.getConfiguration().GoogleGroupChannelTeamAdminThreshold != 2 {
		t.Fatal("invalid configuration replaced active configuration")
	}
	api.settings["GoogleGroupChannelTeamAdminThreshold"] = -1
	if err := p.OnConfigurationChange(); err == nil {
		t.Fatal("accepted negative threshold")
	}
	api.settings["GoogleGroupChannelTeamAdminThreshold"] = 0
	if err := p.OnConfigurationChange(); err != nil {
		t.Fatal(err)
	}
	if p.getConfiguration().GoogleGroupChannelTeamAdminThreshold != 0 {
		t.Fatal("configured zero threshold was not preserved")
	}
	api.settings["GoogleGroupChannelTeamAdminThreshold"] = 2
	if err := p.OnConfigurationChange(); err != nil {
		t.Fatal(err)
	}
	p.SetAPI(&googleGroupSyncTestAPI{categoryTestAPI: &categoryTestAPI{adminTeam: "team"}, systemAdmins: map[string]bool{"system": true}})
	for _, tc := range []struct {
		user    string
		count   int
		wantErr string
	}{
		{"member", 2, ""},
		{"member", 3, "more than 2"},
		{"admin", 4, ""},
		{"admin", 5, "more than 4"},
		{"system", 5, ""},
	} {
		got := p.googleGroupChannelPermissionMessage(tc.user, "team", tc.count)
		if (tc.wantErr == "" && got != "") || (tc.wantErr != "" && !strings.Contains(got, tc.wantErr)) {
			t.Errorf("%s, %d members: got %q; want %q", tc.user, tc.count, got, tc.wantErr)
		}
	}
}

type googleGroupSyncTestAPI struct {
	*categoryTestAPI
	users           map[string]*model.User
	teamMembers     map[string]*model.TeamMember
	teams           map[string]*model.Team
	channelMembers  map[string]*model.ChannelMember
	added           []string
	removed         []string
	teamAdded       []string
	teamRemoved     []string
	teamAdmins      map[string]bool
	teamPermissions map[string]map[*model.Permission]bool
	systemAdmins    map[string]bool
	failRemove      string
	failTeamRemove  string
	usersByID       map[string]*model.User
	usersByName     map[string]*model.User
	dialog          model.OpenDialogRequest
}

func (a *googleGroupSyncTestAPI) GetUser(userID string) (*model.User, *model.AppError) {
	if user, ok := a.usersByID[userID]; ok {
		return user, nil
	}
	if a.user != nil && a.user.Id == userID {
		return a.categoryTestAPI.GetUser(userID)
	}
	return nil, testAppError(http.StatusNotFound)
}

func (a *googleGroupSyncTestAPI) GetUserByUsername(username string) (*model.User, *model.AppError) {
	if user, ok := a.usersByName[username]; ok {
		return user, nil
	}
	return nil, testAppError(http.StatusNotFound)
}

func (a *googleGroupSyncTestAPI) HasPermissionTo(userID string, permission *model.Permission) bool {
	return permission == model.PermissionManageSystem && a.systemAdmins[userID]
}

func (a *googleGroupSyncTestAPI) HasPermissionToTeam(userID, teamID string, permission *model.Permission) bool {
	return (teamID == "team" && permission == model.PermissionManageTeam && a.teamAdmins[userID]) ||
		a.teamPermissions[userID][permission] ||
		a.categoryTestAPI.HasPermissionToTeam(userID, teamID, permission)
}

func (a *googleGroupSyncTestAPI) GetTeam(teamID string) (*model.Team, *model.AppError) {
	if team := a.teams[teamID]; team != nil {
		return team, nil
	}
	if teamID == "team" {
		return &model.Team{Id: teamID, Name: "physics", DisplayName: "Physics"}, nil
	}
	return nil, testAppError(http.StatusNotFound)
}

func (a *googleGroupSyncTestAPI) GetUserByEmail(email string) (*model.User, *model.AppError) {
	user := a.users[email]
	if user == nil {
		return nil, testAppError(http.StatusNotFound)
	}
	return user, nil
}

func (a *googleGroupSyncTestAPI) GetTeamMember(_, userID string) (*model.TeamMember, *model.AppError) {
	member := a.teamMembers[userID]
	if member == nil {
		return nil, testAppError(http.StatusNotFound)
	}
	return member, nil
}

func (a *googleGroupSyncTestAPI) CreateTeamMember(teamID, userID string) (*model.TeamMember, *model.AppError) {
	if a.teamMembers == nil {
		a.teamMembers = make(map[string]*model.TeamMember)
	}
	a.teamAdded = append(a.teamAdded, userID)
	member := &model.TeamMember{TeamId: teamID, UserId: userID}
	a.teamMembers[userID] = member
	return member, nil
}

func (a *googleGroupSyncTestAPI) DeleteTeamMember(teamID, userID, _ string) *model.AppError {
	if userID == a.failTeamRemove {
		return testAppError(http.StatusInternalServerError)
	}
	a.teamRemoved = append(a.teamRemoved, userID)
	delete(a.teamMembers, userID)
	return nil
}

func (a *googleGroupSyncTestAPI) GetChannelMember(channelID, userID string) (*model.ChannelMember, *model.AppError) {
	member := a.channelMembers[userID]
	if member == nil || member.ChannelId != channelID {
		return nil, testAppError(http.StatusNotFound)
	}
	return member, nil
}

func (a *googleGroupSyncTestAPI) AddChannelMember(channelID, userID string) (*model.ChannelMember, *model.AppError) {
	a.added = append(a.added, userID)
	member := &model.ChannelMember{ChannelId: channelID, UserId: userID}
	a.channelMembers[userID] = member
	return member, nil
}

func (a *googleGroupSyncTestAPI) DeleteChannelMember(channelID, userID string) *model.AppError {
	if userID == a.failRemove {
		return testAppError(http.StatusInternalServerError)
	}
	a.removed = append(a.removed, userID)
	delete(a.channelMembers, userID)
	return nil
}

func (a *googleGroupSyncTestAPI) KVSetWithExpiry(key string, value []byte, _ int64) *model.AppError {
	return a.KVSet(key, value)
}

func (a *googleGroupSyncTestAPI) OpenInteractiveDialog(dialog model.OpenDialogRequest) *model.AppError {
	a.dialog = dialog
	return nil
}

type googleTestTransport func(*http.Request) (*http.Response, error)

func (transport googleTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func setupGoogleTestTransport(t *testing.T, handler func(*http.Request) (int, string), cloudHandlers ...func(*http.Request) (int, string)) string {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	key := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	account, err := json.Marshal(googleServiceAccount{ClientEmail: "service@example.com", PrivateKey: string(key), TokenURI: "https://oauth2.googleapis.com/token"})
	if err != nil {
		t.Fatal(err)
	}
	previous := http.DefaultTransport
	http.DefaultTransport = googleTestTransport(func(request *http.Request) (*http.Response, error) {
		var status int
		var body string
		if request.URL.Host == "cloudidentity.googleapis.com" && len(cloudHandlers) > 0 {
			status, body = cloudHandlers[0](request)
		} else if request.URL.Host == "cloudidentity.googleapis.com" {
			switch request.URL.Path {
			case "/v1/groups:lookup":
				status, body = http.StatusOK, `{"name":"groups/id"}`
			case "/v1/groups/id":
				status, body = http.StatusOK, `{"name":"groups/id","groupKey":{"id":"team@example.org"},"displayName":"Team"}`
			case "/v1/groups/id/memberships":
				status, body = http.StatusOK, `{"memberships":[]}`
			default:
				t.Errorf("unexpected Cloud Identity request: %s", request.URL)
				status, body = http.StatusNotFound, `{}`
			}
		} else {
			status, body = handler(request)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: request}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	return string(account)
}

func TestGoogleGroupAuthorizationErrorMessages(t *testing.T) {
	tests := []struct {
		name        string
		tokenStatus int
		tokenBody   string
		apiStatus   int
		apiBody     string
		want        string
		notWant     string
	}{
		{"unknown delegated account", http.StatusBadRequest, `{"error":"invalid_grant","error_description":"Invalid email or User ID"}`, 0, "", "Google could not find the account", "invalid_grant"},
		{"unauthorized client", http.StatusUnauthorized, `{"error":"unauthorized_client","error_description":"not authorized"}`, 0, "", "domain-wide delegation", "unauthorized_client"},
		{"group permission denied", http.StatusOK, `{"access_token":"token"}`, http.StatusForbidden, `{"error":{"errors":[{"reason":"forbidden"}]}}`, "may not have permission", `"reason"`},
		{"other invalid grant", http.StatusBadRequest, `{"error":"invalid_grant","error_description":"Invalid JWT Signature."}`, 0, "", "Invalid JWT Signature.", ""},
		{"quota failure", http.StatusOK, `{"access_token":"token"}`, http.StatusForbidden, `{"error":{"errors":[{"reason":"quotaExceeded"}]}}`, "quotaExceeded", ""},
		{"server failure", http.StatusOK, `{"access_token":"token"}`, http.StatusInternalServerError, `{"error":"unavailable"}`, "HTTP 500", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := setupGoogleTestTransport(t, func(request *http.Request) (int, string) {
				if request.URL.Host == "oauth2.googleapis.com" {
					return tt.tokenStatus, tt.tokenBody
				}
				return tt.apiStatus, tt.apiBody
			}, func(request *http.Request) (int, string) {
				return http.StatusForbidden, `{"error":{"errors":[{"reason":"forbidden"}]}}`
			})
			api := &googleGroupSyncTestAPI{
				categoryTestAPI: &categoryTestAPI{
					user:    &model.User{Id: "caller", Email: "caller@example.org"},
					channel: model.Channel{Id: "channel", TeamId: "team", Type: model.ChannelTypeOpen},
				},
				channelMembers: map[string]*model.ChannelMember{"caller": {ChannelId: "channel", UserId: "caller"}},
			}
			p := &Plugin{configuration: &configuration{GoogleGroupsSyncDomain: "example.org", GoogleGroupsSyncServiceAccountKey: key}}
			p.SetAPI(api)
			response, appErr := p.ExecuteCommand(nil, &model.CommandArgs{
				Command: "/google group add team@example.org", UserId: "caller", TeamId: "team", ChannelId: "channel",
			})
			if appErr != nil || response == nil || !strings.Contains(response.Text, tt.want) ||
				(tt.notWant != "" && strings.Contains(response.Text, tt.notWant)) {
				t.Fatalf("unexpected response: %+v, %v", response, appErr)
			}
		})
	}
}

func TestGoogleGroupDerivedMembershipFallback(t *testing.T) {
	for _, tc := range []struct {
		name        string
		groupStatus int
		direct      int
		derived     int
		admin       int
		noAdmin     bool
		wantAdmin   bool
		wantError   bool
	}{
		{"derived forbidden", 200, 200, 403, 200, false, true, false},
		{"direct forbidden", 200, 403, 403, 200, false, false, true},
		{"group forbidden", 403, 200, 403, 200, false, true, false},
		{"derived server error", 200, 200, 500, 200, false, false, true},
		{"admin forbidden", 200, 200, 403, 403, false, true, true},
		{"admin not configured", 200, 200, 403, 200, true, false, true},
		{"derived allowed", 200, 200, 200, 200, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adminRequests := 0
			cloudTokens := 0
			key := setupGoogleTestTransport(t, func(request *http.Request) (int, string) {
				if request.URL.Host == "oauth2.googleapis.com" {
					if err := request.ParseForm(); err != nil {
						t.Fatal(err)
					}
					parts := strings.Split(request.Form.Get("assertion"), ".")
					claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
					if err != nil {
						t.Fatal(err)
					}
					var claims struct {
						Subject string `json:"sub"`
						Scope   string `json:"scope"`
					}
					if err := json.Unmarshal(claimsBytes, &claims); err != nil {
						t.Fatal(err)
					}
					if claims.Scope == googleCloudIdentityReadScope {
						cloudTokens++
						if claims.Subject != "requester@example.org" {
							t.Errorf("Cloud Identity token impersonated %q", claims.Subject)
						}
					} else if !strings.Contains(claims.Scope, "admin.directory.group.readonly") {
						t.Errorf("unexpected token scopes: %s", claims.Scope)
					}
					return http.StatusOK, fmt.Sprintf(`{"access_token":%q}`, claims.Subject)
				}
				admin := request.Header.Get("Authorization") == "Bearer admin@example.org"
				if admin {
					adminRequests++
				}
				if !strings.HasSuffix(request.URL.Path, "/members") {
					if !admin && tc.groupStatus != 200 {
						return tc.groupStatus, `{"error":{"errors":[{"reason":"forbidden"}]}}`
					}
					return 200, `{"id":"id","email":"team@example.org"}`
				}
				status := tc.direct
				if admin {
					status = tc.admin
				} else if request.URL.Query().Get("includeDerivedMembership") == "true" {
					status = tc.derived
				}
				if status != 200 {
					return status, `{"error":{"errors":[{"reason":"forbidden"}]}}`
				}
				return 200, `{"members":[{"email":"member@example.org","type":"USER"}]}`
			}, func(request *http.Request) (int, string) {
				if request.Header.Get("Authorization") != "Bearer requester@example.org" {
					t.Errorf("incorrect Cloud Identity delegate: %s", request.Header.Get("Authorization"))
				}
				if request.URL.Path == "/v1/groups:lookup" {
					if request.URL.Query().Get("groupKey.id") != "team@example.org" {
						t.Errorf("incorrect group key: %s", request.URL.RawQuery)
					}
					return 200, `{"name":"groups/id"}`
				}
				if request.URL.Path != "/v1/groups/id/memberships" || request.URL.Query().Get("pageSize") != "1" {
					t.Errorf("unexpected Cloud Identity request: %s", request.URL)
				}
				if tc.direct != 200 {
					return tc.direct, `{"error":{"status":"PERMISSION_DENIED"}}`
				}
				return 200, `{"memberships":[]}`
			})
			adminEmail := "admin@example.org"
			if tc.noAdmin {
				adminEmail = ""
			}
			p := &Plugin{configuration: &configuration{GoogleGroupsSyncDomain: "example.org", GoogleGroupsSyncAdminEmail: adminEmail, GoogleGroupsSyncServiceAccountKey: key}}
			snapshot, actor, err := p.googleGroupChannelSnapshot("team@example.org", "requester@example.org")
			if (err != nil) != tc.wantError || (adminRequests > 0) != tc.wantAdmin {
				t.Fatalf("snapshot=%+v actor=%s error=%v admin requests=%d", snapshot, actor, err, adminRequests)
			}
			if tc.direct == http.StatusForbidden && (err == nil || !strings.Contains(googleUserFacingError(err), "Cloud Identity")) {
				t.Fatalf("direct-access failure did not identify Cloud Identity: %v", err)
			}
			if tc.name == "admin forbidden" && (err == nil || !strings.Contains(googleUserFacingError(err), "configured admin account")) {
				t.Fatalf("admin failure did not identify the delegated account: %v", err)
			}
			if err == nil && (snapshot == nil || len(snapshot.Members) != 1 || (actor == "admin@example.org") != tc.wantAdmin) {
				t.Fatalf("unexpected snapshot or actor: %+v %s", snapshot, actor)
			}
			if (tc.wantAdmin || tc.direct != 200 || tc.noAdmin) && cloudTokens != 2 {
				t.Fatalf("expected a Cloud Identity token for each access request, got %d", cloudTokens)
			}
		})
	}
}

func TestGoogleGroupDirectAccessCloudIdentityErrors(t *testing.T) {
	for _, tc := range []struct {
		name        string
		lookupBody  string
		lookupCode  int
		membersBody string
		membersCode int
		wantError   bool
	}{
		{"empty group", `{"name":"groups/id"}`, 200, `{}`, 200, false},
		{"lookup denied", `{"error":{"status":"PERMISSION_DENIED"}}`, 403, `{}`, 200, true},
		{"invalid group name", `{"name":"groups/id/memberships"}`, 200, `{}`, 200, true},
		{"members unavailable", `{"name":"groups/id"}`, 200, `{}`, 500, true},
		{"null response", `{"name":"groups/id"}`, 200, `null`, 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := setupGoogleTestTransport(t, func(request *http.Request) (int, string) {
				if request.URL.Host != "oauth2.googleapis.com" {
					t.Errorf("unexpected request: %s", request.URL)
				}
				return 200, `{"access_token":"token"}`
			}, func(request *http.Request) (int, string) {
				if request.URL.Path == "/v1/groups:lookup" {
					return tc.lookupCode, tc.lookupBody
				}
				return tc.membersCode, tc.membersBody
			})
			p := &Plugin{configuration: &configuration{GoogleGroupsSyncServiceAccountKey: key}}
			err := p.verifyGoogleGroupDirectAccess("team@example.org", "requester@example.org")
			if (err != nil) != tc.wantError {
				t.Fatalf("direct access error = %v", err)
			}
		})
	}
}

func TestGoogleGroupFallbackSyncRechecksCreator(t *testing.T) {
	directAllowed := true
	key := setupGoogleTestTransport(t, func(request *http.Request) (int, string) {
		if request.URL.Host == "oauth2.googleapis.com" {
			if err := request.ParseForm(); err != nil {
				t.Fatal(err)
			}
			parts := strings.Split(request.Form.Get("assertion"), ".")
			claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
			if err != nil {
				t.Fatal(err)
			}
			var claims struct {
				Subject string `json:"sub"`
			}
			if err := json.Unmarshal(claimsBytes, &claims); err != nil {
				t.Fatal(err)
			}
			return 200, fmt.Sprintf(`{"access_token":%q}`, claims.Subject)
		}
		if !strings.HasSuffix(request.URL.Path, "/members") {
			return 200, `{"id":"id","email":"team@example.org","name":"Team"}`
		}
		if request.Header.Get("Authorization") == "Bearer requester@example.org" {
			if !directAllowed || request.URL.Query().Get("includeDerivedMembership") == "true" {
				return 403, `{"error":{"errors":[{"reason":"forbidden"}]}}`
			}
			return 200, `{"members":[{"email":"subgroup@example.org","type":"GROUP"}]}`
		}
		return 200, `{"members":[{"email":"indirect@example.org","type":"USER"}]}`
	}, func(request *http.Request) (int, string) {
		if request.URL.Path == "/v1/groups:lookup" {
			return 200, `{"name":"groups/id"}`
		}
		if request.Header.Get("Authorization") == "Bearer requester@example.org" && !directAllowed {
			return 403, `{"error":{"status":"PERMISSION_DENIED"}}`
		}
		return 200, `{"memberships":[]}`
	})
	base := &categoryTestAPI{
		kv: map[string][]byte{}, user: &model.User{Id: "requester", Username: "requester", Email: "requester@example.org"},
		channel: model.Channel{Id: "channel", Name: "target", TeamId: "team", Type: model.ChannelTypeOpen},
	}
	api := &googleGroupSyncTestAPI{
		categoryTestAPI: base, teamAdmins: map[string]bool{"requester": true},
		users:          map[string]*model.User{"indirect@example.org": {Id: "indirect"}},
		teamMembers:    map[string]*model.TeamMember{"indirect": {UserId: "indirect", TeamId: "team"}},
		channelMembers: map[string]*model.ChannelMember{"requester": {ChannelId: "channel", UserId: "requester"}},
	}
	p := &Plugin{configuration: &configuration{
		GoogleGroupsSyncDomain: "example.org", GoogleGroupsSyncAdminEmail: "admin@example.org", GoogleGroupsSyncServiceAccountKey: key,
		GoogleGroupChannelTeamAdminThreshold: 30, GoogleGroupChannelSystemAdminThreshold: 100,
	}}
	p.SetAPI(api)
	channelArgs := &model.CommandArgs{
		Command: "/google group add team@example.org",
		UserId:  "requester", TeamId: "team", ChannelId: "channel", TriggerId: "trigger",
	}
	channelResponse, appErr := p.googleGroupChannelCommand(channelArgs)
	if appErr != nil || !strings.Contains(channelResponse.Text, "confirm") ||
		!strings.Contains(api.dialog.Dialog.IntroductionText, "requester@example.org") ||
		strings.Contains(api.dialog.Dialog.IntroductionText, "admin@example.org") {
		t.Fatalf("channel preview: %+v %v", channelResponse, appErr)
	}
	var channelPlan googleGroupConfirmation
	if err := json.Unmarshal(api.kv[googleGroupConfirmationPrefix+api.dialog.Dialog.State], &channelPlan); err != nil {
		t.Fatal(err)
	}
	if _, err := p.applyGoogleGroupConfirmation(channelPlan); err != nil || !reflect.DeepEqual(api.added, []string{"indirect"}) {
		t.Fatalf("channel add: added=%v error=%v", api.added, err)
	}
	api.added = nil
	delete(api.channelMembers, "indirect")
	args := &model.CommandArgs{
		Command: "/google group sync start team@example.org " + time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02"),
		UserId:  "requester", TeamId: "team", ChannelId: "channel", TriggerId: "trigger",
	}
	response, appErr := p.googleGroupSyncCommand(args)
	if appErr != nil || !strings.Contains(response.Text, "confirm") ||
		!strings.Contains(api.dialog.Dialog.IntroductionText, "requester@example.org") ||
		strings.Contains(api.dialog.Dialog.IntroductionText, "admin@example.org") {
		t.Fatalf("preview: %+v %v", response, appErr)
	}
	var plan googleGroupConfirmation
	if err := json.Unmarshal(api.kv[googleGroupConfirmationPrefix+api.dialog.Dialog.State], &plan); err != nil {
		t.Fatal(err)
	}
	directAllowed = false
	if _, err := p.applyGoogleGroupConfirmation(plan); err == nil || len(api.added) != 0 {
		t.Fatalf("confirmation accepted revoked access: %v", err)
	}
	directAllowed = true
	if _, err := p.applyGoogleGroupConfirmation(plan); err != nil {
		t.Fatal(err)
	}
	recordKey := googleGroupSyncKey("team", "channel", "team@example.org")
	var job googleGroupSync
	if err := json.Unmarshal(api.kv[recordKey], &job); err != nil {
		t.Fatal(err)
	}
	if job.CreatedBy != "requester" || job.RequesterEmail != "requester@example.org" || job.ActingEmail != "admin@example.org" || len(api.added) != 1 {
		t.Fatalf("unexpected sync: %+v added=%v", job, api.added)
	}
	list, appErr := p.googleGroupSyncCommand(&model.CommandArgs{Command: "/google group sync list", UserId: "requester", TeamId: "team", ChannelId: "channel"})
	if appErr != nil || !strings.Contains(list.Text, "Started by: @requester") ||
		!strings.Contains(list.Text, "requester@example.org") || strings.Contains(list.Text, "admin@example.org") {
		t.Fatalf("sync list: %+v %v", list, appErr)
	}
	job.LastSyncedAt = 0
	api.kv[recordKey], _ = json.Marshal(job)
	api.added = nil
	delete(api.channelMembers, "indirect")
	directAllowed = false
	p.runGoogleGroupSyncJobs(context.Background())
	if len(api.added) != 0 {
		t.Fatalf("scheduled sync ran after access was revoked: %v", api.added)
	}
	directAllowed = true
	p.runGoogleGroupSyncJobs(context.Background())
	if len(api.added) != 1 {
		t.Fatalf("scheduled sync did not run with restored access: %v", api.added)
	}
	job.LastSyncedAt = 0
	api.kv[recordKey], _ = json.Marshal(job)
	api.added = nil
	delete(api.channelMembers, "indirect")
	cfg := *p.getConfiguration()
	cfg.GoogleGroupsSyncAdminEmail = "other-admin@example.org"
	p.setConfiguration(&cfg)
	p.runGoogleGroupSyncJobs(context.Background())
	if len(api.added) != 0 {
		t.Fatalf("scheduled sync accepted a changed admin account: %v", api.added)
	}
	cfg2 := *p.getConfiguration()
	cfg2.GoogleGroupsSyncAdminEmail = "admin@example.org"
	cfg2.GoogleGroupsSyncAdminUsers = []string{"requester"}
	p.setConfiguration(&cfg2)
	job.DelegatedEmail = "admin@example.org"
	job.AdminFallback = false
	api.kv[recordKey], _ = json.Marshal(job)
	directAllowed = false
	p.runGoogleGroupSyncJobs(context.Background())
	if len(api.added) != 0 {
		t.Fatalf("admin delegation bypassed the creator's direct access check: %v", api.added)
	}
	delete(api.kv, recordKey)
	response, appErr = p.googleGroupSyncCommand(args)
	if appErr != nil || !strings.Contains(response.Text, "confirm") {
		t.Fatalf("admin-delegated sync preview: %+v %v", response, appErr)
	}
	if err := json.Unmarshal(api.kv[googleGroupConfirmationPrefix+api.dialog.Dialog.State], &plan); err != nil {
		t.Fatal(err)
	}
	if _, err := p.applyGoogleGroupConfirmation(plan); err == nil || len(api.kv[recordKey]) != 0 {
		t.Fatalf("admin delegation allowed sync without creator access: %v", err)
	}
}

func TestGoogleGroupConfirmationAuthorizationError(t *testing.T) {
	denied := false
	key := setupGoogleTestTransport(t, func(request *http.Request) (int, string) {
		if request.URL.Host == "oauth2.googleapis.com" {
			if denied {
				return http.StatusBadRequest, `{"error":"invalid_grant","error_description":"Invalid email or User ID"}`
			}
			return http.StatusOK, `{"access_token":"token"}`
		}
		if strings.HasSuffix(request.URL.Path, "/members") {
			return http.StatusOK, `{"members":[]}`
		}
		return http.StatusOK, `{"id":"group-id","email":"team@example.org"}`
	})
	api := &googleGroupSyncTestAPI{
		categoryTestAPI: &categoryTestAPI{
			kv:      map[string][]byte{},
			user:    &model.User{Id: "caller", Email: "caller@example.org"},
			channel: model.Channel{Id: "channel", TeamId: "team", Type: model.ChannelTypeOpen},
		},
		channelMembers: map[string]*model.ChannelMember{"caller": {ChannelId: "channel", UserId: "caller"}},
	}
	p := &Plugin{configuration: &configuration{GoogleGroupsSyncDomain: "example.org", GoogleGroupsSyncServiceAccountKey: key}}
	p.SetAPI(api)
	response, appErr := p.ExecuteCommand(nil, &model.CommandArgs{
		Command: "/google group add team@example.org", UserId: "caller", TeamId: "team", ChannelId: "channel", TriggerId: "trigger",
	})
	if appErr != nil || response == nil || !strings.Contains(response.Text, "confirm") {
		t.Fatalf("preview: %+v, %v", response, appErr)
	}
	submission, err := json.Marshal(model.SubmitDialogRequest{
		CallbackId: googleGroupConfirmationID, State: api.dialog.Dialog.State, UserId: "caller", TeamId: "team", ChannelId: "channel",
		Submission: map[string]any{"confirm": "ok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	denied = true
	request := httptest.NewRequest(http.MethodPost, "/google-groups/confirm", bytes.NewReader(submission))
	request.Header.Set("Mattermost-User-Id", "caller")
	recorder := httptest.NewRecorder()
	p.serveGoogleGroupConfirmation(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Google could not find the account") ||
		strings.Contains(recorder.Body.String(), "invalid_grant") {
		t.Fatalf("unexpected confirmation response: %d %s", recorder.Code, recorder.Body.String())
	}
}

func assertGoogleGroupSyncPreviewOmitsCreatorMetadata(t *testing.T, introduction string) {
	t.Helper()
	for _, unwanted := range []string{"**Sync:**", "Mattermost user ID", "created by"} {
		if strings.Contains(introduction, unwanted) {
			t.Fatalf("sync preview contains creator metadata %q: %s", unwanted, introduction)
		}
	}
}

func TestGoogleSyncNowPreviewAndConfirmation(t *testing.T) {
	var memberRequests int
	creatorCanRead := true
	key := setupGoogleTestTransport(t, func(request *http.Request) (int, string) {
		if request.URL.Host == "oauth2.googleapis.com" {
			subject := ""
			if err := request.ParseForm(); err != nil {
				t.Error(err)
			} else {
				parts := strings.Split(request.Form.Get("assertion"), ".")
				if len(parts) != 3 {
					t.Error("invalid token assertion")
				} else {
					claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
					var claims struct {
						Subject string `json:"sub"`
						Scope   string `json:"scope"`
					}
					if err != nil || json.Unmarshal(claimsBytes, &claims) != nil ||
						(claims.Subject != "requester@fysiksektionen.se" && claims.Subject != "creator@fysiksektionen.se") ||
						(claims.Scope != googleCloudIdentityReadScope &&
							(!strings.Contains(claims.Scope, "admin.directory.group.readonly") ||
								!strings.Contains(claims.Scope, "admin.directory.user.readonly"))) {
						t.Errorf("incorrect delegation or scopes: %s", claimsBytes)
					}
					subject = claims.Subject
				}
			}
			return http.StatusOK, fmt.Sprintf(`{"access_token":%q}`, subject)
		}
		switch request.URL.Path {
		case "/admin/directory/v1/groups/team@fysiksektionen.se":
			return http.StatusOK, `{"id":"group-id","email":"team@fysiksektionen.se","name":"Team"}`
		case "/admin/directory/v1/groups/second@fysiksektionen.se":
			return http.StatusOK, `{"id":"second-id","email":"second@fysiksektionen.se","name":"Second"}`
		case "/admin/directory/v1/groups/group-id/members", "/admin/directory/v1/groups/second-id/members":
			if request.URL.Query().Get("maxResults") != "200" {
				t.Errorf("incorrect Google Directory page size: %s", request.URL.RawQuery)
			}
			memberRequests++
			if request.URL.Query().Get("pageToken") == "" {
				return http.StatusOK, `{"members":[{"email":"nested@fysiksektionen.se","type":"GROUP"}],"nextPageToken":"next"}`
			}
			return http.StatusOK, `{"members":[{"email":"indirect@fysiksektionen.se","type":"USER"}]}`
		default:
			t.Errorf("unexpected Google request: %s %s", request.Method, request.URL)
			return http.StatusNotFound, `{}`
		}
	}, func(request *http.Request) (int, string) {
		if request.URL.Path == "/v1/groups:lookup" {
			return 200, `{"name":"groups/id"}`
		}
		if !creatorCanRead && request.Header.Get("Authorization") == "Bearer creator@fysiksektionen.se" {
			return http.StatusForbidden, `{"error":{"status":"PERMISSION_DENIED"}}`
		}
		return 200, `{"memberships":[]}`
	})
	base := &categoryTestAPI{
		kv: map[string][]byte{}, user: &model.User{Id: "requester", Username: "requester", Email: "requester@fysiksektionen.se"},
		channel: model.Channel{Id: "target", Name: "target", TeamId: "team", Type: model.ChannelTypeOpen},
	}
	api := &googleGroupSyncTestAPI{
		categoryTestAPI: base,
		usersByID:       map[string]*model.User{"creator": {Id: "creator", Email: "creator@fysiksektionen.se"}},
		teamAdmins:      map[string]bool{"requester": true},
		users: map[string]*model.User{
			"indirect@fysiksektionen.se": {Id: "indirect", Email: "indirect@fysiksektionen.se"},
			"nested@fysiksektionen.se":   {Id: "nested", Email: "nested@fysiksektionen.se"},
		},
		teamMembers: map[string]*model.TeamMember{
			"indirect": {UserId: "indirect", TeamId: "team"}, "nested": {UserId: "nested", TeamId: "team"},
		},
		channelMembers: map[string]*model.ChannelMember{"requester": {ChannelId: "target", UserId: "requester"}},
	}
	p := &Plugin{pluginBot: model.Bot{UserId: "bot"}, configuration: &configuration{
		GoogleGroupsSyncDomain: "fysiksektionen.se", GoogleGroupsSyncServiceAccountKey: key,
		GoogleGroupChannelTeamAdminThreshold: 30, GoogleGroupChannelSystemAdminThreshold: 100,
	}}
	p.SetAPI(api)
	job := googleGroupSync{
		GroupEmail: "team@fysiksektionen.se", ChannelID: "target", TeamID: "team",
		CreatedBy: "creator", ActingEmail: "creator@fysiksektionen.se", RequesterEmail: "creator@fysiksektionen.se",
		DeadlineAt: time.Now().Add(time.Hour).UnixMilli(), LastSyncedAt: time.Now().UnixMilli(),
	}
	recordKey := googleGroupSyncKey("team", "target", job.GroupEmail)
	recordData, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	api.kv[recordKey] = recordData
	otherJob := job
	otherJob.ChannelID = "other"
	otherJob.GroupEmail = "other@fysiksektionen.se"
	otherKey := googleGroupSyncKey("team", "other", otherJob.GroupEmail)
	otherData, err := json.Marshal(otherJob)
	if err != nil {
		t.Fatal(err)
	}
	api.kv[otherKey] = otherData
	args := &model.CommandArgs{Command: "/google group sync now", UserId: "requester", TeamId: "team", ChannelId: "target", TriggerId: "trigger", SiteURL: "https://mattermost.example"}
	secondJob := job
	secondJob.GroupEmail = "second@fysiksektionen.se"
	secondKey := googleGroupSyncKey("team", "target", secondJob.GroupEmail)
	secondData, err := json.Marshal(secondJob)
	if err != nil {
		t.Fatal(err)
	}
	api.kv[secondKey] = secondData
	multipleResponse, multipleErr := p.ExecuteCommand(nil, args)
	if multipleErr != nil || multipleResponse == nil || !strings.Contains(multipleResponse.Text, "confirm") {
		t.Fatalf("multiple sync preview: %+v, %v", multipleResponse, multipleErr)
	}
	multipleIntro := api.dialog.Dialog.IntroductionText
	if strings.Count(multipleIntro, "> **Google Group details**") != 2 ||
		!strings.Contains(multipleIntro, "team@fysiksektionen.se") || !strings.Contains(multipleIntro, "second@fysiksektionen.se") ||
		strings.Count(multipleIntro, "requester@fysiksektionen.se") != 1 ||
		strings.Count(multipleIntro, "~target") != 1 ||
		!strings.HasSuffix(multipleIntro, "You are performing this action as `requester@fysiksektionen.se`") {
		t.Fatalf("multiple sync preview did not separate group details from the requester: %s", multipleIntro)
	}
	delete(api.kv, secondKey)
	memberRequests = 0
	response, appErr := p.ExecuteCommand(nil, args)
	if appErr != nil || !strings.Contains(response.Text, "confirm") {
		t.Fatalf("manual sync preview: %+v, %v", response, appErr)
	}
	for _, want := range []string{"Team", "team@fysiksektionen.se", "Members:** 2", "indirect@fysiksektionen.se", "~target", "requester@fysiksektionen.se"} {
		if !strings.Contains(api.dialog.Dialog.IntroductionText, want) {
			t.Fatalf("confirmation missing %q: %s", want, api.dialog.Dialog.IntroductionText)
		}
	}
	assertGoogleGroupSyncPreviewOmitsCreatorMetadata(t, api.dialog.Dialog.IntroductionText)
	elements := api.dialog.Dialog.Elements
	if len(elements) != 1 || elements[0].DisplayName != "Account permission:" || elements[0].Name != "confirm" ||
		elements[0].Type != "radio" || elements[0].Default != "ok" || elements[0].Optional ||
		len(elements[0].Options) != 1 || elements[0].Options[0].Value != "ok" ||
		elements[0].Options[0].Text != "OK. You can perform this operation." {
		t.Fatalf("confirmation needs a preselected required radio value for mobile: %+v", elements)
	}
	introduction := api.dialog.Dialog.IntroductionText
	if !strings.Contains(introduction, "> **Google Group details**") ||
		strings.Count(introduction, "requester@fysiksektionen.se") != 1 ||
		strings.Count(introduction, "~target") != 1 ||
		!strings.HasSuffix(introduction, "You are performing this action as `requester@fysiksektionen.se`") {
		t.Fatalf("manual sync preview should show quoted group details before the requester without duplication: %s", introduction)
	}
	submission := model.SubmitDialogRequest{CallbackId: googleGroupConfirmationID, State: api.dialog.Dialog.State, UserId: "requester", TeamId: "team", ChannelId: "target", Submission: map[string]any{"confirm": false}}
	body, _ := json.Marshal(submission)
	submit := func() *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/google-groups/confirm", bytes.NewReader(body))
		request.Header.Set("Mattermost-User-Id", "requester")
		p.serveGoogleGroupConfirmation(recorder, request)
		return recorder
	}
	recorder := httptest.NewRecorder()
	unauthenticated := httptest.NewRequest(http.MethodPost, "/google-groups/confirm", bytes.NewReader(body))
	p.serveGoogleGroupConfirmation(recorder, unauthenticated)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated confirmation was accepted: %d", recorder.Code)
	}
	for _, invalid := range []map[string]any{nil, {"confirm": ""}, {"confirm": false}, {"confirm": "other"}} {
		submission.Submission = invalid
		body, _ = json.Marshal(submission)
		recorder = submit()
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Select OK to confirm") || len(api.added) != 0 ||
			len(api.kv[googleGroupConfirmationPrefix+submission.State]) == 0 {
			t.Fatalf("invalid confirmation was accepted or consumed: %d %s", recorder.Code, recorder.Body.String())
		}
	}
	submission.Submission = map[string]any{"confirm": "ok"}
	body, _ = json.Marshal(submission)
	recorder = submit()
	if recorder.Code != http.StatusOK {
		t.Fatalf("confirmation failed: %d %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "creator@fysiksektionen.se") || !strings.Contains(recorder.Body.String(), "requester@fysiksektionen.se") {
		t.Fatalf("manual sync report missing accounts: %s", recorder.Body.String())
	}
	if !reflect.DeepEqual(api.added, []string{"indirect"}) {
		t.Fatalf("only the indirect user should be added, got %v", api.added)
	}
	if !bytes.Equal(api.kv[recordKey], recordData) || !bytes.Equal(api.kv[otherKey], otherData) {
		t.Fatal("manual sync changed the stored schedule")
	}
	if memberRequests != 4 {
		t.Fatalf("expected paginated initial and confirmation reads, got %d", memberRequests)
	}
	recorder = submit()
	if recorder.Code == http.StatusOK {
		t.Fatal("replayed confirmation was accepted")
	}
	response, appErr = p.ExecuteCommand(nil, args)
	if appErr != nil || !strings.Contains(response.Text, "confirm") {
		t.Fatalf("second preview: %+v, %v", response, appErr)
	}
	submission.State = api.dialog.Dialog.State
	submission.Submission = map[string]any{"confirm": "ok"}
	body, _ = json.Marshal(submission)
	creatorCanRead = false
	recorder = submit()
	if !strings.Contains(recorder.Body.String(), "verify sync creator") || len(api.added) != 1 {
		t.Fatalf("manual sync accepted revoked creator access: %s", recorder.Body.String())
	}
	creatorCanRead = true
	response, appErr = p.ExecuteCommand(nil, args)
	if appErr != nil || !strings.Contains(response.Text, "confirm") {
		t.Fatalf("third preview: %+v, %v", response, appErr)
	}
	submission.State = api.dialog.Dialog.State
	body, _ = json.Marshal(submission)
	job.DeadlineAt += int64(time.Hour / time.Millisecond)
	api.kv[recordKey], _ = json.Marshal(job)
	recorder = submit()
	if !strings.Contains(recorder.Body.String(), "changed") || len(api.added) != 1 {
		t.Fatalf("changed sync was executed: status=%d added=%v", recorder.Code, api.added)
	}
}

func TestGoogleSyncNowRejectsNonMemberAndInvalidChannel(t *testing.T) {
	base := &categoryTestAPI{channel: model.Channel{Id: "target", Name: "target", TeamId: "team", Type: model.ChannelTypePrivate}}
	p := &Plugin{}
	p.SetAPI(&googleGroupSyncTestAPI{categoryTestAPI: base, teamAdmins: map[string]bool{"requester": true}})
	for _, tc := range []struct{ command, channel, want string }{
		{"/google group sync now", "target", "no longer a member"},
		{"/google group sync now", "", "active public or private team channel"},
	} {
		response, appErr := p.googleGroupSyncCommand(&model.CommandArgs{Command: tc.command, TeamId: "team", ChannelId: tc.channel, UserId: "requester"})
		if appErr != nil || !strings.Contains(response.Text, tc.want) {
			t.Errorf("%s: response=%+v error=%v", tc.command, response, appErr)
		}
	}
	base.channel.Type = model.ChannelTypeDirect
	response, appErr := p.googleGroupSyncCommand(&model.CommandArgs{Command: "/google group sync now", TeamId: "team", ChannelId: "target", UserId: "requester"})
	if appErr != nil || !strings.Contains(response.Text, "active public or private team channel") {
		t.Fatalf("direct channel: response=%+v error=%v", response, appErr)
	}
}

func TestGoogleSyncRequiresAdminForEveryCommand(t *testing.T) {
	api := &googleGroupSyncTestAPI{categoryTestAPI: &categoryTestAPI{kv: map[string][]byte{}}, teamAdmins: map[string]bool{}, systemAdmins: map[string]bool{}}
	p := &Plugin{}
	p.SetAPI(api)
	for _, command := range []string{
		"/google group sync start team@example.org 2099-01-01",
		"/google group sync stop team@example.org",
		"/google group sync list",
		"/google group sync now",
	} {
		response, appErr := p.googleGroupSyncCommand(&model.CommandArgs{Command: command, UserId: "requester", TeamId: "team"})
		if appErr != nil || !strings.Contains(response.Text, "admins") {
			t.Errorf("%s: response=%+v error=%v", command, response, appErr)
		}
	}
	for _, action := range []string{"sync_add", "sync_remove", "sync_now"} {
		if _, err := p.applyGoogleGroupConfirmation(googleGroupConfirmation{Action: action, UserID: "requester", TeamID: "team"}); err == nil || !strings.Contains(err.Error(), "admins") {
			t.Errorf("%s accepted revoked admin: %v", action, err)
		}
	}
}

func TestGoogleSyncDeadlineRequiresSystemAdminBeyondOneYear(t *testing.T) {
	key := setupGoogleTestTransport(t, func(request *http.Request) (int, string) {
		if request.URL.Host == "oauth2.googleapis.com" {
			return http.StatusOK, `{"access_token":"token"}`
		}
		if strings.HasSuffix(request.URL.Path, "/members") {
			return http.StatusOK, `{"members":[]}`
		}
		return http.StatusOK, `{"id":"id","email":"team@example.org"}`
	})
	api := &googleGroupSyncTestAPI{
		categoryTestAPI: &categoryTestAPI{
			kv:      map[string][]byte{},
			user:    &model.User{Id: "caller", Email: "caller@example.org"},
			channel: model.Channel{Id: "channel", TeamId: "team", Type: model.ChannelTypeOpen},
		},
		teamAdmins:     map[string]bool{"caller": true},
		systemAdmins:   map[string]bool{},
		channelMembers: map[string]*model.ChannelMember{"caller": {ChannelId: "channel", UserId: "caller"}},
	}
	p := &Plugin{configuration: &configuration{GoogleGroupsSyncDomain: "example.org", GoogleGroupsSyncServiceAccountKey: key}}
	p.SetAPI(api)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		date string
		want bool
	}{
		{"2027-06-15", true},
		{"2027-06-16", false},
	} {
		deadline, err := parseGoogleGroupDeadline(tc.date, now)
		if err != nil {
			t.Fatal(err)
		}
		allowed := p.googleGroupSyncDeadlinePermissionMessage("caller", deadline, now) == ""
		if allowed != tc.want {
			t.Fatalf("deadline %s allowed = %v; want %v", tc.date, allowed, tc.want)
		}
	}
	args := &model.CommandArgs{
		Command: "/google group sync start team@example.org " + time.Now().UTC().AddDate(2, 0, 0).Format("2006-01-02"),
		UserId:  "caller", TeamId: "team", ChannelId: "channel", TriggerId: "trigger",
	}
	response, appErr := p.googleGroupSyncCommand(args)
	if appErr != nil || response == nil || !strings.Contains(response.Text, "Only system admins") {
		t.Fatalf("long sync accepted for team admin: %+v %v", response, appErr)
	}
	api.systemAdmins["caller"] = true
	response, appErr = p.googleGroupSyncCommand(args)
	if appErr != nil || response == nil || !strings.Contains(response.Text, "confirm") {
		t.Fatalf("long sync rejected for system admin: %+v %v", response, appErr)
	}
	var plan googleGroupConfirmation
	if err := json.Unmarshal(api.kv[googleGroupConfirmationPrefix+api.dialog.Dialog.State], &plan); err != nil {
		t.Fatal(err)
	}
	api.systemAdmins["caller"] = false
	if _, err := p.applyGoogleGroupConfirmation(plan); err == nil || !strings.Contains(err.Error(), "Only system admins") {
		t.Fatalf("long sync accepted after system admin permission was revoked: %v", err)
	}
	if len(api.kv[googleGroupSyncKey("team", "channel", "team@example.org")]) != 0 {
		t.Fatal("long sync was saved after permission was revoked")
	}
}

func TestGoogleLegacyMemberConfirmationDisabled(t *testing.T) {
	id := model.NewId()
	plan := googleGroupConfirmation{Action: "member_add", UserID: "requester", TeamID: "team", ChannelID: "channel", ExpiresAt: time.Now().Add(time.Minute).UnixMilli()}
	data, _ := json.Marshal(plan)
	api := &googleGroupSyncTestAPI{categoryTestAPI: &categoryTestAPI{kv: map[string][]byte{googleGroupConfirmationPrefix + id: data}}}
	p := &Plugin{}
	p.SetAPI(api)
	body, _ := json.Marshal(model.SubmitDialogRequest{CallbackId: googleGroupConfirmationID, State: id, UserId: "requester", TeamId: "team", ChannelId: "channel"})
	request := httptest.NewRequest(http.MethodPost, "/google-groups/confirm", bytes.NewReader(body))
	request.Header.Set("Mattermost-User-Id", "requester")
	recorder := httptest.NewRecorder()
	p.serveGoogleGroupConfirmation(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "no longer available") {
		t.Fatalf("legacy member confirmation was accepted: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestGoogleChannelCommands(t *testing.T) {
	var members = `[{"email":"requester@fysiksektionen.se","type":"USER"},{"email":"indirect@fysiksektionen.se","type":"USER"},{"email":"nested@fysiksektionen.se","type":"GROUP"}]`
	key := setupGoogleTestTransport(t, func(request *http.Request) (int, string) {
		if request.URL.Host == "oauth2.googleapis.com" {
			return http.StatusOK, `{"access_token":"token"}`
		}
		if request.Method != http.MethodGet {
			t.Errorf("unexpected Google write request: %s %s", request.Method, request.URL)
			return http.StatusForbidden, `{}`
		}
		switch request.URL.Path {
		case "/admin/directory/v1/groups/team@fysiksektionen.se":
			return http.StatusOK, `{"id":"group-id","email":"team@fysiksektionen.se","name":"Team"}`
		case "/admin/directory/v1/groups/group-id/members":
			if request.URL.Query().Get("includeDerivedMembership") != "true" {
				t.Error("channel command did not request indirect membership")
			}
			return http.StatusOK, `{"members":` + members + `}`
		default:
			t.Errorf("unexpected Google request: %s", request.URL)
			return http.StatusNotFound, `{}`
		}
	})
	base := &categoryTestAPI{
		kv: map[string][]byte{}, user: &model.User{Id: "requester", Email: "requester@fysiksektionen.se"},
		channel: model.Channel{Id: "channel", Name: "target", TeamId: "team", Type: model.ChannelTypeOpen},
	}
	api := &googleGroupSyncTestAPI{
		categoryTestAPI: base,
		users: map[string]*model.User{
			"requester@fysiksektionen.se": {Id: "requester"},
			"indirect@fysiksektionen.se":  {Id: "indirect"},
			"nested@fysiksektionen.se":    {Id: "nested"},
		},
		teamMembers: map[string]*model.TeamMember{"indirect": {UserId: "indirect", TeamId: "team"}},
		channelMembers: map[string]*model.ChannelMember{
			"requester": {ChannelId: "channel", UserId: "requester"},
			"nested":    {ChannelId: "channel", UserId: "nested"},
		},
	}
	p := &Plugin{configuration: &configuration{
		GoogleGroupsSyncDomain: "fysiksektionen.se", GoogleGroupsSyncServiceAccountKey: key,
		GoogleGroupChannelTeamAdminThreshold: 30, GoogleGroupChannelSystemAdminThreshold: 100,
	}}
	p.SetAPI(api)
	recordKey := googleGroupSyncKey("team", "channel", "team@fysiksektionen.se")
	api.kv[recordKey] = []byte("existing sync")
	args := &model.CommandArgs{UserId: "requester", TeamId: "team", ChannelId: "channel", TriggerId: "trigger", SiteURL: "https://mattermost.example"}
	preview := func(command string) googleGroupConfirmation {
		t.Helper()
		args.Command = command
		response, appErr := p.ExecuteCommand(nil, args)
		if appErr != nil || !strings.Contains(response.Text, "confirm") {
			t.Fatalf("%s preview: %+v, %v", command, response, appErr)
		}
		intro := api.dialog.Dialog.IntroductionText
		for _, want := range []string{"Team", "team@fysiksektionen.se", "Members:** 3", "indirect@fysiksektionen.se", "~target"} {
			if !strings.Contains(intro, want) {
				t.Fatalf("preview missing %q: %s", want, intro)
			}
		}
		var plan googleGroupConfirmation
		if err := json.Unmarshal(api.kv[googleGroupConfirmationPrefix+api.dialog.Dialog.State], &plan); err != nil {
			t.Fatal(err)
		}
		return plan
	}
	plan := preview("/google group add team")
	if _, err := p.applyGoogleGroupConfirmation(plan); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(api.added, []string{"indirect"}) {
		t.Fatalf("incorrect additions: %v", api.added)
	}
	plan = preview("/google group remove team")
	if _, err := p.applyGoogleGroupConfirmation(plan); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(api.removed, []string{"indirect"}) || api.channelMembers["requester"] == nil || api.channelMembers["nested"] == nil {
		t.Fatalf("incorrect removals or protected members: removed=%v members=%v", api.removed, api.channelMembers)
	}
	if string(api.kv[recordKey]) != "existing sync" {
		t.Fatal("one-time command changed the sync configuration")
	}
	plan = preview("/google group add team@fysiksektionen.se")
	members = `[{"email":"new@fysiksektionen.se","type":"USER"}]`
	if _, err := p.applyGoogleGroupConfirmation(plan); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("changed Google snapshot accepted: %v", err)
	}
}

func TestGoogleChannelRemovalReportsPartialFailure(t *testing.T) {
	base := &categoryTestAPI{}
	api := &googleGroupSyncTestAPI{
		categoryTestAPI: base,
		users: map[string]*model.User{
			"a@example.org": {Id: "a"},
			"b@example.org": {Id: "b"},
		},
		channelMembers: map[string]*model.ChannelMember{
			"a": {ChannelId: "channel", UserId: "a"},
			"b": {ChannelId: "channel", UserId: "b"},
		},
		failRemove: "a",
	}
	p := &Plugin{}
	p.SetAPI(api)
	count, err := p.removeGoogleGroupChannelMembers(&googleDirectoryGroupSnapshot{Members: []googleDirectoryGroupMember{
		{Email: "a@example.org", Type: "USER"}, {Email: "b@example.org", Type: "USER"},
	}}, "channel", "requester")
	if count != 1 || err == nil || !strings.Contains(err.Error(), "a@example.org") || !reflect.DeepEqual(api.removed, []string{"b"}) {
		t.Fatalf("expected partial failure and one removal: count=%d err=%v removed=%v", count, err, api.removed)
	}
}

func TestGoogleChannelLargeGroupPermissionRechecked(t *testing.T) {
	members := make([]googleDirectoryGroupMember, 51)
	for i := range members {
		members[i] = googleDirectoryGroupMember{Email: "person@example.org", Type: "USER"}
	}
	memberData, err := json.Marshal(members)
	if err != nil {
		t.Fatal(err)
	}
	key := setupGoogleTestTransport(t, func(request *http.Request) (int, string) {
		if request.URL.Host == "oauth2.googleapis.com" {
			return http.StatusOK, `{"access_token":"token"}`
		}
		if strings.HasSuffix(request.URL.Path, "/members") {
			return http.StatusOK, `{"members":` + string(memberData) + `}`
		}
		return http.StatusOK, `{"id":"id","email":"team@fysiksektionen.se"}`
	})
	base := &categoryTestAPI{
		kv: map[string][]byte{}, user: &model.User{Id: "requester", Email: "requester@fysiksektionen.se"},
		channel: model.Channel{Id: "channel", Name: "target", TeamId: "team", Type: model.ChannelTypeOpen},
	}
	api := &googleGroupSyncTestAPI{
		categoryTestAPI: base, teamAdmins: map[string]bool{},
		channelMembers: map[string]*model.ChannelMember{"requester": {ChannelId: "channel", UserId: "requester"}},
	}
	p := &Plugin{configuration: &configuration{
		GoogleGroupsSyncDomain: "fysiksektionen.se", GoogleGroupsSyncServiceAccountKey: key,
		GoogleGroupChannelTeamAdminThreshold: 30, GoogleGroupChannelSystemAdminThreshold: 100,
	}}
	p.SetAPI(api)
	args := &model.CommandArgs{Command: "/google group remove team@fysiksektionen.se", UserId: "requester", TeamId: "team", ChannelId: "channel", TriggerId: "trigger", SiteURL: "https://mattermost.example"}
	response, appErr := p.googleGroupChannelCommand(args)
	if appErr != nil || !strings.Contains(response.Text, "admins") {
		t.Fatalf("non-admin was not denied: %+v %v", response, appErr)
	}
	api.teamAdmins["requester"] = true
	response, appErr = p.googleGroupChannelCommand(args)
	if appErr != nil || !strings.Contains(response.Text, "confirm") {
		t.Fatalf("admin preview: %+v %v", response, appErr)
	}
	var plan googleGroupConfirmation
	if err := json.Unmarshal(api.kv[googleGroupConfirmationPrefix+api.dialog.Dialog.State], &plan); err != nil {
		t.Fatal(err)
	}
	api.teamAdmins["requester"] = false
	if _, err := p.applyGoogleGroupConfirmation(plan); err == nil || !strings.Contains(err.Error(), "admins") {
		t.Fatalf("confirmation accepted revoked permission: %v", err)
	}
}

func TestGoogleSyncListDeniesInaccessibleGroup(t *testing.T) {
	key := setupGoogleTestTransport(t, func(request *http.Request) (int, string) {
		if request.URL.Host == "oauth2.googleapis.com" {
			return http.StatusOK, `{"access_token":"token"}`
		}
		return http.StatusForbidden, `{"error":"forbidden"}`
	})
	job := googleGroupSync{GroupEmail: "private@fysiksektionen.se", GroupName: "Private group", TeamID: "team", ChannelID: "channel", DeadlineAt: time.Now().Add(time.Hour).UnixMilli()}
	data, _ := json.Marshal(job)
	base := &categoryTestAPI{
		kv:      map[string][]byte{googleGroupSyncKey("team", "channel", job.GroupEmail): data},
		user:    &model.User{Id: "requester", Email: "requester@fysiksektionen.se"},
		channel: model.Channel{Id: "channel", Name: "target", TeamId: "team", Type: model.ChannelTypeOpen},
	}
	api := &googleGroupSyncTestAPI{
		categoryTestAPI: base, teamAdmins: map[string]bool{"requester": true},
		channelMembers: map[string]*model.ChannelMember{"requester": {ChannelId: "channel", UserId: "requester"}},
	}
	p := &Plugin{configuration: &configuration{GoogleGroupsSyncDomain: "fysiksektionen.se", GoogleGroupsSyncServiceAccountKey: key}}
	p.SetAPI(api)
	response, appErr := p.googleGroupSyncCommand(&model.CommandArgs{Command: "/google group sync list", UserId: "requester", TeamId: "team", ChannelId: "channel"})
	if appErr != nil || !strings.Contains(response.Text, "no syncs were listed") || strings.Contains(response.Text, "Private group") {
		t.Fatalf("list did not protect inaccessible group: %+v %v", response, appErr)
	}
}

func TestGoogleScheduledSyncSkipsRevokedCreator(t *testing.T) {
	reads := 0
	key := setupGoogleTestTransport(t, func(request *http.Request) (int, string) {
		reads++
		return http.StatusForbidden, `{}`
	})
	job := googleGroupSync{GroupEmail: "team@fysiksektionen.se", TeamID: "team", ChannelID: "channel", CreatedBy: "requester", ActingEmail: "requester@fysiksektionen.se", DeadlineAt: time.Now().Add(time.Hour).UnixMilli()}
	data, _ := json.Marshal(job)
	recordKey := googleGroupSyncKey("team", "channel", job.GroupEmail)
	base := &categoryTestAPI{kv: map[string][]byte{recordKey: data}, user: &model.User{Id: "requester", Email: "requester@fysiksektionen.se"}}
	api := &googleGroupSyncTestAPI{categoryTestAPI: base}
	p := &Plugin{configuration: &configuration{GoogleGroupsSyncDomain: "fysiksektionen.se", GoogleGroupsSyncServiceAccountKey: key}}
	p.SetAPI(api)
	p.runGoogleGroupSyncJobs(context.Background())
	if reads != 0 || !bytes.Equal(api.kv[recordKey], data) {
		t.Fatalf("revoked creator's sync ran or was removed: reads=%d", reads)
	}
}

func TestGoogleSyncStartAndStop(t *testing.T) {
	key := setupGoogleTestTransport(t, func(request *http.Request) (int, string) {
		if request.URL.Host == "oauth2.googleapis.com" {
			return http.StatusOK, `{"access_token":"token"}`
		}
		if request.Method != http.MethodGet {
			t.Errorf("unexpected Google write request: %s", request.Method)
			return http.StatusForbidden, `{}`
		}
		if strings.HasSuffix(request.URL.Path, "/members") {
			return http.StatusOK, `{"members":[{"email":"indirect@fysiksektionen.se","type":"USER"}]}`
		}
		return http.StatusOK, `{"id":"id","email":"team@fysiksektionen.se","name":"Team"}`
	})
	base := &categoryTestAPI{
		kv: map[string][]byte{}, user: &model.User{Id: "requester", Username: "requester", Email: "requester@fysiksektionen.se"},
		channel: model.Channel{Id: "channel", Name: "target", TeamId: "team", Type: model.ChannelTypeOpen},
	}
	api := &googleGroupSyncTestAPI{
		categoryTestAPI: base, teamAdmins: map[string]bool{"requester": true},
		users:          map[string]*model.User{"indirect@fysiksektionen.se": {Id: "indirect"}},
		teamMembers:    map[string]*model.TeamMember{"indirect": {UserId: "indirect", TeamId: "team"}},
		channelMembers: map[string]*model.ChannelMember{"requester": {ChannelId: "channel", UserId: "requester"}},
	}
	p := &Plugin{googleSyncWake: make(chan struct{}, 1), configuration: &configuration{
		GoogleGroupsSyncDomain: "fysiksektionen.se", GoogleGroupsSyncServiceAccountKey: key,
		GoogleGroupChannelTeamAdminThreshold: 30, GoogleGroupChannelSystemAdminThreshold: 100,
	}}
	p.SetAPI(api)
	args := &model.CommandArgs{UserId: "requester", TeamId: "team", ChannelId: "channel", TriggerId: "trigger", SiteURL: "https://mattermost.example"}
	for _, command := range []string{
		"/google group sync add team@fysiksektionen.se 2099-01-01",
		"/google group sync remove team@fysiksektionen.se",
	} {
		args.Command = command
		response, appErr := p.googleGroupSyncCommand(args)
		if appErr != nil || !strings.Contains(response.Text, "Usage:") {
			t.Fatalf("legacy command %s was not rejected: response=%+v error=%v", command, response, appErr)
		}
	}
	execute := func(command string) {
		t.Helper()
		args.Command = command
		response, appErr := p.googleGroupSyncCommand(args)
		if appErr != nil || !strings.Contains(response.Text, "confirm") {
			t.Fatalf("%s preview: %+v %v", command, response, appErr)
		}
		var plan googleGroupConfirmation
		if err := json.Unmarshal(api.kv[googleGroupConfirmationPrefix+api.dialog.Dialog.State], &plan); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(api.dialog.Dialog.IntroductionText, "requester@fysiksektionen.se") {
			t.Fatalf("sync preview missing account: %s", api.dialog.Dialog.IntroductionText)
		}
		assertGoogleGroupSyncPreviewOmitsCreatorMetadata(t, api.dialog.Dialog.IntroductionText)
		if strings.Count(api.dialog.Dialog.IntroductionText, "requester@fysiksektionen.se") != 1 {
			t.Fatalf("sync preview repeats requester account: %s", api.dialog.Dialog.IntroductionText)
		}
		if strings.Contains(command, " sync start ") && !strings.Contains(api.dialog.Dialog.IntroductionText, strings.Fields(command)[5]+" (UTC)") {
			t.Fatalf("sync preview missing deadline: %s", api.dialog.Dialog.IntroductionText)
		}
		message, err := p.applyGoogleGroupConfirmation(plan)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(message, "requester@fysiksektionen.se") || !strings.Contains(message, "`requester`") {
			t.Fatalf("sync report missing account: %s", message)
		}
	}
	recordKey := googleGroupSyncKey("team", "channel", "team@fysiksektionen.se")
	execute("/google group sync start team " + time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02"))
	select {
	case <-p.googleSyncWake:
	default:
		t.Fatal("new sync did not wake the continuous scheduler")
	}
	var job googleGroupSync
	if err := json.Unmarshal(api.kv[recordKey], &job); err != nil || job.LastSyncedAt == 0 || !reflect.DeepEqual(api.added, []string{"indirect"}) {
		t.Fatalf("sync was not saved and run: job=%+v added=%v error=%v", job, api.added, err)
	}
	list, appErr := p.googleGroupSyncCommand(&model.CommandArgs{Command: "/google group sync list", UserId: "requester", TeamId: "team", ChannelId: "channel"})
	location, err := time.LoadLocation(googleGroupSyncDisplayTimeZone)
	if err != nil {
		t.Fatal(err)
	}
	expectedNextSync := formatGoogleGroupSyncTime(googleGroupNextSyncAt(job, time.Now()), location)
	expectedLastSync := formatGoogleGroupSyncTime(job.LastSyncedAt, location)
	expectedExpiry := time.UnixMilli(job.DeadlineAt).UTC().Format("2006-01-02")
	if appErr != nil || list == nil || !strings.Contains(list.Text, "requester@fysiksektionen.se") ||
		!strings.Contains(list.Text, "Started by: @requester") ||
		!strings.Contains(list.Text, "Sync cycle: 1 hour") ||
		!strings.Contains(list.Text, "Last synced: "+expectedLastSync) ||
		!strings.Contains(list.Text, "Next sync: "+expectedNextSync) ||
		!strings.Contains(list.Text, "Expires: "+expectedExpiry+" UTC") {
		t.Fatalf("sync list missing account: %+v %v", list, appErr)
	}
	cfg := *p.getConfiguration()
	cfg.GoogleGroupsSyncMode = "continuous"
	cfg.GoogleGroupsSyncDelaySeconds = 10
	p.setConfiguration(&cfg)
	delete(api.channelMembers, "indirect")
	api.added = nil
	p.runGoogleGroupSyncJobsMode(context.Background(), true)
	if !reflect.DeepEqual(api.added, []string{"indirect"}) {
		t.Fatalf("continuous sync did not run despite a recent hourly sync: %v", api.added)
	}
	otherJob := googleGroupSync{GroupEmail: "other@example.org", TeamID: "another-team", DeadlineAt: time.Now().Add(time.Hour).UnixMilli()}
	otherKey := googleGroupSyncKey(otherJob.TeamID, "", otherJob.GroupEmail)
	api.kv[otherKey], err = json.Marshal(otherJob)
	if err != nil {
		t.Fatal(err)
	}
	list, appErr = p.googleGroupSyncCommand(&model.CommandArgs{Command: "/google group sync list", UserId: "requester", TeamId: "team", ChannelId: "channel"})
	if appErr != nil || !strings.Contains(list.Text, "Sync approximately every 1 min") ||
		!strings.Contains(list.Text, "Minimum sync cycle: 20 seconds") || strings.Contains(list.Text, otherJob.GroupEmail) {
		t.Fatalf("continuous sync list: %+v %v", list, appErr)
	}
	for _, tc := range []struct {
		delay int
		want  string
	}{{29, "1 min"}, {30, "1 min"}, {31, "2 min"}} {
		cfg := *p.getConfiguration()
		cfg.GoogleGroupsSyncDelaySeconds = tc.delay
		p.setConfiguration(&cfg)
		list, appErr = p.googleGroupSyncCommand(&model.CommandArgs{Command: "/google group sync list", UserId: "requester", TeamId: "team", ChannelId: "channel"})
		if appErr != nil || !strings.Contains(list.Text, "Sync approximately every "+tc.want) || strings.Contains(list.Text, "Next sync:") {
			t.Fatalf("continuous sync list for %ds delay: %+v %v", tc.delay, list, appErr)
		}
	}
	delete(api.kv, otherKey)
	execute("/google group sync stop team@fysiksektionen.se")
	if len(api.kv[recordKey]) != 0 || api.channelMembers["indirect"] == nil {
		t.Fatal("removing sync did not preserve channel members")
	}
	execute("/google group sync start team@fysiksektionen.se " + time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02"))
	execute("/google group sync stop team")
	if len(api.kv[recordKey]) != 0 {
		t.Fatal("short group name did not remove the sync created with its full address")
	}
}

func TestGoogleGroupAddRejectsGroupIdentity(t *testing.T) {
	writes := 0
	key := setupGoogleTestTransport(t, func(request *http.Request) (int, string) {
		if request.URL.Host == "oauth2.googleapis.com" {
			return http.StatusOK, `{"access_token":"token"}`
		}

		switch {
		case request.URL.Path == "/admin/directory/v1/groups/target@fysiksektionen.se":
			return http.StatusOK, `{"id":"target-id","email":"target@fysiksektionen.se"}`
		case request.URL.Path == "/admin/directory/v1/groups/target-id/members" && request.Method == http.MethodGet:
			if request.URL.Query().Get("includeDerivedMembership") != "" {
				t.Error("group-write operations must examine direct membership")
			}
			return http.StatusOK, `{"members":[]}`
		case strings.HasPrefix(request.URL.Path, "/admin/directory/v1/users/"):
			return http.StatusNotFound, `{"error":"not a user"}`
		case request.Method == http.MethodPost:
			writes++
			return http.StatusOK, `{}`
		default:
			return http.StatusNotFound, `{}`
		}
	})
	base := &categoryTestAPI{kv: map[string][]byte{}, user: &model.User{Id: "requester", Email: "group@fysiksektionen.se"}}
	p := &Plugin{configuration: &configuration{GoogleGroupsSyncDomain: "fysiksektionen.se", GoogleGroupsSyncServiceAccountKey: key}}
	p.SetAPI(&googleGroupSyncTestAPI{categoryTestAPI: base})
	snapshot, err := p.googleGroupSnapshot("target@fysiksektionen.se", "group@fysiksektionen.se", false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.applyGoogleGroupConfirmation(googleGroupConfirmation{
		Action: "manage_add", UserID: "requester", TeamID: "team", GroupEmail: "target@fysiksektionen.se",
		MemberEmail: "group@fysiksektionen.se", MemberUserID: "requester", ActingEmail: "group@fysiksektionen.se", SnapshotHash: googleGroupSnapshotHash(snapshot),
	})
	if err == nil || !strings.Contains(err.Error(), "verify") || writes != 0 {
		t.Fatalf("group identity was not rejected: err=%v writes=%d", err, writes)
	}
}

func TestGoogleGroupManageDelegatesCallerAndChangesSelectedUser(t *testing.T) {
	var subject string
	var memberPresent bool
	var denyWrite bool
	var writes []string
	key := setupGoogleTestTransport(t, func(request *http.Request) (int, string) {
		if request.URL.Host == "oauth2.googleapis.com" {
			if err := request.ParseForm(); err != nil {
				t.Fatal(err)
			}
			parts := strings.Split(request.Form.Get("assertion"), ".")
			if len(parts) != 3 {
				t.Fatal("invalid token assertion")
			}
			claims, err := base64.RawURLEncoding.DecodeString(parts[1])
			if err != nil {
				t.Fatal(err)
			}
			var data struct {
				Subject string `json:"sub"`
			}
			if err := json.Unmarshal(claims, &data); err != nil {
				t.Fatal(err)
			}
			subject = data.Subject
			return http.StatusOK, `{"access_token":"token"}`
		}
		if subject != "caller@example.org" {
			t.Errorf("management used privileged or fallback delegation: %s", subject)
		}
		switch {
		case request.URL.Path == "/admin/directory/v1/users/target@example.org" && request.Method == http.MethodGet:
			return http.StatusOK, `{"id":"google-user","primaryEmail":"target@example.org"}`
		case request.URL.Path == "/admin/directory/v1/users/caller@example.org" && request.Method == http.MethodGet:
			return http.StatusOK, `{"id":"caller-google-user","primaryEmail":"caller@example.org"}`
		default:
			t.Errorf("unexpected request %s %s", request.Method, request.URL)
			return http.StatusNotFound, `{}`
		}
	}, func(request *http.Request) (int, string) {
		switch {
		case request.URL.Path == "/v1/groups:lookup":
			return http.StatusOK, `{"name":"groups/id"}`
		case request.URL.Path == "/v1/groups/id" && request.Method == http.MethodGet:
			return http.StatusOK, `{"name":"groups/id","groupKey":{"id":"team@example.org"},"displayName":"Team"}`
		case request.URL.Path == "/v1/groups/id/memberships" && request.Method == http.MethodGet:
			if request.URL.Query().Get("view") != "FULL" {
				t.Errorf("management did not request full Cloud Identity membership details: %s", request.URL.RawQuery)
			}
			if memberPresent {
				return http.StatusOK, `{"memberships":[{"name":"groups/id/memberships/member-id","preferredMemberKey":{"id":"target@example.org"},"type":"USER"}]}`
			}
			return http.StatusOK, `{"memberships":[]}`
		case request.URL.Path == "/v1/groups/id/memberships" && request.Method == http.MethodPost:
			if denyWrite {
				return http.StatusForbidden, `{"error":{"errors":[{"reason":"forbidden"}]}}`
			}
			var payload struct {
				MemberKey struct {
					ID string `json:"id"`
				} `json:"preferredMemberKey"`
				Roles []struct {
					Name string `json:"name"`
				} `json:"roles"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil ||
				payload.MemberKey.ID != "target@example.org" || len(payload.Roles) != 1 || payload.Roles[0].Name != "MEMBER" {
				t.Errorf("incorrect Cloud Identity membership payload: %+v %v", payload, err)
			}
			memberPresent = true
			writes = append(writes, "add")
			return http.StatusOK, `{"name":"operations/add","done":false}`
		case request.URL.Path == "/v1/operations/add" && request.Method == http.MethodGet:
			return http.StatusOK, `{"name":"operations/add","done":true}`
		case request.URL.Path == "/v1/groups/id/memberships/member-id" && request.Method == http.MethodDelete:
			memberPresent = false
			writes = append(writes, "remove")
			return http.StatusOK, `{"name":"operations/remove","done":false}`
		case request.URL.Path == "/v1/operations/remove" && request.Method == http.MethodGet:
			return http.StatusOK, `{"name":"operations/remove","done":true}`
		default:
			t.Errorf("unexpected Cloud Identity request %s %s", request.Method, request.URL)
			return http.StatusNotFound, `{}`
		}
	})
	target := &model.User{Id: "target", Username: "target", Email: "target@example.org"}
	base := &categoryTestAPI{kv: map[string][]byte{}, user: &model.User{Id: "caller", Email: "caller@example.org"}}
	api := &googleGroupSyncTestAPI{categoryTestAPI: base, usersByID: map[string]*model.User{"target": target}, usersByName: map[string]*model.User{"target": target}}
	p := &Plugin{pluginBot: model.Bot{UserId: "bot"}, configuration: &configuration{
		GoogleGroupsSyncServiceAccountKey: key, GoogleGroupsSyncDomain: "example.org",
		GoogleGroupsSyncAdminEmail: "admin@example.org", GoogleGroupsSyncFallbackEmail: "fallback@example.org",
		GoogleGroupsSyncAdminUsers: []string{"caller"},
	}}
	p.SetAPI(api)
	args := &model.CommandArgs{UserId: "caller", TeamId: "team", ChannelId: "channel", TriggerId: "trigger"}
	preview := func(action string) googleGroupConfirmation {
		t.Helper()
		args.Command = "/google group manage team " + action + " @target"
		response, appErr := p.ExecuteCommand(nil, args)
		if appErr != nil || response == nil || !strings.Contains(response.Text, "confirm") {
			t.Fatalf("preview %s: %+v %v", action, response, appErr)
		}
		if !strings.Contains(api.dialog.Dialog.IntroductionText, "target@example.org") || !strings.Contains(api.dialog.Dialog.IntroductionText, "caller@example.org") {
			t.Fatalf("preview missing target or actor: %s", api.dialog.Dialog.IntroductionText)
		}
		var plan googleGroupConfirmation
		if err := json.Unmarshal(api.kv[googleGroupConfirmationPrefix+api.dialog.Dialog.State], &plan); err != nil {
			t.Fatal(err)
		}
		return plan
	}
	plan := preview("add")
	denyWrite = true
	if _, err := p.applyGoogleGroupConfirmation(plan); err == nil || !strings.Contains(err.Error(), "may not have permission") ||
		strings.Contains(err.Error(), `"reason"`) || len(writes) != 0 {
		t.Fatalf("Google write denial not surfaced: %v, writes=%v", err, writes)
	}
	denyWrite = false
	plan = preview("add")
	target.Email = "changed@example.org"
	if _, err := p.applyGoogleGroupConfirmation(plan); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("changed target accepted: %v", err)
	}
	target.Email = "target@example.org"
	plan = preview("add")
	if _, err := p.applyGoogleGroupConfirmation(plan); err != nil {
		t.Fatal(err)
	}
	plan = preview("remove")
	submission := model.SubmitDialogRequest{CallbackId: googleGroupConfirmationID, State: api.dialog.Dialog.State, UserId: "caller", TeamId: "team", ChannelId: "channel", Submission: map[string]any{"confirm": "ok"}}
	body, _ := json.Marshal(submission)
	request := httptest.NewRequest(http.MethodPost, "/google-groups/confirm", bytes.NewReader(body))
	request.Header.Set("Mattermost-User-Id", "caller")
	recorder := httptest.NewRecorder()
	p.serveGoogleGroupConfirmation(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Removed") {
		t.Fatalf("group management confirmation failed: %d %s", recorder.Code, recorder.Body.String())
	}
	if !reflect.DeepEqual(writes, []string{"add", "remove"}) {
		t.Fatalf("wrong Google writes: %v", writes)
	}
}

func TestGoogleGroupManageGetDirectMembers(t *testing.T) {
	var empty, deny, denySecondPage bool
	var pages []string
	key := setupGoogleTestTransport(t, func(request *http.Request) (int, string) {
		if request.URL.Host != "oauth2.googleapis.com" {
			t.Errorf("unexpected non-Cloud Identity request: %s", request.URL)
			return http.StatusNotFound, `{}`
		}
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(request.Form.Get("assertion"), ".")
		if len(parts) != 3 {
			t.Fatal("invalid token assertion")
		}
		claims, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			t.Fatal(err)
		}
		var data struct {
			Subject string `json:"sub"`
			Scope   string `json:"scope"`
		}
		if err := json.Unmarshal(claims, &data); err != nil {
			t.Fatal(err)
		}
		if data.Subject != "caller@example.org" || data.Scope != googleCloudIdentityReadScope {
			t.Errorf("unexpected delegation: %+v", data)
		}
		return http.StatusOK, `{"access_token":"token"}`
	}, func(request *http.Request) (int, string) {
		if request.Method != http.MethodGet {
			t.Errorf("unexpected write: %s", request.Method)
			return http.StatusMethodNotAllowed, `{}`
		}
		switch request.URL.Path {
		case "/v1/groups:lookup":
			if request.URL.Query().Get("groupKey.id") != "team@example.org" {
				t.Errorf("unexpected group key: %s", request.URL.RawQuery)
			}
			return http.StatusOK, `{"name":"groups/id"}`
		case "/v1/groups/id":
			return http.StatusOK, `{"name":"groups/id","groupKey":{"id":"team@example.org"}}`
		case "/v1/groups/id/memberships":
			page := request.URL.Query().Get("pageToken")
			pages = append(pages, page)
			if request.URL.Query().Get("view") != "FULL" {
				t.Errorf("missing FULL view: %s", request.URL.RawQuery)
			}
			if deny || (denySecondPage && page == "next") {
				return http.StatusForbidden, `{"error":{"errors":[{"reason":"forbidden"}]}}`
			}
			if empty {
				return http.StatusOK, `{"memberships":[]}`
			}
			if page == "" {
				return http.StatusOK, `{"memberships":[{"preferredMemberKey":{"id":"z@example.org"},"type":"USER"},{"preferredMemberKey":{"id":"nested@example.org"},"type":"GROUP"}],"nextPageToken":"next"}`
			}
			if page == "next" {
				return http.StatusOK, `{"memberships":[{"preferredMemberKey":{"id":"a@example.org"},"type":"USER"},{"name":"groups/id/memberships/no-key"}]}`
			}
		}
		t.Errorf("unexpected request: %s", request.URL)
		return http.StatusNotFound, `{}`
	})
	api := &googleGroupSyncTestAPI{categoryTestAPI: &categoryTestAPI{user: &model.User{Id: "caller", Email: "caller@example.org"}}}
	p := &Plugin{configuration: &configuration{
		GoogleGroupsSyncDomain: "example.org", GoogleGroupsSyncServiceAccountKey: key,
		GoogleGroupsSyncAdminEmail: "admin@example.org", GoogleGroupsSyncAdminUsers: []string{"caller"},
		GoogleGroupsSyncFallbackEmail: "fallback@example.org",
	}}
	p.SetAPI(api)
	args := &model.CommandArgs{UserId: "caller", Command: "/google group manage team get"}
	response, appErr := p.ExecuteCommand(nil, args)
	if appErr != nil || response == nil || response.ResponseType != model.CommandResponseTypeEphemeral {
		t.Fatalf("get response: %+v, %v", response, appErr)
	}
	if !reflect.DeepEqual(pages, []string{"", "next"}) {
		t.Fatalf("pages read: %v", pages)
	}
	for _, want := range []string{"team@example.org", "a@example.org", "nested@example.org", "z@example.org", "groups/id/memberships/no-key"} {
		if !strings.Contains(response.Text, want) {
			t.Errorf("missing %q: %s", want, response.Text)
		}
	}
	if strings.Index(response.Text, "a@example.org") > strings.Index(response.Text, "nested@example.org") ||
		strings.Index(response.Text, "nested@example.org") > strings.Index(response.Text, "z@example.org") {
		t.Errorf("members not sorted: %s", response.Text)
	}
	aliasResponse, aliasErr := p.ExecuteCommand(nil, &model.CommandArgs{UserId: "caller", Command: "/ggroup manage team get"})
	if aliasErr != nil || aliasResponse == nil || aliasResponse.Text != response.Text {
		t.Fatalf("alias get response: %+v, %v; original: %+v", aliasResponse, aliasErr, response)
	}
	denySecondPage = true
	response, appErr = p.ExecuteCommand(nil, args)
	if appErr != nil || !strings.Contains(response.Text, "may not have permission") || strings.Contains(response.Text, "z@example.org") {
		t.Fatalf("partial listing after page error: %+v, %v", response, appErr)
	}
	denySecondPage = false
	empty = true
	response, appErr = p.ExecuteCommand(nil, args)
	if appErr != nil || !strings.Contains(response.Text, "No direct members") {
		t.Fatalf("empty group: %+v, %v", response, appErr)
	}
	deny = true
	response, appErr = p.ExecuteCommand(nil, args)
	if appErr != nil || !strings.Contains(response.Text, "may not have permission") {
		t.Fatalf("permission denial: %+v, %v", response, appErr)
	}
	for _, command := range []string{
		"/google group manage", "/google group manage help", "/google group manage team",
		"/google group manage team get @target", "/google group manage team add",
		"/google group manage team remove", "/google group manage team unknown",
		"/google group manage add team @target", "/google group manage remove team @target",
	} {
		args.Command = command
		response, appErr = p.ExecuteCommand(nil, args)
		if appErr != nil || response == nil || !strings.Contains(response.Text, "Usage:") {
			t.Errorf("invalid command %q: %+v, %v", command, response, appErr)
		}
	}
}

func TestGoogleGroupManageCannotRemoveCaller(t *testing.T) {
	var targetGoogleID = "caller-google-id"
	var failActorLookup bool
	var writes int
	key := setupGoogleTestTransport(t, func(request *http.Request) (int, string) {
		if request.URL.Host == "oauth2.googleapis.com" {
			return http.StatusOK, `{"access_token":"token"}`
		}
		switch {
		case request.URL.Path == "/admin/directory/v1/users/caller@example.org":
			if failActorLookup {
				return http.StatusForbidden, `{"error":"forbidden"}`
			}
			return http.StatusOK, `{"id":"caller-google-id","primaryEmail":"caller@example.org"}`
		case request.URL.Path == "/admin/directory/v1/users/alias@example.org":
			return http.StatusOK, fmt.Sprintf(`{"id":%q,"primaryEmail":"caller@example.org"}`, targetGoogleID)
		default:
			t.Errorf("unexpected request %s %s", request.Method, request.URL)
			return http.StatusNotFound, `{}`
		}
	}, func(request *http.Request) (int, string) {
		switch request.URL.Path {
		case "/v1/groups:lookup":
			return http.StatusOK, `{"name":"groups/group-id"}`
		case "/v1/groups/group-id":
			return http.StatusOK, `{"name":"groups/group-id","groupKey":{"id":"team@example.org"}}`
		case "/v1/groups/group-id/memberships":
			return http.StatusOK, `{"memberships":[{"name":"groups/group-id/memberships/member-id","preferredMemberKey":{"id":"alias@example.org"},"type":"USER"}]}`
		default:
			return http.StatusNotFound, `{}`
		}
	})
	caller := &model.User{Id: "caller", Username: "caller", Email: "caller@example.org"}
	alias := &model.User{Id: "other", Username: "other", Email: "alias@example.org"}
	sameEmail := &model.User{Id: "same-email", Username: "same-email", Email: "CALLER@example.org"}
	api := &googleGroupSyncTestAPI{
		categoryTestAPI: &categoryTestAPI{kv: map[string][]byte{}, user: caller},
		usersByID:       map[string]*model.User{"other": alias},
		usersByName:     map[string]*model.User{"caller": caller, "other": alias, "same-email": sameEmail},
	}
	p := &Plugin{configuration: &configuration{GoogleGroupsSyncDomain: "example.org", GoogleGroupsSyncServiceAccountKey: key}}
	p.SetAPI(api)
	args := &model.CommandArgs{UserId: "caller", TeamId: "team", ChannelId: "channel", TriggerId: "trigger"}
	for _, username := range []string{"caller", "same-email"} {
		args.Command = "/google group manage team@example.org remove @" + username
		response, appErr := p.ExecuteCommand(nil, args)
		if appErr != nil || response == nil || !strings.Contains(response.Text, "cannot remove yourself") {
			t.Fatalf("self-removal preview for %s: %+v, %v", username, response, appErr)
		}
	}
	if _, err := p.applyGoogleGroupConfirmation(googleGroupConfirmation{
		Action: "manage_remove", UserID: "caller", MemberUserID: "caller", ActingEmail: caller.Email,
	}); err == nil || !strings.Contains(err.Error(), "cannot remove yourself") {
		t.Fatalf("previously opened self-removal confirmation accepted: %v", err)
	}

	args.Command = "/google group manage team@example.org remove @other"
	response, appErr := p.ExecuteCommand(nil, args)
	if appErr != nil || response == nil || !strings.Contains(response.Text, "confirm") {
		t.Fatalf("alias preview: %+v, %v", response, appErr)
	}
	var plan googleGroupConfirmation
	if err := json.Unmarshal(api.kv[googleGroupConfirmationPrefix+api.dialog.Dialog.State], &plan); err != nil {
		t.Fatal(err)
	}
	if _, err := p.applyGoogleGroupConfirmation(plan); err == nil || !strings.Contains(err.Error(), "cannot remove yourself") {
		t.Fatalf("same Google account via alias accepted: %v", err)
	}
	targetGoogleID = ""
	if _, err := p.applyGoogleGroupConfirmation(plan); err == nil || !strings.Contains(err.Error(), "cannot safely remove") {
		t.Fatalf("unverified Google account accepted: %v", err)
	}
	failActorLookup = true
	if _, err := p.applyGoogleGroupConfirmation(plan); err == nil || !strings.Contains(err.Error(), "verify Google account") {
		t.Fatalf("failed Google account lookup accepted: %v", err)
	}
	if writes != 0 {
		t.Fatalf("self-removal sent %d Google writes", writes)
	}
}

func TestGoogleChannelDelegationRevocationInvalidatesConfirmation(t *testing.T) {
	api := &googleGroupSyncTestAPI{categoryTestAPI: &categoryTestAPI{
		kv: map[string][]byte{}, user: &model.User{Id: "caller", Email: "caller@example.org"},
	}}
	config := &configuration{GoogleGroupsSyncDomain: "example.org", GoogleGroupsSyncAdminEmail: "admin@example.org", GoogleGroupsSyncAdminUsers: []string{"caller"}}
	p := &Plugin{configuration: config}
	p.SetAPI(api)
	plan := googleGroupConfirmation{Action: "channel_add", UserID: "caller", ActingEmail: "admin@example.org"}
	config.GoogleGroupsSyncAdminUsers = nil
	if _, err := p.applyGoogleGroupConfirmation(plan); err == nil || !strings.Contains(err.Error(), "delegated Google account changed") {
		t.Fatalf("revoked delegation accepted: %v", err)
	}
}

func TestGoogleChannelUsesSelectedAdminDelegation(t *testing.T) {
	var subject string
	key := setupGoogleTestTransport(t, func(request *http.Request) (int, string) {
		if request.URL.Host == "oauth2.googleapis.com" {
			if err := request.ParseForm(); err != nil {
				t.Fatal(err)
			}
			parts := strings.Split(request.Form.Get("assertion"), ".")
			if len(parts) != 3 {
				t.Fatal("invalid token assertion")
			}
			data, err := base64.RawURLEncoding.DecodeString(parts[1])
			if err != nil {
				t.Fatal(err)
			}
			var claims struct {
				Subject string `json:"sub"`
			}
			if err := json.Unmarshal(data, &claims); err != nil {
				t.Fatal(err)
			}
			subject = claims.Subject
			return http.StatusOK, `{"access_token":"token"}`
		}
		if subject != "admin@example.org" {
			t.Errorf("channel operation used wrong delegation: %s", subject)
		}
		if strings.HasSuffix(request.URL.Path, "/members") {
			return http.StatusOK, `{"members":[]}`
		}
		return http.StatusOK, `{"id":"id","email":"team@example.org"}`
	})
	api := &googleGroupSyncTestAPI{
		categoryTestAPI: &categoryTestAPI{
			kv: map[string][]byte{}, user: &model.User{Id: "caller", Email: "caller@example.org"},
			channel: model.Channel{Id: "channel", Name: "target", TeamId: "team", Type: model.ChannelTypeOpen},
		},
		channelMembers: map[string]*model.ChannelMember{"caller": {ChannelId: "channel", UserId: "caller"}},
	}
	p := &Plugin{configuration: &configuration{GoogleGroupsSyncDomain: "example.org", GoogleGroupsSyncAdminEmail: "admin@example.org", GoogleGroupsSyncAdminUsers: []string{"caller"}, GoogleGroupsSyncServiceAccountKey: key}}
	p.SetAPI(api)
	response, appErr := p.ExecuteCommand(nil, &model.CommandArgs{
		Command: "/google group add team@example.org", UserId: "caller", TeamId: "team", ChannelId: "channel", TriggerId: "trigger",
	})
	if appErr != nil || !strings.Contains(response.Text, "confirm") || subject != "admin@example.org" {
		t.Fatalf("admin preview failed: %+v %v, subject=%s", response, appErr, subject)
	}
	if !strings.Contains(api.dialog.Dialog.IntroductionText, "caller@example.org") ||
		strings.Contains(api.dialog.Dialog.IntroductionText, "admin@example.org") {
		t.Fatal("preview should identify the Mattermost user without exposing delegated account")
	}
	var plan googleGroupConfirmation
	if err := json.Unmarshal(api.kv[googleGroupConfirmationPrefix+api.dialog.Dialog.State], &plan); err != nil {
		t.Fatal(err)
	}
	if _, err := p.applyGoogleGroupConfirmation(plan); err != nil {
		t.Fatalf("admin channel confirmation failed: %v", err)
	}
}

func TestGoogleManageOnlyRecognizesDirectUserMembers(t *testing.T) {
	p := &Plugin{configuration: &configuration{GoogleGroupsSyncDomain: "example.org"}}
	members := []googleDirectoryGroupMember{{Email: "target@example.org", Type: "GROUP"}}
	if _, ok := p.googleGroupMember(members, "target@example.org"); ok {
		t.Fatal("group was treated as a user member")
	}
	members = append(members, googleDirectoryGroupMember{Email: "TARGET@example.org", Type: "USER"})
	if _, ok := p.googleGroupMember(members, "target@example.org"); !ok {
		t.Fatal("direct user was not recognized")
	}
}

func TestGoogleManageRecognizesAlternateDomainMembership(t *testing.T) {
	p := &Plugin{configuration: &configuration{
		GoogleGroupsSyncDomain: "example.org", GoogleGroupsSyncAlternateDomain: "alternate.example",
	}}
	members := []googleDirectoryGroupMember{
		{Email: "person@alternate.example", Type: "USER"},
	}
	member, ok := p.googleGroupMember(members, "person@example.org")
	if !ok || member.Email != "person@alternate.example" {
		t.Fatalf("alternate-domain member was not recognized: member=%+v found=%t", member, ok)
	}
}

func TestGoogleGroupAddUsesVerifiedUser(t *testing.T) {
	var addedEmail string
	key := setupGoogleTestTransport(t, func(request *http.Request) (int, string) {
		if request.URL.Host == "oauth2.googleapis.com" {
			return http.StatusOK, `{"access_token":"token"}`
		}
		switch {
		case request.URL.Path == "/admin/directory/v1/users/person@fysiksektionen.se":
			if request.URL.Query().Get("viewType") != "domain_public" {
				t.Error("user verification should use the domain-public view")
			}
			return http.StatusOK, `{"id":"user-id","primaryEmail":"person@fysiksektionen.se"}`
		default:
			t.Errorf("unexpected request %s %s", request.Method, request.URL)
			return http.StatusNotFound, `{}`
		}
	}, func(request *http.Request) (int, string) {
		switch {
		case request.URL.Path == "/v1/groups:lookup":
			return http.StatusOK, `{"name":"groups/target-id"}`
		case request.URL.Path == "/v1/groups/target-id":
			return http.StatusOK, `{"name":"groups/target-id","groupKey":{"id":"target@fysiksektionen.se"}}`
		case request.URL.Path == "/v1/groups/target-id/memberships" && request.Method == http.MethodGet:
			return http.StatusOK, `{"memberships":[]}`
		case request.URL.Path == "/v1/groups/target-id/memberships" && request.Method == http.MethodPost:
			var member struct {
				MemberKey struct {
					ID string `json:"id"`
				} `json:"preferredMemberKey"`
			}
			if err := json.NewDecoder(request.Body).Decode(&member); err != nil {
				t.Error(err)
			}
			addedEmail = member.MemberKey.ID
			return http.StatusOK, `{"name":"operations/add","done":true}`
		default:
			t.Errorf("unexpected Cloud Identity request %s %s", request.Method, request.URL)
			return http.StatusNotFound, `{}`
		}
	})
	base := &categoryTestAPI{kv: map[string][]byte{}, user: &model.User{Id: "requester", Email: "person@f.kth.se"}}
	p := &Plugin{configuration: &configuration{GoogleGroupsSyncDomain: "fysiksektionen.se", GoogleGroupsSyncAlternateDomain: "f.kth.se", GoogleGroupsSyncServiceAccountKey: key}}
	p.SetAPI(&googleGroupSyncTestAPI{categoryTestAPI: base})
	snapshot, err := p.googleGroupSnapshot("target@fysiksektionen.se", "person@fysiksektionen.se", false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.applyGoogleGroupConfirmation(googleGroupConfirmation{
		Action: "manage_add", UserID: "requester", TeamID: "team", GroupEmail: "target@fysiksektionen.se",
		MemberEmail: "person@fysiksektionen.se", MemberUserID: "requester", ActingEmail: "person@fysiksektionen.se", SnapshotHash: googleGroupSnapshotHash(snapshot),
	})
	if err != nil || addedEmail != "person@fysiksektionen.se" {
		t.Fatalf("verified user was not added: err=%v email=%q", err, addedEmail)
	}
}
