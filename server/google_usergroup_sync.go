package main

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

type googleUsergroupSync struct {
	Usergroup     string   `json:"usergroup"`
	GroupEmail    string   `json:"group_email"`
	ExcludedUsers []string `json:"excluded_users"`
}

func validateGoogleUsergroupSyncs(syncs []googleUsergroupSync) error {
	seen := make(map[string]bool)
	for i, sync := range syncs {
		name, ok := strings.CutPrefix(sync.Usergroup, "@")
		if !ok || name == "" || (&model.Group{Name: &name}).IsValidName() != nil {
			return fmt.Errorf("entry %d: specify a valid @usergroup", i+1)
		}
		if seen[name] {
			return fmt.Errorf("entry %d: duplicate usergroup %s", i+1, sync.Usergroup)
		}
		seen[name] = true
		if err := validateFysiksektionenEmail(sync.GroupEmail); err != nil {
			return fmt.Errorf("entry %d: %w", i+1, err)
		}
		for _, userID := range sync.ExcludedUsers {
			if !model.IsValidId(userID) {
				return fmt.Errorf("entry %d: invalid excluded user ID", i+1)
			}
		}
	}
	return nil
}

func (p *Plugin) runGoogleUsergroupSyncs(ctx context.Context) {
	config := p.getConfiguration()
	if len(config.GoogleGroupsUsergroupSyncs) == 0 {
		return
	}
	if err := validateGoogleUsergroupSyncs(config.GoogleGroupsUsergroupSyncs); err != nil {
		p.API.LogError("Invalid Google usergroup sync configuration", "error", err.Error())
		return
	}
	adminEmail := strings.TrimSpace(config.GoogleGroupsSyncAdminEmail)
	normalizedAdmin, ok := p.googleOrganizationEmail(adminEmail)
	if !ok || !strings.EqualFold(adminEmail, normalizedAdmin) {
		p.API.LogError("Google usergroup sync requires a valid admin email")
		return
	}
	for _, sync := range config.GoogleGroupsUsergroupSyncs {
		if ctx.Err() != nil || p.getConfiguration() != config {
			return
		}
		if err := p.syncGoogleUsergroup(ctx, config, sync, adminEmail); err != nil {
			p.API.LogError("Google usergroup sync failed", "usergroup", sync.Usergroup, "group", sync.GroupEmail, "error", err.Error())
		}
	}
}

func (p *Plugin) syncGoogleUsergroup(ctx context.Context, config *configuration, sync googleUsergroupSync, adminEmail string) error {
	group, appErr := p.API.GetGroupByName(strings.TrimPrefix(sync.Usergroup, "@"))
	if appErr != nil {
		return fmt.Errorf("find usergroup: %s", appErr.Error())
	}
	if group == nil || group.Id == "" || group.DeleteAt != 0 {
		return fmt.Errorf("usergroup is unavailable")
	}
	snapshot, err := p.googleGroupSnapshot(sync.GroupEmail, adminEmail, true)
	if err != nil {
		return fmt.Errorf("read Google group: %w", err)
	}
	desired := make(map[string]bool)
	for _, member := range snapshot.Members {
		if member.Type == "" || (strings.EqualFold(member.Type, "USER") && member.Email == "") {
			return fmt.Errorf("Google group returned an invalid member")
		}
		if !strings.EqualFold(member.Type, "USER") || p.isGoogleFallbackEmail(member.Email) {
			continue
		}
		user, lookupErr := p.googleGroupMattermostUser(member.Email)
		if lookupErr != nil {
			if lookupErr.StatusCode == http.StatusNotFound {
				continue
			}
			return fmt.Errorf("find Mattermost user %q: %s", member.Email, lookupErr.Error())
		}
		if user == nil || user.Id == "" {
			return fmt.Errorf("invalid Mattermost user for %q", member.Email)
		}
		if user.DeleteAt == 0 && !slices.Contains(sync.ExcludedUsers, user.Id) {
			desired[user.Id] = true
		}
	}
	existing := make(map[string]bool)
	const pageSize = 200
	for page := 0; ; page++ {
		users, listErr := p.API.GetGroupMemberUsers(group.Id, page, pageSize)
		if listErr != nil {
			return fmt.Errorf("list usergroup members: %s", listErr.Error())
		}
		for _, user := range users {
			if user == nil || user.Id == "" {
				return fmt.Errorf("invalid usergroup member")
			}
			existing[user.Id] = true
		}
		if len(users) < pageSize {
			break
		}
	}
	checkActive := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if p.getConfiguration() != config {
			return fmt.Errorf("configuration changed during sync")
		}
		return nil
	}
	for userID := range desired {
		if existing[userID] {
			continue
		}
		if err := checkActive(); err != nil {
			return err
		}
		if _, addErr := p.API.UpsertGroupMember(group.Id, userID); addErr != nil {
			return fmt.Errorf("add user %s: %s", userID, addErr.Error())
		}
	}
	for userID := range existing {
		if desired[userID] {
			continue
		}
		if err := checkActive(); err != nil {
			return err
		}
		if _, removeErr := p.API.DeleteGroupMember(group.Id, userID); removeErr != nil {
			return fmt.Errorf("remove user %s: %s", userID, removeErr.Error())
		}
	}
	return nil
}
