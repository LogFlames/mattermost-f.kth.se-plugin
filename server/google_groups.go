package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

const (
	googleGroupSyncPrefix          = "google_group_sync_"
	googleGroupContinuousStateKey  = "google_continuous_sync_state"
	googleGroupConfirmationPrefix  = "google_group_confirmation_"
	googleGroupConfirmationID      = "google_group_confirmation"
	googleGroupSyncEvery           = time.Hour
	googleGroupSyncMaxResults      = 200
	googleGroupSyncDisplayTimeZone = "Europe/Stockholm"
	googleCloudIdentityURL         = "https://cloudidentity.googleapis.com/v1"
	googleCloudIdentityReadScope   = "https://www.googleapis.com/auth/cloud-identity.groups.readonly"
	googleCloudIdentityWriteScope  = "https://www.googleapis.com/auth/cloud-identity.groups"
)

type googleDirectoryGroup struct {
	ID                 string `json:"id"`
	ResourceName       string `json:"resourceName,omitempty"`
	Email              string `json:"email"`
	Name               string `json:"name"`
	DirectMembersCount string `json:"directMembersCount"`
}

type googleDirectoryGroupMember struct {
	ID                 string `json:"id"`
	Name               string `json:"name,omitempty"`
	Email              string `json:"email"`
	Role               string `json:"role"`
	Type               string `json:"type"`
	Status             string `json:"status"`
	PreferredMemberKey struct {
		ID string `json:"id"`
	} `json:"preferredMemberKey,omitempty"`
}

type googleDirectoryGroupSnapshot struct {
	Group   googleDirectoryGroup
	Members []googleDirectoryGroupMember
}

type googleGroupSync struct {
	GroupEmail     string `json:"group_email"`
	GroupName      string `json:"group_name"`
	TeamID         string `json:"team_id"`
	ChannelID      string `json:"channel_id"`
	CreatedBy      string `json:"created_by"`
	ActingEmail    string `json:"acting_email"`
	RequesterEmail string `json:"requester_email,omitempty"`
	DelegatedEmail string `json:"delegated_email,omitempty"`
	AdminFallback  bool   `json:"admin_fallback,omitempty"`
	DeadlineAt     int64  `json:"deadline_at"`
	LastSyncedAt   int64  `json:"last_synced_at"`
}

type googleGroupSyncRecord struct {
	Key  string
	Data []byte
	Job  googleGroupSync
}

type googleGroupSyncPreview struct {
	Key          string `json:"key"`
	RecordHash   string `json:"record_hash"`
	SnapshotHash string `json:"snapshot_hash"`
	ActingEmail  string `json:"acting_email,omitempty"`
}

type googleGroupConfirmation struct {
	Action           string                   `json:"action"`
	UserID           string                   `json:"user_id"`
	RequesterEmail   string                   `json:"requester_email,omitempty"`
	TeamID           string                   `json:"team_id"`
	ChannelID        string                   `json:"channel_id"`
	TargetTeam       bool                     `json:"target_team,omitempty"`
	AlsoInviteToTeam bool                     `json:"also_invite_to_team,omitempty"`
	GroupEmail       string                   `json:"group_email"`
	MemberEmail      string                   `json:"member_email,omitempty"`
	MemberUserID     string                   `json:"member_user_id,omitempty"`
	ActingEmail      string                   `json:"acting_email"`
	SnapshotEmail    string                   `json:"snapshot_email,omitempty"`
	DeadlineAt       int64                    `json:"deadline_at,omitempty"`
	SnapshotHash     string                   `json:"snapshot_hash"`
	ExpiresAt        int64                    `json:"expires_at"`
	TargetChannelID  string                   `json:"target_channel_id,omitempty"`
	SyncPreviews     []googleGroupSyncPreview `json:"sync_previews,omitempty"`
}

func googleUserFacingError(err error) string {
	var accessErr *googleGroupAccessError
	if errors.As(err, &accessErr) {
		return accessErr.operation + ": " + googleUserFacingError(accessErr.err)
	}
	var tokenErr *googleTokenError
	if errors.As(err, &tokenErr) {
		if tokenErr.status == http.StatusBadRequest && tokenErr.code == "invalid_grant" &&
			strings.EqualFold(strings.TrimSpace(tokenErr.description), "Invalid email or User ID") {
			return "Google could not find the account used for this operation. Check your Mattermost email or ask an administrator to check the delegated Google account."
		}
		if (tokenErr.status == http.StatusBadRequest || tokenErr.status == http.StatusUnauthorized) && tokenErr.code == "unauthorized_client" {
			return "Google did not authorize the service account. Ask an administrator to check domain-wide delegation and the required API scopes."
		}
	}

	if googlePermissionDenied(err) {
		return "Google denied this operation. The delegated Google account may not have permission to access or change this group."
	}
	return err.Error()
}

func googlePermissionDenied(err error) bool {
	var apiErr *googleAPIError
	if !errors.As(err, &apiErr) || apiErr.status != http.StatusForbidden {
		return false
	}
	var details struct {
		Error struct {
			Status string `json:"status"`
			Errors []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(apiErr.body), &details) != nil {
		return false
	}
	for _, item := range details.Error.Errors {
		if item.Reason == "forbidden" || item.Reason == "insufficientPermissions" {
			return true
		}
	}
	return len(details.Error.Errors) == 0 && details.Error.Status == "PERMISSION_DENIED"
}

func (p *Plugin) googleGroupCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	fields := strings.Fields(args.Command)
	if len(fields) < 3 || fields[1] != "group" {
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: "Usage: `/google group add|remove|team|sync|manage ...`"}, nil
	}
	switch fields[2] {
	case "sync":
		return p.googleGroupSyncCommand(args)
	case "team":
		return p.googleGroupTeamCommand(args)
	case "manage":
		return p.googleGroupMemberCommand(args)
	default:
		return p.googleGroupChannelCommand(args)
	}
}

func (p *Plugin) googleGroupSyncCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	return p.googleGroupSyncCommandForTarget(args, false)
}

func (p *Plugin) googleGroupTeamCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	fields := strings.Fields(args.Command)
	if len(fields) < 4 {
		response.Text = "Usage: `/google group team add|remove|sync ...`"
		return response, nil
	}
	if !p.googleGroupTeamAdmin(args.UserId, args.TeamId) {
		response.Text = "Only system or team admins can manage Google Groups for a team."
		return response, nil
	}
	switch fields[3] {
	case "add", "remove":
		return p.googleGroupTeamMemberCommand(args, fields[3])
	case "sync":
		targetArgs := *args
		targetArgs.Command = "/google group sync " + strings.Join(fields[4:], " ")
		return p.googleGroupSyncCommandForTarget(&targetArgs, true)
	default:
		response.Text = "Usage: `/google group team add|remove|sync ...`"
		return response, nil
	}
}

func (p *Plugin) googleGroupSyncCommandForTarget(args *model.CommandArgs, teamTarget bool) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	if !p.googleGroupTeamAdmin(args.UserId, args.TeamId) {
		response.Text = "Only system or team admins can manage Google Group syncs."
		return response, nil
	}
	fields := strings.Fields(args.Command)
	usagePrefix := "/google group sync"
	if teamTarget {
		usagePrefix = "/google group team sync"
	}
	if len(fields) < 4 {
		response.Text = googleGroupSyncUsage(teamTarget,
			fmt.Sprintf("Usage: `%s start <group-email> <YYYY-MM-DD> / stop <group-email> / list`", usagePrefix))
		return response, nil
	}

	switch fields[3] {
	case "now":
		if teamTarget {
			response.Text = googleGroupSyncUsage(true,
				"Usage: `/google group team sync start <group-email> <YYYY-MM-DD> / stop <group-email> / list`")
			return response, nil
		}
		if len(fields) != 4 {
			response.Text = "Usage: `/google group sync now`"
			return response, nil
		}
		return p.googleGroupSyncNow(args)
	case "list":
		if len(fields) != 4 {
			response.Text = googleGroupSyncUsage(teamTarget, fmt.Sprintf("Usage: `%s list`", usagePrefix))
			return response, nil
		}
		return p.listGoogleGroupSyncs(args)
	case "start":
		alsoInviteToTeam := len(fields) == 7 && fields[6] == "--also-invite-to-team"
		if (len(fields) != 6 && !alsoInviteToTeam) || (teamTarget && len(fields) != 6) {
			flagHint := " [--also-invite-to-team]"
			if teamTarget {
				flagHint = ""
			}
			response.Text = googleGroupSyncUsage(teamTarget,
				fmt.Sprintf("Usage: `%s start <group-email> <YYYY-MM-DD>%s`", usagePrefix, flagHint))
			return response, nil
		}
		groupEmail, err := p.normalizeGoogleGroupEmail(fields[4])
		if err != nil {
			response.Text = err.Error()
			return response, nil
		}
		deadline, err := parseGoogleGroupDeadline(fields[5], time.Now())
		if err != nil {
			response.Text = err.Error()
			return response, nil
		}
		if message := p.googleGroupSyncDeadlinePermissionMessage(args.UserId, deadline, time.Now()); message != "" {
			response.Text = message
			return response, nil
		}
		channel, appErr := p.googleGroupSyncChannel(args)
		if appErr != nil {
			return nil, appErr
		}
		if channel == nil {
			response.Text = "Run this command in an active public or private team channel."
			return response, nil
		}
		if err := p.requireGoogleSyncChannelMember(channel.Id, args.UserId); err != nil {
			response.Text = err.Error()
			return response, nil
		}
		if teamTarget || alsoInviteToTeam {
			if message := p.googleGroupTeamPermissionMessage(args.UserId, args.TeamId, model.PermissionAddUserToTeam, "add"); message != "" {
				response.Text = message
				return response, nil
			}
		}
		actorEmail, err := p.googleGroupActingEmail(args.UserId)
		if err != nil {
			response.Text = err.Error()
			return response, nil
		}
		snapshot, snapshotEmail, err := p.googleGroupChannelSnapshot(groupEmail, actorEmail)
		if err != nil {
			response.Text = fmt.Sprintf("Could not read Google Group %q: %s", groupEmail, googleUserFacingError(err))
			return response, nil
		}
		if message := p.googleGroupChannelPermissionMessage(args.UserId, args.TeamId, len(snapshot.Members)); message != "" {
			response.Text = message
			return response, nil
		}
		channelKey := googleGroupSyncKey(args.TeamId, channel.Id, groupEmail)
		teamKey := googleGroupSyncKey(args.TeamId, "", groupEmail)
		keys := []string{channelKey}
		if teamTarget {
			keys = []string{teamKey}
		} else if alsoInviteToTeam {
			keys = append(keys, teamKey)
		}
		for _, key := range keys {
			data, appErr := p.API.KVGet(key)
			if appErr != nil {
				return nil, appErr
			}
			if len(data) != 0 {
				response.Text = "This Google Group already has a sync configured for that target. Stop it first to replace its deadline."
				return response, nil
			}
		}
		plan := googleGroupConfirmation{
			Action: "sync_add", UserID: args.UserId, TeamID: args.TeamId, ChannelID: args.ChannelId,
			TargetTeam: teamTarget, AlsoInviteToTeam: alsoInviteToTeam,
			GroupEmail: groupEmail, ActingEmail: actorEmail, SnapshotEmail: snapshotEmail, DeadlineAt: deadline,
			SnapshotHash: googleGroupSnapshotHash(snapshot),
		}
		requesterEmail, err := p.googleGroupOwnActingEmail(args.UserId)
		if err != nil {
			response.Text = err.Error()
			return response, nil
		}
		plan.RequesterEmail = requesterEmail
		action := fmt.Sprintf("Add this group to the channel and sync it hourly through %s (UTC)?", time.UnixMilli(deadline).UTC().Format("2006-01-02"))
		confirmationChannelName := channel.Name
		if teamTarget {
			action = fmt.Sprintf("Add this group to the team and sync it hourly through %s (UTC)?", time.UnixMilli(deadline).UTC().Format("2006-01-02"))
			confirmationChannelName = ""
		} else if alsoInviteToTeam {
			action = fmt.Sprintf("Add this group to the channel and team and sync it hourly through %s (UTC)?", time.UnixMilli(deadline).UTC().Format("2006-01-02"))
		}
		return p.openGoogleGroupConfirmation(args, plan, snapshot, confirmationChannelName, action)
	case "stop":
		if len(fields) != 5 {
			response.Text = googleGroupSyncUsage(teamTarget, fmt.Sprintf("Usage: `%s stop <group-email>`", usagePrefix))
			return response, nil
		}
		groupEmail, err := p.normalizeGoogleGroupEmail(fields[4])
		if err != nil {
			response.Text = err.Error()
			return response, nil
		}
		channel, appErr := p.googleGroupSyncChannel(args)
		if appErr != nil {
			return nil, appErr
		}
		if channel == nil {
			response.Text = "Run this command in an active public or private team channel."
			return response, nil
		}
		if err := p.requireGoogleSyncChannelMember(channel.Id, args.UserId); err != nil {
			response.Text = err.Error()
			return response, nil
		}
		if teamTarget {
			if message := p.googleGroupTeamPermissionMessage(args.UserId, args.TeamId, model.PermissionAddUserToTeam, "add"); message != "" {
				response.Text = message
				return response, nil
			}
		}
		key := googleGroupSyncKey(args.TeamId, args.ChannelId, groupEmail)
		if teamTarget {
			key = googleGroupSyncKey(args.TeamId, "", groupEmail)
		}
		data, appErr := p.API.KVGet(key)
		if appErr != nil {
			return nil, appErr
		}
		if len(data) == 0 {
			target := "channel"
			if teamTarget {
				target = "team"
			}
			response.Text = fmt.Sprintf("Google Group %q does not have a sync configured for this %s.", groupEmail, target)
			return response, nil
		}
		actorEmail, err := p.googleGroupActingEmail(args.UserId)
		if err != nil {
			response.Text = err.Error()
			return response, nil
		}
		snapshot, snapshotEmail, err := p.googleGroupChannelSnapshot(groupEmail, actorEmail)
		if err != nil {
			response.Text = fmt.Sprintf("Could not read Google Group %q: %s", groupEmail, googleUserFacingError(err))
			return response, nil
		}
		if message := p.googleGroupChannelPermissionMessage(args.UserId, args.TeamId, len(snapshot.Members)); message != "" {
			response.Text = message
			return response, nil
		}
		plan := googleGroupConfirmation{
			Action: "sync_remove", UserID: args.UserId, TeamID: args.TeamId, ChannelID: args.ChannelId,
			TargetTeam: teamTarget,
			GroupEmail: groupEmail, ActingEmail: actorEmail, SnapshotEmail: snapshotEmail, SnapshotHash: googleGroupSnapshotHash(snapshot),
		}
		if err := json.Unmarshal(data, new(googleGroupSync)); err != nil {
			return nil, model.NewAppError("googleGroupSyncCommand", "plugin.google_groups.sync", nil, err.Error(), http.StatusInternalServerError)
		}
		requesterEmail, err := p.googleGroupMattermostEmail(args.UserId)
		if err != nil {
			response.Text = err.Error()
			return response, nil
		}
		plan.RequesterEmail = requesterEmail
		targetDescription := "channel"
		confirmationChannelName := channel.Name
		if teamTarget {
			targetDescription = "team"
			confirmationChannelName = ""
		}
		return p.openGoogleGroupConfirmation(args, plan, snapshot, confirmationChannelName,
			"Stop syncing this group to the "+targetDescription+"? Existing members will not be removed.")
	default:
		response.Text = googleGroupSyncUsage(teamTarget,
			fmt.Sprintf("Usage: `%s start <group-email> <YYYY-MM-DD> / stop <group-email> / list`", usagePrefix))
		return response, nil
	}
}

func googleGroupSyncUsage(teamTarget bool, detail string) string {
	if !teamTarget {
		return detail
	}
	return "Usage: `/google group team sync start|stop|list`\n" + detail
}

func (p *Plugin) googleGroupChannelCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	fields := strings.Fields(args.Command)
	if len(fields) != 4 || (fields[2] != "add" && fields[2] != "remove") {
		response.Text = "Usage: `/google group add|remove <group-email>`"
		return response, nil
	}
	groupEmail, err := p.normalizeGoogleGroupEmail(fields[3])
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	channel, appErr := p.googleGroupSyncChannel(args)
	if appErr != nil {
		return nil, appErr
	}
	if channel == nil {
		response.Text = "Run this command in an active public or private team channel."
		return response, nil
	}
	if err := p.requireGoogleSyncChannelMember(channel.Id, args.UserId); err != nil {
		response.Text = err.Error()
		return response, nil
	}
	actingEmail, err := p.googleGroupActingEmail(args.UserId)
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	snapshot, snapshotEmail, err := p.googleGroupChannelSnapshot(groupEmail, actingEmail)
	if err != nil {
		response.Text = fmt.Sprintf("Could not read Google Group %q: %s", groupEmail, googleUserFacingError(err))
		return response, nil
	}
	if message := p.googleGroupChannelPermissionMessage(args.UserId, args.TeamId, len(snapshot.Members)); message != "" {
		response.Text = message
		return response, nil
	}
	plan := googleGroupConfirmation{
		Action: "channel_" + fields[2], UserID: args.UserId, TeamID: args.TeamId, ChannelID: channel.Id,
		GroupEmail: groupEmail, ActingEmail: actingEmail, SnapshotEmail: snapshotEmail, SnapshotHash: googleGroupSnapshotHash(snapshot),
	}
	description := "Add this group's users to the channel once, without configuring a sync?"
	if fields[2] == "remove" {
		description = "Remove this group's users from the channel once? You will not be removed; active syncs may add members again."
	}
	return p.openGoogleGroupConfirmation(args, plan, snapshot, channel.Name, description)
}

func (p *Plugin) googleGroupTeamMemberCommand(args *model.CommandArgs, action string) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	fields := strings.Fields(args.Command)
	if len(fields) != 5 {
		response.Text = "Usage: `/google group team add|remove <group-email>`"
		return response, nil
	}
	groupEmail, err := p.normalizeGoogleGroupEmail(fields[4])
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	permission, actionDescription := model.PermissionAddUserToTeam, "add"
	if action == "remove" {
		permission, actionDescription = model.PermissionRemoveUserFromTeam, "remove"
	}
	if message := p.googleGroupTeamPermissionMessage(args.UserId, args.TeamId, permission, actionDescription); message != "" {
		response.Text = message
		return response, nil
	}
	channel, appErr := p.googleGroupSyncChannel(args)
	if appErr != nil {
		return nil, appErr
	}
	if channel == nil {
		response.Text = "Run this command in an active public or private team channel."
		return response, nil
	}
	if err := p.requireGoogleSyncChannelMember(channel.Id, args.UserId); err != nil {
		response.Text = err.Error()
		return response, nil
	}
	actingEmail, err := p.googleGroupActingEmail(args.UserId)
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	snapshot, snapshotEmail, err := p.googleGroupChannelSnapshot(groupEmail, actingEmail)
	if err != nil {
		response.Text = fmt.Sprintf("Could not read Google Group %q: %s", groupEmail, googleUserFacingError(err))
		return response, nil
	}
	if message := p.googleGroupChannelPermissionMessage(args.UserId, args.TeamId, len(snapshot.Members)); message != "" {
		response.Text = message
		return response, nil
	}
	plan := googleGroupConfirmation{
		Action: "team_" + action, UserID: args.UserId, TeamID: args.TeamId, ChannelID: channel.Id,
		GroupEmail: groupEmail, ActingEmail: actingEmail, SnapshotEmail: snapshotEmail,
		SnapshotHash: googleGroupSnapshotHash(snapshot),
	}
	description := "Add this group's users to the team once, without configuring a sync?"
	if action == "remove" {
		description = "Remove this group's users from the team once? Active syncs may add them again."
	}
	return p.openGoogleGroupConfirmation(args, plan, snapshot, "", description)
}

func (p *Plugin) googleGroupMemberCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	fields := strings.Fields(args.Command)
	if len(fields) < 5 || (fields[4] == "get" && len(fields) != 5) ||
		((fields[4] == "add" || fields[4] == "remove") && len(fields) != 6) ||
		(fields[4] != "get" && fields[4] != "add" && fields[4] != "remove") {
		response.Text = "Usage: `/google group manage <group-email> get / <group-email> add/remove @<username>`"
		return response, nil
	}
	groupEmail, err := p.normalizeGoogleGroupEmail(fields[3])
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	actingEmail, err := p.googleGroupOwnActingEmail(args.UserId)
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	if fields[4] == "get" {
		snapshot, err := p.googleGroupSnapshot(groupEmail, actingEmail, false)
		if err != nil {
			response.Text = fmt.Sprintf("Could not read Google Group %q: %s", groupEmail, googleUserFacingError(err))
			return response, nil
		}
		lines := make([]string, 0, len(snapshot.Members))
		for _, member := range snapshot.Members {
			identifier := member.Email
			if identifier == "" {
				identifier = member.Name
			}
			if identifier == "" {
				lines = append(lines, "- _Member without an identifier_")
			} else {
				lines = append(lines, "- `"+strings.ReplaceAll(identifier, "`", "'")+"`")
			}
		}
		if len(lines) == 0 {
			lines = append(lines, "_No direct members_")
		}
		response.Text = fmt.Sprintf("#### Direct members of `%s`\n\n%s", strings.ReplaceAll(snapshot.Group.Email, "`", "'"), strings.Join(lines, "\n"))
		return response, nil
	}
	user, err := p.migrationMattermostUser(fields[5])
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	memberEmail, ok := p.googleOrganizationEmail(user.Email)
	if user.DeleteAt != 0 || user.IsBot || !ok || p.isGoogleFallbackEmail(memberEmail) {
		response.Text = "The selected user must have an active non-fallback Google Workspace account in the configured domain."
		return response, nil
	}
	if fields[4] == "remove" && (user.Id == args.UserId || strings.EqualFold(memberEmail, actingEmail)) {
		response.Text = "You cannot remove yourself from a Google Group with this command."
		return response, nil
	}
	snapshot, err := p.googleGroupSnapshot(groupEmail, actingEmail, false)
	if err != nil {
		response.Text = fmt.Sprintf("Could not read Google Group %q: %s", groupEmail, googleUserFacingError(err))
		return response, nil
	}
	_, isMember := p.googleGroupMember(snapshot.Members, memberEmail)
	if fields[4] == "add" && isMember {
		response.Text = fmt.Sprintf("%s is already a member of %s.", memberEmail, googleGroupName(snapshot.Group))
		return response, nil
	}
	if fields[4] == "remove" && !isMember {
		response.Text = fmt.Sprintf("%s is not a direct user member of %s.", memberEmail, googleGroupName(snapshot.Group))
		return response, nil
	}
	plan := googleGroupConfirmation{
		Action: "manage_" + fields[4], UserID: args.UserId, TeamID: args.TeamId, ChannelID: args.ChannelId,
		GroupEmail: groupEmail, MemberEmail: memberEmail, MemberUserID: user.Id, ActingEmail: actingEmail,
		SnapshotHash: googleGroupSnapshotHash(snapshot),
	}
	description := fmt.Sprintf("Add `%s` to this Google Group?", memberEmail)
	if fields[4] == "remove" {
		description = fmt.Sprintf("Remove `%s` from this Google Group?", memberEmail)
	}
	return p.openGoogleGroupConfirmation(args, plan, snapshot, "", description)
}

func (p *Plugin) listGoogleGroupSyncs(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	channel, appErr := p.googleGroupSyncChannel(args)
	if appErr != nil {
		return nil, appErr
	}
	if channel == nil {
		response.Text = "Run this command in an active public or private team channel."
		return response, nil
	}
	if err := p.requireGoogleSyncChannelMember(channel.Id, args.UserId); err != nil {
		response.Text = err.Error()
		return response, nil
	}

	displayLocation, err := time.LoadLocation(googleGroupSyncDisplayTimeZone)
	if err != nil {
		return nil, model.NewAppError("listGoogleGroupSyncs", "plugin.google_groups.list", nil,
			fmt.Sprintf("load time zone %q: %v", googleGroupSyncDisplayTimeZone, err), http.StatusInternalServerError)
	}

	jobs, err := p.googleGroupSyncRecords()
	if err != nil {
		return nil, model.NewAppError("listGoogleGroupSyncs", "plugin.google_groups.list", nil, err.Error(), http.StatusInternalServerError)
	}
	sort.Slice(jobs, func(i, j int) bool {
		iTeamSync, jTeamSync := jobs[i].Job.ChannelID == "", jobs[j].Job.ChannelID == ""
		if iTeamSync != jTeamSync {
			return iTeamSync
		}
		iName, jName := jobs[i].Job.GroupName, jobs[j].Job.GroupName
		if iName == "" {
			iName = jobs[i].Job.GroupEmail
		}
		if jName == "" {
			jName = jobs[j].Job.GroupEmail
		}
		if !strings.EqualFold(iName, jName) {
			return strings.ToLower(iName) < strings.ToLower(jName)
		}
		return strings.ToLower(jobs[i].Job.GroupEmail) < strings.ToLower(jobs[j].Job.GroupEmail)
	})
	var lines []string
	creatorNames := make(map[string]string)
	now := time.Now()
	config := p.getConfiguration()
	cycle, cycleSeconds, err := googleGroupSyncCycleDescription(jobs, config, now)
	if err != nil {
		return nil, model.NewAppError("listGoogleGroupSyncs", "plugin.google_groups.list", nil, err.Error(), http.StatusInternalServerError)
	}
	actingEmail, err := p.googleGroupActingEmail(args.UserId)
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	for _, record := range jobs {
		job := record.Job
		teamSync := job.ChannelID == ""
		if job.TeamID != args.TeamId || (!teamSync && job.ChannelID != channel.Id) || job.DeadlineAt <= now.UnixMilli() {
			continue
		}
		if _, _, err := p.googleGroupChannelSnapshot(job.GroupEmail, actingEmail); err != nil {
			p.API.LogError("Verify Google Group access for sync list failed", "group", job.GroupEmail, "user_id", args.UserId, "error", err.Error())
			response.Text = "Could not verify access to a configured Google Group; no syncs were listed. Check Google permissions and server logs."
			return response, nil
		}
		creatorName, ok := creatorNames[job.CreatedBy]
		if !ok {
			creatorName = "@unknown"
			if job.CreatedBy != "" {
				creator, appErr := p.API.GetUser(job.CreatedBy)
				if appErr != nil && appErr.StatusCode != http.StatusNotFound {
					return nil, appErr
				}
				if appErr == nil && creator != nil && creator.Username != "" {
					creatorName = "@" + creator.Username
				}
			}
			creatorNames[job.CreatedBy] = creatorName
		}
		name := job.GroupName
		if name == "" {
			name = job.GroupEmail
		}
		target := "Channel"
		if teamSync {
			target = "Team"
		}
		account := strings.ReplaceAll(job.RequesterEmail, "`", "'")
		if account == "" {
			account = "unknown"
		}
		lastSynced := "First sync pending"
		if job.LastSyncedAt > 0 {
			lastSynced = fmt.Sprintf("Last synced: %s", formatGoogleGroupSyncTime(job.LastSyncedAt, displayLocation))
		}
		line := fmt.Sprintf("- **%s sync: %s** (`%s`)\n  - Started by: %s\n  - Scheduled Google account: `%s`\n  - Expires: %s UTC\n  - %s\n  - %s",
			target, strings.ReplaceAll(name, "`", "'"), strings.ReplaceAll(job.GroupEmail, "`", "'"),
			creatorName, account, time.UnixMilli(job.DeadlineAt).UTC().Format("2006-01-02"),
			lastSynced, googleGroupSyncNextDescriptionForMode(job, config, cycleSeconds, now, displayLocation))
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		response.Text = "No active Google Group syncs are configured for this team or channel."
	} else {
		response.Text = "#### Google Group syncs for `~" + strings.ReplaceAll(channel.Name, "`", "'") + "` and this team\n\n" + cycle + "\n\n" + strings.Join(lines, "\n\n")
	}
	return response, nil
}

func formatGoogleGroupSyncTime(timestamp int64, location *time.Location) string {
	return time.UnixMilli(timestamp).In(location).Format("2006-01-02 15:04 MST")
}

func googleGroupNextSyncAt(job googleGroupSync, now time.Time) int64 {
	if job.LastSyncedAt <= 0 || job.DeadlineAt <= now.UnixMilli() {
		return 0
	}
	lastSynced := time.UnixMilli(job.LastSyncedAt).UTC()
	nextSync := time.Date(lastSynced.Year(), lastSynced.Month(), lastSynced.Day(), lastSynced.Hour(), 23, 0, 0, time.UTC)
	if !nextSync.After(lastSynced) {
		nextSync = nextSync.Add(time.Hour)
	}
	nextSyncAt := nextSync.UnixMilli()
	if nextSyncAt >= job.DeadlineAt {
		return 0
	}
	return nextSyncAt
}

func nextGoogleGroupSyncRun(now time.Time) time.Time {
	now = now.UTC()
	nextRun := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 23, 0, 0, time.UTC)
	if !nextRun.After(now) {
		nextRun = nextRun.Add(time.Hour)
	}
	return nextRun
}

func googleGroupSyncNextDescription(job googleGroupSync, now time.Time, location *time.Location) string {
	nextSyncAt := googleGroupNextSyncAt(job, now)
	if nextSyncAt > 0 {
		return fmt.Sprintf("Next sync: %s", formatGoogleGroupSyncTime(nextSyncAt, location))
	}
	if job.DeadlineAt <= now.UnixMilli() {
		return "No next sync (expired)"
	}
	if job.LastSyncedAt <= 0 {
		return "Next sync pending (first sync)"
	}
	return "No next sync before deadline"
}

func googleGroupSyncNextDescriptionForMode(job googleGroupSync, config *configuration, cycleSeconds int64, now time.Time, location *time.Location) string {
	if config.GoogleGroupsSyncMode == "continuous" && job.DeadlineAt > now.UnixMilli() {
		minutes := cycleSeconds / 60
		if cycleSeconds%60 != 0 {
			minutes++
		}
		return fmt.Sprintf("Sync approximately every %d min", minutes)
	}
	return googleGroupSyncNextDescription(job, now, location)
}

func (p *Plugin) googleGroupSyncNow(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	channel, appErr := p.googleGroupSyncChannel(args)
	if appErr != nil {
		return nil, appErr
	}
	if channel == nil {
		response.Text = "Run this command in an active public or private team channel."
		return response, nil
	}
	if err := p.requireGoogleSyncChannelMember(channel.Id, args.UserId); err != nil {
		response.Text = err.Error()
		return response, nil
	}
	actingEmail, err := p.googleGroupActingEmail(args.UserId)
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	requesterEmail, err := p.googleGroupMattermostEmail(args.UserId)
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	records, err := p.googleGroupSyncRecords()
	if err != nil {
		return nil, model.NewAppError("googleGroupSyncNow", "plugin.google_groups.list", nil, err.Error(), http.StatusInternalServerError)
	}
	plan := googleGroupConfirmation{
		Action: "sync_now", UserID: args.UserId, TeamID: args.TeamId, ChannelID: args.ChannelId,
		TargetChannelID: channel.Id, ActingEmail: actingEmail, RequesterEmail: requesterEmail,
	}
	var introductions []string
	for _, record := range records {
		job := record.Job
		if job.TeamID != args.TeamId || job.ChannelID != channel.Id || job.DeadlineAt <= time.Now().UnixMilli() {
			continue
		}
		snapshot, snapshotEmail, err := p.googleGroupChannelSnapshot(job.GroupEmail, actingEmail)
		if err != nil {
			response.Text = fmt.Sprintf("Could not read Google Group %q; nothing was synced: %s", job.GroupEmail, googleUserFacingError(err))
			return response, nil
		}
		if message := p.googleGroupChannelPermissionMessage(args.UserId, args.TeamId, len(snapshot.Members)); message != "" {
			response.Text = message
			return response, nil
		}
		plan.SyncPreviews = append(plan.SyncPreviews, googleGroupSyncPreview{
			Key: record.Key, RecordHash: googleGroupRecordHash(record.Data),
			SnapshotHash: googleGroupSnapshotHash(snapshot), ActingEmail: snapshotEmail,
		})
		introduction, err := p.googleGroupConfirmationIntroduction(snapshot, "", "")
		if err != nil {
			p.API.LogError("Filter Google Group members for manual sync preview failed", "group", job.GroupEmail, "error", err.Error())
			response.Text = "Could not check Mattermost accounts for Google Group members; no sync was run."
			return response, nil
		}
		introductions = append(introductions, introduction)
	}
	if len(plan.SyncPreviews) == 0 {
		response.Text = fmt.Sprintf("No active Google Group syncs are configured for ~%s.", channel.Name)
		return response, nil
	}
	introduction := fmt.Sprintf("Sync all active Google Groups configured for `~%s` now?\n\n%s",
		strings.ReplaceAll(channel.Name, "`", "'"), strings.Join(introductions, "\n\n---\n\n"))
	return p.openGoogleGroupConfirmationText(args, plan, introduction)
}

func googleGroupRecordHash(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (p *Plugin) applyGoogleGroupSyncNow(plan googleGroupConfirmation, actingEmail string) (string, error) {
	channel, appErr := p.API.GetChannel(plan.TargetChannelID)
	if appErr != nil {
		return "", fmt.Errorf("load target channel: %s", appErr.Error())
	}
	if channel == nil || channel.DeleteAt != 0 || channel.TeamId != plan.TeamID ||
		(channel.Type != model.ChannelTypeOpen && channel.Type != model.ChannelTypePrivate) {
		return "", fmt.Errorf("the target channel is no longer active")
	}
	if err := p.requireGoogleSyncChannelMember(channel.Id, plan.UserID); err != nil {
		return "", err
	}
	requesterEmail, err := p.googleGroupMattermostEmail(plan.UserID)
	if err != nil {
		return "", err
	}
	if plan.RequesterEmail != "" && !strings.EqualFold(requesterEmail, plan.RequesterEmail) {
		return "", fmt.Errorf("your Mattermost e-mail changed; run the command again")
	}
	records, err := p.googleGroupSyncRecords()
	if err != nil {
		return "", fmt.Errorf("list Google Group syncs: %w", err)
	}
	active := make(map[string]googleGroupSyncRecord)
	for _, record := range records {
		job := record.Job
		if job.TeamID == plan.TeamID && job.ChannelID == channel.Id && job.DeadlineAt > time.Now().UnixMilli() {
			active[record.Key] = record
		}
	}
	if len(active) != len(plan.SyncPreviews) || len(active) == 0 {
		return "", fmt.Errorf("configured syncs changed or expired; run the command again")
	}
	type pendingSync struct {
		job      googleGroupSync
		snapshot *googleDirectoryGroupSnapshot
	}
	pending := make([]pendingSync, 0, len(active))
	for _, preview := range plan.SyncPreviews {
		record, ok := active[preview.Key]
		if !ok || googleGroupRecordHash(record.Data) != preview.RecordHash {
			return "", fmt.Errorf("configured syncs changed; run the command again")
		}
		delete(active, preview.Key)
		if err := p.verifyGoogleGroupSyncRequester(record.Job); err != nil {
			return "", fmt.Errorf("could not verify sync creator's access to %q: %s", record.Job.GroupEmail, googleUserFacingError(err))
		}
		snapshot, snapshotEmail, err := p.googleGroupChannelSnapshot(record.Job.GroupEmail, actingEmail)
		if err != nil {
			return "", fmt.Errorf("could not re-check Google Group %q: %s", record.Job.GroupEmail, googleUserFacingError(err))
		}
		expectedEmail := preview.ActingEmail
		if expectedEmail == "" {
			expectedEmail = actingEmail
		}
		if !strings.EqualFold(snapshotEmail, expectedEmail) {
			return "", fmt.Errorf("the delegated Google account changed; run the command again")
		}
		if googleGroupSnapshotHash(snapshot) != preview.SnapshotHash {
			return "", fmt.Errorf("Google Group %q changed after the confirmation was opened; run the command again", record.Job.GroupEmail)
		}
		if message := p.googleGroupChannelPermissionMessage(plan.UserID, plan.TeamID, len(snapshot.Members)); message != "" {
			return "", fmt.Errorf("%s", message)
		}
		pending = append(pending, pendingSync{job: record.Job, snapshot: snapshot})
	}
	if len(active) != 0 {
		return "", fmt.Errorf("configured syncs changed; run the command again")
	}
	for _, item := range pending {
		if item.job.DeadlineAt <= time.Now().UnixMilli() {
			return "", fmt.Errorf("a sync expired during confirmation; run the command again")
		}
	}
	total := 0
	var failures []string
	for _, item := range pending {
		if item.job.DeadlineAt <= time.Now().UnixMilli() {
			failures = append(failures, fmt.Sprintf("%s expired before it could be synced", item.job.GroupEmail))
			continue
		}
		added, syncErr := p.syncGoogleGroupMembers(item.snapshot, item.job)
		total += added
		if syncErr != nil {
			p.API.LogError("Manual Google Group sync failed", "group", item.job.GroupEmail, "channel_id", channel.Id, "error", syncErr.Error())
			failures = append(failures, fmt.Sprintf("%s: %v", item.job.GroupEmail, syncErr))
		}
	}
	accounts := make([]string, 0, len(pending))
	for _, item := range pending {
		accounts = append(accounts, fmt.Sprintf("- `%s`: %s", strings.ReplaceAll(item.job.GroupEmail, "`", "'"), googleGroupSyncAccountDescription(item.job)))
	}
	if len(failures) > 0 {
		return fmt.Sprintf("Manual sync requested by `%s` added %d channel members, but some syncs failed: %s\n\n%s", strings.ReplaceAll(requesterEmail, "`", "'"), total, strings.Join(failures, "; "), strings.Join(accounts, "\n")), nil
	}
	return fmt.Sprintf("Synced %d Google Groups to ~%s at the request of `%s` and added %d channel members. Hourly schedules and deadlines are unchanged.\n\n%s", len(pending), channel.Name, strings.ReplaceAll(requesterEmail, "`", "'"), total, strings.Join(accounts, "\n")), nil
}

func (p *Plugin) googleGroupSyncChannel(args *model.CommandArgs) (*model.Channel, *model.AppError) {
	if args.TeamId == "" || args.ChannelId == "" {
		return nil, nil
	}
	channel, appErr := p.API.GetChannel(args.ChannelId)
	if appErr != nil {
		return nil, appErr
	}
	if channel == nil || channel.DeleteAt != 0 || channel.TeamId != args.TeamId ||
		(channel.Type != model.ChannelTypeOpen && channel.Type != model.ChannelTypePrivate) {
		return nil, nil
	}
	return channel, nil
}

func (p *Plugin) googleGroupTeamPermissionMessage(userID, teamID string, permission *model.Permission, action string) string {
	if teamID == "" || !p.API.HasPermissionToTeam(userID, teamID, permission) {
		return fmt.Sprintf("You do not have permission to %s members in this team.", action)
	}
	return ""
}

func (p *Plugin) googleGroupChannelPermissionMessage(userID, teamID string, memberCount int) string {
	config := p.getConfiguration()
	if memberCount > config.GoogleGroupChannelSystemAdminThreshold && !p.API.HasPermissionTo(userID, model.PermissionManageSystem) {
		return fmt.Sprintf("Only system admins can manage Google Groups with more than %d members.", config.GoogleGroupChannelSystemAdminThreshold)
	}
	if memberCount > config.GoogleGroupChannelTeamAdminThreshold && !p.googleGroupTeamAdmin(userID, teamID) {
		return fmt.Sprintf("Only team admins can manage Google Groups with more than %d members.", config.GoogleGroupChannelTeamAdminThreshold)
	}
	return ""
}

func (p *Plugin) googleGroupSyncDeadlinePermissionMessage(userID string, deadline int64, now time.Time) string {
	yearLimit := now.UTC().AddDate(1, 0, 0).Format("2006-01-02")
	if time.UnixMilli(deadline).UTC().Format("2006-01-02") > yearLimit &&
		!p.API.HasPermissionTo(userID, model.PermissionManageSystem) {
		return "Only system admins can add Google Group syncs with a deadline more than one year from today (UTC)."
	}
	return ""
}

func googleGroupSyncAccountDescription(job googleGroupSync) string {
	creator := strings.ReplaceAll(job.CreatedBy, "`", "'")
	account := strings.ReplaceAll(job.RequesterEmail, "`", "'")
	if creator == "" {
		creator = "unknown"
	}
	if account == "" {
		account = "unknown"
	}
	return fmt.Sprintf("created by Mattermost user ID `%s`; sync creator Google account `%s`", creator, account)
}

func (p *Plugin) googleGroupMattermostEmail(userID string) (string, error) {
	user, appErr := p.API.GetUser(userID)
	if appErr != nil {
		return "", fmt.Errorf("load Mattermost user: %s", appErr.Error())
	}
	if user == nil || user.DeleteAt != 0 || strings.TrimSpace(user.Email) == "" {
		return "", fmt.Errorf("your Mattermost account or e-mail is unavailable")
	}
	return strings.TrimSpace(user.Email), nil
}

func (p *Plugin) googleGroupTeamAdmin(userID, teamID string) bool {
	return teamID != "" && (p.API.HasPermissionTo(userID, model.PermissionManageSystem) ||
		p.API.HasPermissionToTeam(userID, teamID, model.PermissionManageTeam))
}

func (p *Plugin) googleGroupActingEmail(userID string) (string, error) {
	user, appErr := p.API.GetUser(userID)
	if appErr != nil {
		return "", fmt.Errorf("load Mattermost user: %s", appErr.Error())
	}
	if user == nil || user.DeleteAt != 0 {
		return "", fmt.Errorf("your Mattermost account is unavailable")
	}
	config := p.getConfiguration()
	if slices.Contains(config.GoogleGroupsSyncAdminUsers, userID) {
		email, ok := p.googleOrganizationEmail(config.GoogleGroupsSyncAdminEmail)
		if !ok {
			return "", fmt.Errorf("configure a valid Google Groups Sync admin e-mail in plugin settings")
		}
		return email, nil
	}
	if email, ok := p.googleOrganizationEmail(user.Email); ok {
		return email, nil
	}
	email, ok := p.googleOrganizationEmail(config.GoogleGroupsSyncFallbackEmail)
	if !ok {
		return "", fmt.Errorf("configure a valid Google Groups Sync fallback account e-mail in plugin settings")
	}
	return email, nil
}

func (p *Plugin) googleGroupOwnActingEmail(userID string) (string, error) {
	user, appErr := p.API.GetUser(userID)
	if appErr != nil {
		return "", fmt.Errorf("load Mattermost user: %s", appErr.Error())
	}
	if user == nil || user.DeleteAt != 0 {
		return "", fmt.Errorf("your Mattermost account is unavailable")
	}
	email, ok := p.googleOrganizationEmail(user.Email)
	if !ok || p.isGoogleFallbackEmail(email) {
		return "", fmt.Errorf("Google Group management requires your own non-fallback account in the configured Google Workspace domain")
	}
	return email, nil
}

func (p *Plugin) isGoogleFallbackEmail(email string) bool {
	fallback := strings.TrimSpace(p.getConfiguration().GoogleGroupsSyncFallbackEmail)
	if email == "" || fallback == "" {
		return false
	}
	normalizedEmail, emailOK := p.googleOrganizationEmail(email)
	normalizedFallback, fallbackOK := p.googleOrganizationEmail(fallback)
	if emailOK && fallbackOK {
		return strings.EqualFold(normalizedEmail, normalizedFallback)
	}
	return strings.EqualFold(strings.TrimSpace(email), fallback)
}

func (p *Plugin) googleOrganizationEmail(email string) (string, bool) {
	email = strings.TrimSpace(email)
	parsed, err := mail.ParseAddress(strings.TrimSpace(email))
	if err != nil || parsed.Address == "" || parsed.Address != email {
		return "", false
	}
	at := strings.LastIndexByte(parsed.Address, '@')
	config := p.getConfiguration()
	domain := strings.TrimSpace(config.GoogleGroupsSyncDomain)
	if at <= 0 || domain == "" {
		return "", false
	}
	addressDomain := parsed.Address[at+1:]
	alternateDomain := strings.TrimSpace(config.GoogleGroupsSyncAlternateDomain)
	if alternateDomain != "" && strings.EqualFold(addressDomain, alternateDomain) {
		return parsed.Address[:at] + "@" + domain, true
	}
	if !strings.EqualFold(addressDomain, domain) {
		return "", false
	}
	return parsed.Address, true
}

func (p *Plugin) googleGroupMattermostUser(email string) (*model.User, *model.AppError) {
	user, appErr := p.API.GetUserByEmail(email)
	if appErr == nil || appErr.StatusCode != http.StatusNotFound {
		return user, appErr
	}
	config := p.getConfiguration()
	domain := strings.TrimSpace(config.GoogleGroupsSyncDomain)
	alternateDomain := strings.TrimSpace(config.GoogleGroupsSyncAlternateDomain)
	at := strings.LastIndexByte(email, '@')
	if at <= 0 || domain == "" || alternateDomain == "" ||
		strings.EqualFold(domain, alternateDomain) || !strings.EqualFold(email[at+1:], domain) {
		return nil, appErr
	}
	return p.API.GetUserByEmail(email[:at] + "@" + alternateDomain)
}

func (p *Plugin) normalizeGoogleGroupEmail(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("specify a Google Group e-mail address or name")
	}
	if !strings.Contains(value, "@") {
		domain := strings.TrimSpace(p.getConfiguration().GoogleGroupsSyncDomain)
		if domain == "" {
			return "", fmt.Errorf("configure the Google Groups Sync organization domain to use a group name without a domain")
		}
		value += "@" + domain
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value || !strings.Contains(value, "@") || strings.ContainsAny(value, " \t\r\n") {
		return "", fmt.Errorf("specify a valid Google Group e-mail address or name")
	}
	return strings.ToLower(value), nil
}

func parseGoogleGroupDeadline(value string, now time.Time) (int64, error) {
	date, err := time.Parse("2006-01-02", value)
	if err != nil || date.Format("2006-01-02") != value {
		return 0, fmt.Errorf("specify the sync deadline as YYYY-MM-DD")
	}
	expiresAt := date.AddDate(0, 0, 1).Add(-time.Millisecond)
	if !expiresAt.After(now) {
		return 0, fmt.Errorf("the sync deadline must be today or a future date")
	}
	return expiresAt.UnixMilli(), nil
}

func (p *Plugin) googleGroupChannelSnapshot(groupEmail, actingEmail string) (*googleDirectoryGroupSnapshot, string, error) {
	snapshot, err := p.googleGroupSnapshot(groupEmail, actingEmail, true)
	if err == nil {
		return snapshot, actingEmail, nil
	}
	if !googlePermissionDenied(err) {
		return nil, "", err
	}
	if directErr := p.verifyGoogleGroupDirectAccess(groupEmail, actingEmail); directErr != nil {
		return nil, "", fmt.Errorf("verify direct group access: %w", directErr)
	}
	adminEmail, ok := p.googleOrganizationEmail(p.getConfiguration().GoogleGroupsSyncAdminEmail)
	if !ok {
		return nil, "", fmt.Errorf("configure a valid Google Groups Sync admin e-mail to read indirect group membership")
	}
	if strings.EqualFold(actingEmail, adminEmail) {
		return nil, "", err
	}
	snapshot, err = p.googleGroupSnapshot(groupEmail, adminEmail, true)
	if err != nil {
		return nil, "", &googleGroupAccessError{operation: "Could not read group using the configured admin account", err: err}
	}
	return snapshot, adminEmail, nil
}

type googleGroupAccessError struct {
	operation string
	err       error
}

func (e *googleGroupAccessError) Error() string { return e.operation + ": " + e.err.Error() }
func (e *googleGroupAccessError) Unwrap() error { return e.err }

func (p *Plugin) verifyGoogleGroupDirectAccess(groupEmail, actingEmail string) error {
	query := url.Values{"groupKey.id": {groupEmail}}
	var group struct {
		Name string `json:"name"`
	}
	if err := p.googleRequestWithScopes(googleGroupsSyncServiceAccountKey, actingEmail, http.MethodGet,
		googleCloudIdentityURL+"/groups:lookup?"+query.Encode(), nil, &group, googleCloudIdentityReadScope); err != nil {
		return &googleGroupAccessError{operation: "Could not look up group with Cloud Identity", err: err}
	}
	groupID, ok := strings.CutPrefix(group.Name, "groups/")
	if !ok || groupID == "" || strings.ContainsAny(groupID, "/\\?#") {
		return fmt.Errorf("Cloud Identity returned an invalid Google Group resource name")
	}
	var response *struct {
		Memberships []json.RawMessage `json:"memberships"`
	}
	endpoint := googleCloudIdentityURL + "/groups/" + url.PathEscape(groupID) + "/memberships?pageSize=1"
	if err := p.googleRequestWithScopes(googleGroupsSyncServiceAccountKey, actingEmail, http.MethodGet,
		endpoint, nil, &response, googleCloudIdentityReadScope); err != nil {
		return &googleGroupAccessError{operation: "Could not verify direct group access with Cloud Identity", err: err}
	}
	if response == nil {
		return fmt.Errorf("Cloud Identity returned an invalid membership response")
	}
	return nil
}

func (p *Plugin) verifyGoogleGroupSyncRequester(job googleGroupSync) error {
	delegatedEmail, err := p.googleGroupActingEmail(job.CreatedBy)
	if err != nil {
		return err
	}
	expectedEmail := job.ActingEmail
	if job.DelegatedEmail != "" {
		expectedEmail = job.DelegatedEmail
	}
	if !strings.EqualFold(delegatedEmail, expectedEmail) {
		return fmt.Errorf("the sync creator's delegated Google account changed")
	}
	requesterEmail, err := p.googleGroupOwnActingEmail(job.CreatedBy)
	if err != nil {
		return err
	}
	if job.RequesterEmail != "" && !strings.EqualFold(requesterEmail, job.RequesterEmail) {
		return fmt.Errorf("the sync creator's Google account changed")
	}
	if err := p.verifyGoogleGroupDirectAccess(job.GroupEmail, requesterEmail); err != nil {
		return fmt.Errorf("verify direct group access: %w", err)
	}
	return nil
}

func (p *Plugin) googleGroupSnapshot(groupEmail, actingEmail string, includeDerived bool) (*googleDirectoryGroupSnapshot, error) {
	if !includeDerived {
		return p.googleCloudIdentityGroupSnapshot(groupEmail, actingEmail)
	}

	return p.googleDirectoryGroupSnapshot(groupEmail, actingEmail, true)
}

func (p *Plugin) googleDirectoryGroupSnapshot(groupEmail, actingEmail string, includeDerived bool) (*googleDirectoryGroupSnapshot, error) {
	var group googleDirectoryGroup
	groupURL := googleDirectoryURL + "/groups/" + url.PathEscape(groupEmail)
	if err := p.googleRequest(googleGroupsSyncServiceAccountKey, actingEmail, http.MethodGet, groupURL, nil, &group); err != nil {
		return nil, fmt.Errorf("get group details: %w", err)
	}
	group.Email = strings.ToLower(group.Email)
	if group.Email == "" {
		return nil, fmt.Errorf("Google Group response did not contain an e-mail address")
	}
	groupKey := group.ID
	if groupKey == "" {
		groupKey = group.Email
	}
	members := make([]googleDirectoryGroupMember, 0)
	pageToken := ""
	for {
		query := url.Values{"maxResults": {strconv.Itoa(googleGroupSyncMaxResults)}}
		if includeDerived {
			query.Set("includeDerivedMembership", "true")
		}
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		var page struct {
			Members       []googleDirectoryGroupMember `json:"members"`
			NextPageToken string                       `json:"nextPageToken"`
		}
		endpoint := googleDirectoryURL + "/groups/" + url.PathEscape(groupKey) + "/members?" + query.Encode()
		if err := p.googleRequest(googleGroupsSyncServiceAccountKey, actingEmail, http.MethodGet, endpoint, nil, &page); err != nil {
			return nil, fmt.Errorf("list group members: %w", err)
		}
		members = append(members, page.Members...)
		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}
	sortGoogleGroupMembers(members)
	return &googleDirectoryGroupSnapshot{Group: group, Members: members}, nil
}

func (p *Plugin) googleCloudIdentityGroupSnapshot(groupEmail, actingEmail string) (*googleDirectoryGroupSnapshot, error) {
	query := url.Values{"groupKey.id": {groupEmail}}
	var lookup struct {
		Name string `json:"name"`
	}
	if err := p.googleRequestWithScopes(googleGroupsSyncServiceAccountKey, actingEmail, http.MethodGet,
		googleCloudIdentityURL+"/groups:lookup?"+query.Encode(), nil, &lookup, googleCloudIdentityReadScope); err != nil {
		return nil, fmt.Errorf("look up group with Cloud Identity: %w", err)
	}
	groupID, ok := strings.CutPrefix(lookup.Name, "groups/")
	if !ok || groupID == "" || strings.ContainsAny(groupID, "/\\?#") {
		return nil, fmt.Errorf("Cloud Identity returned an invalid Google Group resource name")
	}

	var identityGroup struct {
		Name     string `json:"name"`
		Display  string `json:"displayName"`
		GroupKey struct {
			ID string `json:"id"`
		} `json:"groupKey"`
	}
	groupEndpoint := googleCloudIdentityURL + "/groups/" + url.PathEscape(groupID)
	if err := p.googleRequestWithScopes(googleGroupsSyncServiceAccountKey, actingEmail, http.MethodGet,
		groupEndpoint, nil, &identityGroup, googleCloudIdentityReadScope); err != nil {
		return nil, fmt.Errorf("get group details with Cloud Identity: %w", err)
	}
	if identityGroup.Name != lookup.Name || strings.TrimSpace(identityGroup.GroupKey.ID) == "" {
		return nil, fmt.Errorf("Cloud Identity returned incomplete Google Group details")
	}
	group := googleDirectoryGroup{
		ResourceName: identityGroup.Name,
		Email:        strings.ToLower(identityGroup.GroupKey.ID),
		Name:         identityGroup.Display,
	}

	members := make([]googleDirectoryGroupMember, 0)
	pageToken := ""
	for {
		query := url.Values{
			"pageSize": {strconv.Itoa(googleGroupSyncMaxResults)},
			"view":     {"FULL"},
		}
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		var page struct {
			Memberships   []googleDirectoryGroupMember `json:"memberships"`
			NextPageToken string                       `json:"nextPageToken"`
		}
		endpoint := googleCloudIdentityURL + "/" + identityGroup.Name + "/memberships?" + query.Encode()
		if err := p.googleRequestWithScopes(googleGroupsSyncServiceAccountKey, actingEmail, http.MethodGet,
			endpoint, nil, &page, googleCloudIdentityReadScope); err != nil {
			return nil, fmt.Errorf("list group members with Cloud Identity: %w", err)
		}
		for _, member := range page.Memberships {
			if member.Email == "" {
				member.Email = member.PreferredMemberKey.ID
			}
			members = append(members, member)
		}
		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}
	sortGoogleGroupMembers(members)
	return &googleDirectoryGroupSnapshot{Group: group, Members: members}, nil
}

func sortGoogleGroupMembers(members []googleDirectoryGroupMember) {
	sort.Slice(members, func(i, j int) bool {
		left, right := strings.ToLower(members[i].Email), strings.ToLower(members[j].Email)
		if left == right {
			return members[i].Email < members[j].Email
		}
		return left < right
	})
}

func googleGroupName(group googleDirectoryGroup) string {
	if strings.TrimSpace(group.Name) != "" {
		return group.Name
	}
	return group.Email
}

func (p *Plugin) googleGroupMember(members []googleDirectoryGroupMember, email string) (googleDirectoryGroupMember, bool) {
	normalizedEmail, ok := p.googleOrganizationEmail(email)
	if !ok {
		normalizedEmail = strings.TrimSpace(email)
	}
	for _, member := range members {
		memberEmail, ok := p.googleOrganizationEmail(member.Email)
		if !ok {
			memberEmail = strings.TrimSpace(member.Email)
		}
		if strings.EqualFold(member.Type, "USER") && strings.EqualFold(memberEmail, normalizedEmail) {
			return member, true
		}
	}
	return googleDirectoryGroupMember{}, false
}

func googleCloudIdentityGroupMembershipsEndpoint(groupResourceName string) (string, error) {
	groupID, ok := strings.CutPrefix(groupResourceName, "groups/")
	if !ok || groupID == "" || strings.ContainsAny(groupID, "/\\?#") {
		return "", fmt.Errorf("Cloud Identity returned an invalid Google Group resource name")
	}
	return googleCloudIdentityURL + "/" + groupResourceName + "/memberships", nil
}

func (p *Plugin) googleCloudIdentityMembershipOperation(adminEmail, method, endpoint string, payload interface{}) error {
	var operation struct {
		Name  string `json:"name"`
		Done  bool   `json:"done"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := p.googleRequestWithScopes(googleGroupsSyncServiceAccountKey, adminEmail, method,
		endpoint, payload, &operation, googleCloudIdentityWriteScope); err != nil {
		return err
	}
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	pollInterval := time.NewTimer(250 * time.Millisecond)
	defer pollInterval.Stop()
	for {
		if operation.Done {
			if operation.Error != nil {
				return fmt.Errorf("Cloud Identity operation failed (%d): %s", operation.Error.Code, operation.Error.Message)
			}
			return nil
		}
		operationID, ok := strings.CutPrefix(operation.Name, "operations/")
		if !ok || operationID == "" || strings.ContainsAny(operationID, "/\\?#") {
			return fmt.Errorf("Cloud Identity returned an invalid operation resource name")
		}

		select {
		case <-pollInterval.C:
			pollInterval.Reset(250 * time.Millisecond)
		case <-deadline.C:
			return fmt.Errorf("Cloud Identity operation did not complete within 30 seconds")
		}
		operationEndpoint := googleCloudIdentityURL + "/operations/" + url.PathEscape(operationID)
		if err := p.googleRequestWithScopes(googleGroupsSyncServiceAccountKey, adminEmail, http.MethodGet,
			operationEndpoint, nil, &operation, googleCloudIdentityWriteScope); err != nil {
			return fmt.Errorf("check Cloud Identity operation: %w", err)
		}
	}
}

func googleGroupSnapshotHash(snapshot *googleDirectoryGroupSnapshot) string {
	data, _ := json.Marshal(struct {
		Group   googleDirectoryGroup
		Members []googleDirectoryGroupMember
	}{snapshot.Group, snapshot.Members})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (p *Plugin) googleGroupConfirmationIntroduction(snapshot *googleDirectoryGroupSnapshot, channelName, action string) (string, error) {
	members := make([]googleDirectoryGroupMember, 0, len(snapshot.Members))
	omittedSubgroups := 0
	for _, member := range snapshot.Members {
		if member.Email == "" {
			continue
		}
		if !strings.EqualFold(member.Type, "GROUP") {
			members = append(members, member)
			continue
		}
		user, appErr := p.googleGroupMattermostUser(member.Email)
		if appErr != nil && appErr.StatusCode != http.StatusNotFound {
			return "", fmt.Errorf("Mattermost user lookup failed: %s", appErr.Error())
		}
		if user != nil {
			members = append(members, member)
		} else {
			omittedSubgroups++
		}
	}
	return formatGoogleGroupConfirmationIntroduction(snapshot, channelName, action, members, omittedSubgroups), nil
}

func formatGoogleGroupConfirmationIntroduction(snapshot *googleDirectoryGroupSnapshot, channelName, action string, displayMembers []googleDirectoryGroupMember, omittedSubgroups int) string {
	members := make([]string, 0, 5)
	sortedMembers := append([]googleDirectoryGroupMember(nil), displayMembers...)
	sortGoogleGroupMembers(sortedMembers)
	for _, member := range sortedMembers {
		if member.Email == "" {
			continue
		}
		members = append(members, "- `"+strings.ReplaceAll(member.Email, "`", "'")+"`")
		if len(members) == 5 {
			break
		}
	}
	if len(members) == 0 {
		if len(snapshot.Members) == 0 {
			members = append(members, "- _No members_")
		} else {
			members = append(members, "- _No reportable members_")
		}
	}
	if omittedSubgroups > 0 {
		if omittedSubgroups == 1 {
			members = append(members, "- (and a subgroup)")
		} else {
			members = append(members, "- (and some subgroups)")
		}
	}
	introduction := "> **Google Group details**"
	if name := googleGroupName(snapshot.Group); !strings.EqualFold(name, snapshot.Group.Email) {
		introduction += "\n>\n> **Group:** `" + strings.ReplaceAll(name, "`", "'") + "`"
	}
	introduction += fmt.Sprintf(
		"\n>\n> **Address:** `%s`\n>\n> **Members:** %d\n>\n> **First five alphabetically:**\n> %s",
		strings.ReplaceAll(snapshot.Group.Email, "`", "'"), len(snapshot.Members), strings.Join(members, "\n> "),
	)
	if channelName != "" {
		introduction += "\n>\n> **Channel:** `~" + strings.ReplaceAll(channelName, "`", "'") + "`"
	}
	if action != "" {
		introduction = action + "\n\n" + introduction
	}
	return introduction
}

func googleGroupSyncKey(teamID, channelID, groupEmail string) string {
	hash := sha256.Sum256([]byte(strings.ToLower(groupEmail)))
	return googleGroupSyncPrefix + teamID + "_" + channelID + "_" + fmt.Sprintf("%x", hash[:12])
}

func (p *Plugin) openGoogleGroupConfirmation(args *model.CommandArgs, plan googleGroupConfirmation, snapshot *googleDirectoryGroupSnapshot, channelName, action string) (*model.CommandResponse, *model.AppError) {
	introduction, err := p.googleGroupConfirmationIntroduction(snapshot, channelName, action)
	if err != nil {
		p.API.LogError("Filter Google Group members for confirmation failed", "group", snapshot.Group.Email, "error", err.Error())
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "Could not check Mattermost accounts for Google Group members; no changes were made.",
		}, nil
	}
	return p.openGoogleGroupConfirmationText(args, plan, introduction)
}

func (p *Plugin) openGoogleGroupConfirmationText(args *model.CommandArgs, plan googleGroupConfirmation, introduction string) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	if args.TriggerId == "" {
		response.Text = "Mattermost did not provide a confirmation trigger. Please retry the command."
		return response, nil
	}
	requesterEmail := plan.RequesterEmail
	if requesterEmail == "" {
		var err error
		requesterEmail, err = p.googleGroupMattermostEmail(args.UserId)
		if err != nil {
			response.Text = err.Error()
			return response, nil
		}
	}
	account := "`" + strings.ReplaceAll(requesterEmail, "`", "'") + "`"
	if p.isGoogleFallbackEmail(plan.ActingEmail) {
		account = "an external account"
	}
	introduction += "\n\nYou are performing this action as " + account
	id := model.NewId()
	plan.ExpiresAt = time.Now().Add(5 * time.Minute).UnixMilli()
	data, err := json.Marshal(plan)
	if err != nil {
		return nil, model.NewAppError("openGoogleGroupConfirmation", "plugin.google_groups.confirmation", nil, err.Error(), http.StatusInternalServerError)
	}
	if appErr := p.API.KVSetWithExpiry(googleGroupConfirmationPrefix+id, data, 300); appErr != nil {
		return nil, appErr
	}
	dialog := model.OpenDialogRequest{
		TriggerId: args.TriggerId,
		URL:       "/plugins/" + p.API.GetPluginID() + "/google-groups/confirm",
		Dialog: model.Dialog{
			CallbackId:       googleGroupConfirmationID,
			Title:            "Confirm /google group",
			IntroductionText: introduction,
			SubmitLabel:      "Confirm",
			Elements: []model.DialogElement{{
				DisplayName: "Account permission:",
				Name:        "confirm",
				Type:        "radio",
				Default:     "ok",
				Options: []*model.PostActionOptions{{
					Text:  "OK. You can perform this operation.",
					Value: "ok",
				}},
			}},
			NotifyOnCancel: true,
			State:          id,
		},
	}
	if err := dialog.IsValid(); err != nil {
		if deleteErr := p.API.KVDelete(googleGroupConfirmationPrefix + id); deleteErr != nil {
			p.API.LogError("Delete invalid Google Group confirmation failed", "error", deleteErr.Error())
		}
		return nil, model.NewAppError("openGoogleGroupConfirmation", "plugin.google_groups.dialog", nil, err.Error(), http.StatusInternalServerError)
	}
	if appErr := p.API.OpenInteractiveDialog(dialog); appErr != nil {
		if deleteErr := p.API.KVDelete(googleGroupConfirmationPrefix + id); deleteErr != nil {
			p.API.LogError("Delete unopened Google Group confirmation failed", "error", deleteErr.Error())
		}
		return nil, appErr
	}
	response.Text = "Review and confirm the `/google group` action in the dialog."
	return response, nil
}

func (p *Plugin) serveGoogleGroupConfirmation(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, message string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(model.SubmitDialogResponse{Error: message})
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		fail(http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	var submission model.SubmitDialogRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&submission); err != nil {
		fail(http.StatusBadRequest, "Invalid confirmation submission.")
		return
	}
	if submission.CallbackId != googleGroupConfirmationID || !model.IsValidId(submission.State) {
		fail(http.StatusBadRequest, "Invalid or expired confirmation.")
		return
	}
	key := googleGroupConfirmationPrefix + submission.State
	data, appErr := p.API.KVGet(key)
	if appErr != nil {
		fail(http.StatusInternalServerError, "Could not load the confirmation.")
		return
	}
	var plan googleGroupConfirmation
	if len(data) == 0 || json.Unmarshal(data, &plan) != nil || time.Now().UnixMilli() >= plan.ExpiresAt {
		fail(http.StatusConflict, "This confirmation expired. Run the command again.")
		return
	}
	if r.Header.Get("Mattermost-User-Id") != plan.UserID || submission.UserId != plan.UserID || submission.TeamId != plan.TeamID || submission.ChannelId != plan.ChannelID {
		fail(http.StatusForbidden, "This confirmation belongs to another user or channel.")
		return
	}
	if plan.Action == "member_add" || plan.Action == "member_remove" {
		fail(http.StatusConflict, "Google Group membership commands are no longer available.")
		return
	}
	if submission.Cancelled {
		if err := p.API.KVDelete(key); err != nil {
			fail(http.StatusInternalServerError, "Could not cancel the confirmation.")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"text": "Cancelled."})
		return
	}
	if confirmation, ok := submission.Submission["confirm"].(string); !ok || confirmation != "ok" {
		_ = json.NewEncoder(w).Encode(model.SubmitDialogResponse{Errors: map[string]string{
			"confirm": "Select OK to confirm the operation.",
		}})
		return
	}
	consumed, consumeErr := p.API.KVSetWithOptions(key, nil, model.PluginKVSetOptions{Atomic: true, OldValue: data})
	if consumeErr != nil {
		fail(http.StatusInternalServerError, "Could not consume the confirmation.")
		return
	}
	if !consumed {
		fail(http.StatusConflict, "This confirmation was already used. Run the command again.")
		return
	}
	message, err := p.applyGoogleGroupConfirmation(plan)
	if err != nil {
		fail(http.StatusOK, err.Error())
		return
	}
	if post := p.API.SendEphemeralPost(submission.UserId, &model.Post{
		UserId: p.pluginBot.UserId, ChannelId: submission.ChannelId, Message: message,
	}); post == nil {
		p.API.LogError("Could not report Google Group confirmation result", "user_id", submission.UserId)
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"text": message})
}

func (p *Plugin) applyGoogleGroupConfirmation(plan googleGroupConfirmation) (string, error) {
	lock, err := cluster.NewMutex(p.API, "google_groups_sync")
	if err != nil {
		return "", fmt.Errorf("could not lock Google Group operations: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := lock.LockWithContext(ctx); err != nil {
		return "", fmt.Errorf("Google Group operations are busy; retry the command: %w", err)
	}
	defer lock.Unlock()

	if (plan.Action == "sync_add" || plan.Action == "sync_remove" || plan.Action == "sync_now" ||
		plan.Action == "team_add" || plan.Action == "team_remove") &&
		!p.googleGroupTeamAdmin(plan.UserID, plan.TeamID) {
		return "", fmt.Errorf("only system or team admins can manage Google Group syncs")
	}
	if plan.Action == "team_add" || (plan.Action == "sync_add" && (plan.TargetTeam || plan.AlsoInviteToTeam)) ||
		(plan.Action == "sync_remove" && plan.TargetTeam) {
		if message := p.googleGroupTeamPermissionMessage(plan.UserID, plan.TeamID, model.PermissionAddUserToTeam, "add"); message != "" {
			return "", fmt.Errorf("%s", message)
		}
	}
	if plan.Action == "team_remove" {
		if message := p.googleGroupTeamPermissionMessage(plan.UserID, plan.TeamID, model.PermissionRemoveUserFromTeam, "remove"); message != "" {
			return "", fmt.Errorf("%s", message)
		}
	}
	var actingEmail string
	if plan.Action == "manage_add" || plan.Action == "manage_remove" {
		actingEmail, err = p.googleGroupOwnActingEmail(plan.UserID)
	} else {
		actingEmail, err = p.googleGroupActingEmail(plan.UserID)
	}
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(actingEmail, plan.ActingEmail) {
		return "", fmt.Errorf("the delegated Google account changed; run the command again")
	}
	if plan.Action == "manage_remove" && (plan.MemberUserID == plan.UserID || strings.EqualFold(plan.MemberEmail, actingEmail)) {
		return "", fmt.Errorf("you cannot remove yourself from a Google Group")
	}
	if plan.Action == "sync_now" {
		return p.applyGoogleGroupSyncNow(plan, actingEmail)
	}
	var snapshot *googleDirectoryGroupSnapshot
	snapshotEmail := actingEmail
	if plan.Action == "sync_add" || plan.Action == "sync_remove" || plan.Action == "channel_add" ||
		plan.Action == "channel_remove" || plan.Action == "team_add" || plan.Action == "team_remove" {
		snapshot, snapshotEmail, err = p.googleGroupChannelSnapshot(plan.GroupEmail, actingEmail)
	} else {
		snapshot, err = p.googleGroupSnapshot(plan.GroupEmail, actingEmail, false)
	}
	if err != nil {
		return "", fmt.Errorf("could not re-check Google Group %q: %s", plan.GroupEmail, googleUserFacingError(err))
	}
	expectedSnapshotEmail := plan.SnapshotEmail
	if expectedSnapshotEmail == "" {
		expectedSnapshotEmail = actingEmail
	}
	if !strings.EqualFold(snapshotEmail, expectedSnapshotEmail) {
		return "", fmt.Errorf("the delegated Google account changed; run the command again")
	}
	if googleGroupSnapshotHash(snapshot) != plan.SnapshotHash {
		return "", fmt.Errorf("Google Group membership changed after the confirmation was opened; run the command again")
	}

	switch plan.Action {
	case "channel_add", "channel_remove":
		if message := p.googleGroupChannelPermissionMessage(plan.UserID, plan.TeamID, len(snapshot.Members)); message != "" {
			return "", fmt.Errorf("%s", message)
		}
		channel, appErr := p.googleGroupSyncChannel(&model.CommandArgs{TeamId: plan.TeamID, ChannelId: plan.ChannelID})
		if appErr != nil {
			return "", fmt.Errorf("load channel: %s", appErr.Error())
		}
		if channel == nil {
			return "", fmt.Errorf("the target channel is no longer active")
		}
		if err := p.requireGoogleSyncChannelMember(channel.Id, plan.UserID); err != nil {
			return "", err
		}
		if plan.Action == "channel_add" {
			added, err := p.syncGoogleGroupMembers(snapshot, googleGroupSync{TeamID: plan.TeamID, ChannelID: channel.Id})
			if err != nil {
				return fmt.Sprintf("Added %d channel members, but some could not be added: %v", added, err), nil
			}
			return fmt.Sprintf("Added %d members of %s to ~%s. No sync was configured.", added, snapshot.Group.Email, channel.Name), nil
		}
		removed, err := p.removeGoogleGroupChannelMembers(snapshot, channel.Id, plan.UserID)
		if err != nil {
			return fmt.Sprintf("Removed %d channel members, but some could not be removed: %v", removed, err), nil
		}
		return fmt.Sprintf("Removed %d members of %s from ~%s. Active syncs may add them again.", removed, snapshot.Group.Email, channel.Name), nil
	case "team_add", "team_remove":
		if message := p.googleGroupChannelPermissionMessage(plan.UserID, plan.TeamID, len(snapshot.Members)); message != "" {
			return "", fmt.Errorf("%s", message)
		}
		channel, appErr := p.googleGroupSyncChannel(&model.CommandArgs{TeamId: plan.TeamID, ChannelId: plan.ChannelID})
		if appErr != nil {
			return "", fmt.Errorf("load channel: %s", appErr.Error())
		}
		if channel == nil {
			return "", fmt.Errorf("the target channel is no longer active")
		}
		if err := p.requireGoogleSyncChannelMember(channel.Id, plan.UserID); err != nil {
			return "", err
		}
		team, appErr := p.API.GetTeam(plan.TeamID)
		if appErr != nil {
			return "", fmt.Errorf("load team: %s", appErr.Error())
		}
		if team == nil || team.DeleteAt != 0 {
			return "", fmt.Errorf("the target team is no longer active")
		}
		if plan.Action == "team_add" {
			added, err := p.syncGoogleGroupTeamMembers(snapshot, team.Id)
			if err != nil {
				return fmt.Sprintf("Added %d team members, but some could not be added: %v", added, err), nil
			}
			return fmt.Sprintf("Added %d members of %s to team %s.", added, snapshot.Group.Email, team.DisplayName), nil
		}
		removed, err := p.removeGoogleGroupTeamMembers(snapshot, team.Id, plan.UserID)
		if err != nil {
			return fmt.Sprintf("Removed %d team members, but some could not be removed: %v", removed, err), nil
		}
		return fmt.Sprintf("Removed %d members of %s from team %s. Active syncs may add them again.", removed, snapshot.Group.Email, team.DisplayName), nil
	case "sync_add":
		return p.applyGoogleGroupSyncAdd(plan, snapshot, snapshotEmail, actingEmail)
	case "sync_remove":
		if message := p.googleGroupChannelPermissionMessage(plan.UserID, plan.TeamID, len(snapshot.Members)); message != "" {
			return "", fmt.Errorf("%s", message)
		}
		channel, appErr := p.googleGroupSyncChannel(&model.CommandArgs{TeamId: plan.TeamID, ChannelId: plan.ChannelID})
		if appErr != nil {
			return "", fmt.Errorf("load channel: %s", appErr.Error())
		}
		if channel == nil {
			return "", fmt.Errorf("the target channel is no longer active")
		}
		if err := p.requireGoogleSyncChannelMember(plan.ChannelID, plan.UserID); err != nil {
			return "", err
		}
		key := googleGroupSyncKey(plan.TeamID, plan.ChannelID, plan.GroupEmail)
		if plan.TargetTeam {
			key = googleGroupSyncKey(plan.TeamID, "", plan.GroupEmail)
		}
		data, appErr := p.API.KVGet(key)
		if appErr != nil {
			return "", fmt.Errorf("load sync settings: %s", appErr.Error())
		}
		if len(data) == 0 {
			return "", fmt.Errorf("this Google Group sync was already removed")
		}
		var job googleGroupSync
		if err := json.Unmarshal(data, &job); err != nil {
			return "", fmt.Errorf("decode sync settings: %w", err)
		}
		if appErr := p.API.KVDelete(key); appErr != nil {
			return "", fmt.Errorf("remove sync settings: %s", appErr.Error())
		}
		target := "channel"
		if plan.TargetTeam {
			target = "team"
		}
		return fmt.Sprintf("Stopped syncing %s to the %s (%s). Existing members were not removed.", plan.GroupEmail, target, googleGroupSyncAccountDescription(job)), nil
	case "manage_add", "manage_remove":
		target, appErr := p.API.GetUser(plan.MemberUserID)
		if appErr != nil {
			return "", fmt.Errorf("load selected Mattermost user: %s", appErr.Error())
		}
		targetEmail, targetEmailOK := "", false
		if target != nil {
			targetEmail, targetEmailOK = p.googleOrganizationEmail(target.Email)
		}
		if target == nil || target.DeleteAt != 0 || target.IsBot || target.Id != plan.MemberUserID ||
			!targetEmailOK || !strings.EqualFold(targetEmail, plan.MemberEmail) {
			return "", fmt.Errorf("the selected Mattermost user or their e-mail changed; run the command again")
		}
		if _, ok := p.googleOrganizationEmail(plan.MemberEmail); !ok || p.isGoogleFallbackEmail(plan.MemberEmail) {
			return "", fmt.Errorf("only a non-fallback account in the configured domain can be added to or removed from Google Groups")
		}
		groupMember, isMember := p.googleGroupMember(snapshot.Members, plan.MemberEmail)
		if plan.Action == "manage_add" {
			if isMember {
				return fmt.Sprintf("%s is already a member of %s.", plan.MemberEmail, googleGroupName(snapshot.Group)), nil
			}
			var googleUser googleDirectoryUser
			userURL := googleDirectoryURL + "/users/" + url.PathEscape(plan.MemberEmail) + "?viewType=domain_public&projection=basic"
			if err := p.googleRequest(googleGroupsSyncServiceAccountKey, actingEmail, http.MethodGet, userURL, nil, &googleUser); err != nil {
				return "", fmt.Errorf("verify that %s is a Google user before adding it: %s", plan.MemberEmail, googleUserFacingError(err))
			}
			_, validEmail := p.googleOrganizationEmail(googleUser.PrimaryEmail)
			if googleUser.ID == "" || !validEmail || p.isGoogleFallbackEmail(googleUser.PrimaryEmail) {
				return "", fmt.Errorf("Google did not identify %s as an eligible organization user", plan.MemberEmail)
			}
			payload := map[string]interface{}{
				"preferredMemberKey": map[string]string{"id": googleUser.PrimaryEmail},
				"roles":              []map[string]string{{"name": "MEMBER"}},
			}
			endpoint, err := googleCloudIdentityGroupMembershipsEndpoint(snapshot.Group.ResourceName)
			if err != nil {
				return "", err
			}
			if err := p.googleCloudIdentityMembershipOperation(actingEmail, http.MethodPost, endpoint, payload); err != nil {
				return "", fmt.Errorf("Google denied adding %s to %s: %s", plan.MemberEmail, snapshot.Group.Email, googleUserFacingError(err))
			}
			return fmt.Sprintf("Added %s to Google Group %s.", plan.MemberEmail, snapshot.Group.Email), nil
		}
		if !isMember {
			return fmt.Sprintf("%s is no longer a member of %s.", plan.MemberEmail, googleGroupName(snapshot.Group)), nil
		}
		var actor, member googleDirectoryUser
		for _, lookup := range []struct {
			email string
			user  *googleDirectoryUser
		}{{actingEmail, &actor}, {plan.MemberEmail, &member}} {
			endpoint := googleDirectoryURL + "/users/" + url.PathEscape(lookup.email) + "?viewType=domain_public&projection=basic"
			if err := p.googleRequest(googleGroupsSyncServiceAccountKey, actingEmail, http.MethodGet, endpoint, nil, lookup.user); err != nil {
				return "", fmt.Errorf("verify Google account %q before removal: %s", lookup.email, googleUserFacingError(err))
			}
			if lookup.user.ID == "" {
				return "", fmt.Errorf("Google did not identify account %q; cannot safely remove group member", lookup.email)
			}
		}
		if actor.ID == member.ID {
			return "", fmt.Errorf("you cannot remove yourself from a Google Group")
		}
		prefix := snapshot.Group.ResourceName + "/memberships/"
		membershipID := strings.TrimPrefix(groupMember.Name, prefix)
		if !strings.HasPrefix(groupMember.Name, prefix) || membershipID == "" || strings.ContainsAny(membershipID, "/\\?#") {
			return "", fmt.Errorf("Cloud Identity returned an invalid group membership resource name")
		}
		endpoint := googleCloudIdentityURL + "/" + groupMember.Name
		if err := p.googleCloudIdentityMembershipOperation(actingEmail, http.MethodDelete, endpoint, nil); err != nil {
			return "", fmt.Errorf("Google denied removing %s from %s: %s", plan.MemberEmail, snapshot.Group.Email, googleUserFacingError(err))
		}
		return fmt.Sprintf("Removed %s from Google Group %s.", plan.MemberEmail, snapshot.Group.Email), nil
	default:
		return "", fmt.Errorf("unknown Google Group action")
	}
}

func (p *Plugin) applyGoogleGroupSyncAdd(plan googleGroupConfirmation, snapshot *googleDirectoryGroupSnapshot, snapshotEmail, actingEmail string) (string, error) {
	if message := p.googleGroupChannelPermissionMessage(plan.UserID, plan.TeamID, len(snapshot.Members)); message != "" {
		return "", fmt.Errorf("%s", message)
	}
	if message := p.googleGroupSyncDeadlinePermissionMessage(plan.UserID, plan.DeadlineAt, time.Now()); message != "" {
		return "", fmt.Errorf("%s", message)
	}
	if time.Now().UnixMilli() >= plan.DeadlineAt {
		return "", fmt.Errorf("the sync deadline has passed; run the command again with a future date")
	}
	channel, appErr := p.googleGroupSyncChannel(&model.CommandArgs{TeamId: plan.TeamID, ChannelId: plan.ChannelID})
	if appErr != nil {
		return "", fmt.Errorf("load channel: %s", appErr.Error())
	}
	if channel == nil {
		return "", fmt.Errorf("the target channel is no longer active")
	}
	if err := p.requireGoogleSyncChannelMember(plan.ChannelID, plan.UserID); err != nil {
		return "", err
	}
	if plan.TargetTeam || plan.AlsoInviteToTeam {
		if message := p.googleGroupTeamPermissionMessage(plan.UserID, plan.TeamID, model.PermissionAddUserToTeam, "add"); message != "" {
			return "", fmt.Errorf("%s", message)
		}
		team, appErr := p.API.GetTeam(plan.TeamID)
		if appErr != nil {
			return "", fmt.Errorf("load team: %s", appErr.Error())
		}
		if team == nil || team.DeleteAt != 0 {
			return "", fmt.Errorf("the target team is no longer active")
		}
	}
	requesterEmail, err := p.googleGroupOwnActingEmail(plan.UserID)
	if err != nil {
		return "", fmt.Errorf("could not identify sync creator's Google account: %w", err)
	}
	if plan.RequesterEmail != "" && !strings.EqualFold(requesterEmail, plan.RequesterEmail) {
		return "", fmt.Errorf("the sync creator's Google account changed; run the command again")
	}

	type targetSync struct {
		key         string
		job         googleGroupSync
		initialData []byte
	}
	newSyncs := make([]targetSync, 0, 2)
	baseJob := googleGroupSync{
		GroupEmail: snapshot.Group.Email, GroupName: googleGroupName(snapshot.Group),
		TeamID: plan.TeamID, CreatedBy: plan.UserID, RequesterEmail: requesterEmail,
		ActingEmail: snapshotEmail, DelegatedEmail: actingEmail, DeadlineAt: plan.DeadlineAt,
		AdminFallback: !strings.EqualFold(snapshotEmail, actingEmail),
	}
	if plan.TargetTeam || plan.AlsoInviteToTeam {
		teamJob := baseJob
		newSyncs = append(newSyncs, targetSync{
			key: googleGroupSyncKey(plan.TeamID, "", plan.GroupEmail),
			job: teamJob,
		})
	}
	if !plan.TargetTeam {
		channelJob := baseJob
		channelJob.ChannelID = plan.ChannelID
		newSyncs = append(newSyncs, targetSync{
			key: googleGroupSyncKey(plan.TeamID, plan.ChannelID, plan.GroupEmail),
			job: channelJob,
		})
	}
	for _, target := range newSyncs {
		if err := p.verifyGoogleGroupSyncRequester(target.job); err != nil {
			return "", fmt.Errorf("could not verify sync creator's access: %s", googleUserFacingError(err))
		}
		oldData, appErr := p.API.KVGet(target.key)
		if appErr != nil {
			return "", fmt.Errorf("check existing sync: %s", appErr.Error())
		}
		if len(oldData) != 0 {
			return "", fmt.Errorf("this group already has a sync configured for that target")
		}
	}

	savedKeys := make([]string, 0, len(newSyncs))
	for index := range newSyncs {
		target := &newSyncs[index]
		data, err := json.Marshal(target.job)
		if err != nil {
			return "", fmt.Errorf("encode sync settings: %w", err)
		}
		if appErr := p.API.KVSet(target.key, data); appErr != nil {
			for _, key := range savedKeys {
				if deleteErr := p.API.KVDelete(key); deleteErr != nil {
					p.API.LogError("Rollback partial Google Group sync setup failed", "key", key, "error", deleteErr.Error())
				}
			}
			return "", fmt.Errorf("save sync settings: %s", appErr.Error())
		}
		target.initialData = data
		savedKeys = append(savedKeys, target.key)
	}
	defer p.wakeGoogleGroupSync()

	var failures []string
	teamAdded, channelAdded := 0, 0
	for index := range newSyncs {
		target := &newSyncs[index]
		if target.job.ChannelID == "" {
			teamAdded, err = p.syncGoogleGroupTeamMembers(snapshot, target.job.TeamID)
		} else {
			channelAdded, err = p.syncGoogleGroupMembers(snapshot, target.job)
		}
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s sync: %v", syncTargetLabel(target.job), err))
			p.API.LogError("Initial Google Group sync completed with errors",
				"group", target.job.GroupEmail, "team_id", target.job.TeamID, "channel_id", target.job.ChannelID, "error", err.Error())
			continue
		}
		target.job.LastSyncedAt = time.Now().UnixMilli()
		updated, marshalErr := json.Marshal(target.job)
		if marshalErr != nil {
			return "", fmt.Errorf("encode sync status: %w", marshalErr)
		}
		saved, appErr := p.API.KVSetWithOptions(target.key, updated, model.PluginKVSetOptions{Atomic: true, OldValue: target.initialData})
		if appErr != nil || !saved {
			if appErr != nil {
				return "", fmt.Errorf("members were added but the sync status could not be saved: %s", appErr.Error())
			}
			return "", fmt.Errorf("members were added but the sync status changed concurrently; the sync may retry")
		}
	}

	message := fmt.Sprintf("Sync configured for %s until %s UTC (%s).",
		snapshot.Group.Email, time.UnixMilli(plan.DeadlineAt).UTC().Format("2006-01-02"), googleGroupSyncAccountDescription(baseJob))
	if plan.TargetTeam {
		message += fmt.Sprintf(" Added %d team members.", teamAdded)
	} else {
		message += fmt.Sprintf(" Added %d channel members to ~%s.", channelAdded, channel.Name)
		if plan.AlsoInviteToTeam {
			message += fmt.Sprintf(" Added %d team members.", teamAdded)
		}
	}
	if p.getConfiguration().GoogleGroupsSyncMode == "continuous" {
		message += " Future syncs run continuously with the configured delay between operations."
	} else {
		message += " Next runs are scheduled at the 23rd minute of each hour."
	}
	message += " Members are never removed automatically."
	if len(failures) > 0 {
		message += " Some members could not be added: " + strings.Join(failures, "; ") + ". The next scheduled run will retry."
	}
	return message, nil
}

func syncTargetLabel(job googleGroupSync) string {
	if job.ChannelID == "" {
		return "team"
	}
	return "channel"
}

func (p *Plugin) requireGoogleSyncChannelMember(channelID, userID string) error {
	member, appErr := p.API.GetChannelMember(channelID, userID)
	if appErr != nil {
		if appErr.StatusCode == http.StatusNotFound {
			return fmt.Errorf("you are no longer a member of the target channel")
		}
		return fmt.Errorf("check your channel membership: %s", appErr.Error())
	}
	if member == nil {
		return fmt.Errorf("you are no longer a member of the target channel")
	}
	return nil
}

func (p *Plugin) syncGoogleGroupMembers(snapshot *googleDirectoryGroupSnapshot, job googleGroupSync) (int, error) {
	added := 0
	var firstErr error
	for _, member := range snapshot.Members {
		if member.Email == "" || !strings.EqualFold(member.Type, "USER") || p.isGoogleFallbackEmail(member.Email) {
			continue
		}
		user, appErr := p.googleGroupMattermostUser(member.Email)
		if appErr != nil {
			if appErr.StatusCode == http.StatusNotFound {
				continue
			}
			firstErr = firstError(firstErr, fmt.Errorf("find Mattermost user %q: %s", member.Email, appErr.Error()))
			continue
		}
		if user == nil || user.DeleteAt != 0 {
			continue
		}
		teamMember, appErr := p.API.GetTeamMember(job.TeamID, user.Id)
		if appErr != nil {
			if appErr.StatusCode == http.StatusNotFound {
				continue
			}
			firstErr = firstError(firstErr, fmt.Errorf("check team membership for %q: %s", member.Email, appErr.Error()))
			continue
		}
		if teamMember == nil || teamMember.DeleteAt != 0 {
			continue
		}
		channelMember, appErr := p.API.GetChannelMember(job.ChannelID, user.Id)
		if appErr == nil && channelMember != nil {
			continue
		}
		if appErr != nil && appErr.StatusCode != http.StatusNotFound {
			firstErr = firstError(firstErr, fmt.Errorf("check channel membership for %q: %s", member.Email, appErr.Error()))
			continue
		}
		if _, appErr := p.API.AddChannelMember(job.ChannelID, user.Id); appErr != nil {
			memberNow, checkErr := p.API.GetChannelMember(job.ChannelID, user.Id)
			if checkErr == nil && memberNow != nil {
				continue
			}
			firstErr = firstError(firstErr, fmt.Errorf("add %q to channel: %s", member.Email, appErr.Error()))
			continue
		}
		added++
	}
	return added, firstErr
}

func (p *Plugin) syncGoogleGroupTeamMembers(snapshot *googleDirectoryGroupSnapshot, teamID string) (int, error) {
	added := 0
	var firstErr error
	for _, member := range snapshot.Members {
		if member.Email == "" || !strings.EqualFold(member.Type, "USER") || p.isGoogleFallbackEmail(member.Email) {
			continue
		}
		user, appErr := p.googleGroupMattermostUser(member.Email)
		if appErr != nil {
			if appErr.StatusCode != http.StatusNotFound {
				firstErr = firstError(firstErr, fmt.Errorf("find Mattermost user %q: %s", member.Email, appErr.Error()))
			}
			continue
		}
		if user == nil || user.DeleteAt != 0 || user.IsBot {
			continue
		}
		teamMember, appErr := p.API.GetTeamMember(teamID, user.Id)
		if appErr == nil && teamMember != nil && teamMember.DeleteAt == 0 {
			continue
		}
		if appErr != nil && appErr.StatusCode != http.StatusNotFound {
			firstErr = firstError(firstErr, fmt.Errorf("check team membership for %q: %s", member.Email, appErr.Error()))
			continue
		}
		if _, appErr := p.API.CreateTeamMember(teamID, user.Id); appErr != nil {
			memberNow, checkErr := p.API.GetTeamMember(teamID, user.Id)
			if checkErr == nil && memberNow != nil && memberNow.DeleteAt == 0 {
				continue
			}
			firstErr = firstError(firstErr, fmt.Errorf("add %q to team: %s", member.Email, appErr.Error()))
			continue
		}
		added++
	}
	return added, firstErr
}

func (p *Plugin) removeGoogleGroupTeamMembers(snapshot *googleDirectoryGroupSnapshot, teamID, requesterID string) (int, error) {
	removed := 0
	var firstErr error
	for _, member := range snapshot.Members {
		if member.Email == "" || !strings.EqualFold(member.Type, "USER") {
			continue
		}
		user, appErr := p.googleGroupMattermostUser(member.Email)
		if appErr != nil {
			if appErr.StatusCode != http.StatusNotFound {
				firstErr = firstError(firstErr, fmt.Errorf("find Mattermost user %q: %s", member.Email, appErr.Error()))
			}
			continue
		}
		if user == nil || user.Id == requesterID {
			continue
		}
		teamMember, appErr := p.API.GetTeamMember(teamID, user.Id)
		if appErr != nil {
			if appErr.StatusCode != http.StatusNotFound {
				firstErr = firstError(firstErr, fmt.Errorf("check team membership for %q: %s", member.Email, appErr.Error()))
			}
			continue
		}
		if teamMember == nil || teamMember.DeleteAt != 0 {
			continue
		}
		if appErr := p.API.DeleteTeamMember(teamID, user.Id, requesterID); appErr != nil {
			firstErr = firstError(firstErr, fmt.Errorf("remove %q from team: %s", member.Email, appErr.Error()))
			continue
		}
		removed++
	}
	return removed, firstErr
}

func (p *Plugin) removeGoogleGroupChannelMembers(snapshot *googleDirectoryGroupSnapshot, channelID, requesterID string) (int, error) {
	removed := 0
	var firstErr error
	for _, member := range snapshot.Members {
		if member.Email == "" || !strings.EqualFold(member.Type, "USER") {
			continue
		}
		user, appErr := p.googleGroupMattermostUser(member.Email)
		if appErr != nil {
			if appErr.StatusCode != http.StatusNotFound {
				firstErr = firstError(firstErr, fmt.Errorf("find Mattermost user %q: %s", member.Email, appErr.Error()))
			}
			continue
		}
		if user == nil || user.Id == requesterID {
			continue
		}
		channelMember, appErr := p.API.GetChannelMember(channelID, user.Id)
		if appErr != nil {
			if appErr.StatusCode != http.StatusNotFound {
				firstErr = firstError(firstErr, fmt.Errorf("check channel membership for %q: %s", member.Email, appErr.Error()))
			}
			continue
		}
		if channelMember == nil {
			continue
		}
		if appErr := p.API.DeleteChannelMember(channelID, user.Id); appErr != nil {
			firstErr = firstError(firstErr, fmt.Errorf("remove %q from channel: %s", member.Email, appErr.Error()))
			continue
		}
		removed++
	}
	return removed, firstErr
}

func (p *Plugin) googleGroupSyncRecords() ([]googleGroupSyncRecord, error) {
	var jobs []googleGroupSyncRecord
	for page := 0; ; page++ {
		keys, appErr := p.API.KVList(page, 100)
		if appErr != nil {
			return nil, appErr
		}
		for _, key := range keys {
			if !strings.HasPrefix(key, googleGroupSyncPrefix) {
				continue
			}
			data, appErr := p.API.KVGet(key)
			if appErr != nil {
				return nil, appErr
			}
			if len(data) == 0 {
				continue
			}
			var job googleGroupSync
			if err := json.Unmarshal(data, &job); err != nil {
				return nil, fmt.Errorf("decode Google Group sync %q: %w", key, err)
			}
			jobs = append(jobs, googleGroupSyncRecord{Key: key, Data: data, Job: job})
		}
		if len(keys) < 100 {
			break
		}
	}
	return jobs, nil
}

func (p *Plugin) runGoogleGroupSyncJobs(ctx context.Context) {
	p.runGoogleGroupSyncJobsMode(ctx, false)
}

func (p *Plugin) runGoogleGroupSyncJobsMode(ctx context.Context, continuous bool) (wait time.Duration) {
	const retryDelay = 2 * time.Second
	lock, err := cluster.NewMutex(p.API, "google_groups_sync")
	if err != nil {
		p.API.LogError("Create Google Group sync lock failed", "error", err.Error())
		return retryDelay
	}
	lockCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := lock.LockWithContext(lockCtx); err != nil {
		if ctx.Err() == nil {
			p.API.LogError("Lock Google Group sync failed", "error", err.Error())
		}
		return retryDelay
	}
	defer lock.Unlock()

	config := p.getConfiguration()
	if !continuous {
		p.runGoogleUsergroupSyncs(ctx)
		if p.getConfiguration().GoogleGroupsSyncMode == "continuous" {
			return 0
		}
	} else if config.GoogleGroupsSyncMode != "continuous" {
		return 0
	}

	var state googleContinuousSyncState
	if continuous {
		data, appErr := p.API.KVGet(googleGroupContinuousStateKey)
		if appErr != nil {
			p.API.LogError("Read continuous Google Group sync state failed", "error", appErr.Error())
			return retryDelay
		}
		if len(data) > 0 {
			if err := json.Unmarshal(data, &state); err != nil {
				p.API.LogError("Decode continuous Google Group sync state failed", "error", err.Error())
				return retryDelay
			}
		}
		delay := time.Duration(config.GoogleGroupsSyncDelaySeconds) * time.Second
		if delay <= 0 {
			p.API.LogError("Invalid continuous Google Group sync delay")
			return retryDelay
		}
		if state.CompletedAt != 0 {
			if remaining := time.Until(time.Unix(0, state.CompletedAt).Add(delay)); remaining > 0 {
				return remaining
			}
		}
	}

	var keys []string
	for page := 0; ; page++ {
		batch, appErr := p.API.KVList(page, 100)
		if appErr != nil {
			p.API.LogError("List Google Group syncs failed", "error", appErr.Error())
			return retryDelay
		}
		for _, key := range batch {
			if strings.HasPrefix(key, googleGroupSyncPrefix) {
				keys = append(keys, key)
			}
		}
		if len(batch) < 100 {
			break
		}
	}
	var records []googleGroupSyncRecord
	for _, key := range keys {
		if ctx.Err() != nil {
			return retryDelay
		}
		data, appErr := p.API.KVGet(key)
		if appErr != nil {
			p.API.LogError("Read Google Group sync failed", "key", key, "error", appErr.Error())
			continue
		}
		if len(data) == 0 {
			continue
		}
		var job googleGroupSync
		if err := json.Unmarshal(data, &job); err != nil {
			p.API.LogError("Decode Google Group sync failed", "key", key, "error", err.Error())
			continue
		}
		records = append(records, googleGroupSyncRecord{Key: key, Data: data, Job: job})
	}
	if continuous {
		if p.getConfiguration().GoogleGroupsSyncMode != "continuous" {
			return 0
		}
		sort.Slice(records, func(i, j int) bool {
			return googleContinuousSyncOrder(records[i]) < googleContinuousSyncOrder(records[j])
		})
	} else {
		sort.SliceStable(records, func(i, j int) bool {
			return records[i].Job.ChannelID == "" && records[j].Job.ChannelID != ""
		})
	}
	if continuous {
		if len(records) == 0 {
			return 30 * time.Second
		}
		index := sort.Search(len(records), func(i int) bool {
			return googleContinuousSyncOrder(records[i]) > state.Cursor
		})
		if index == len(records) {
			index = 0
		}
		records = records[index : index+1]
		state.Cursor = googleContinuousSyncOrder(records[0])
		defer func() {
			state.CompletedAt = time.Now().UnixNano()
			data, marshalErr := json.Marshal(state)
			if marshalErr != nil {
				p.API.LogError("Encode continuous Google Group sync state failed", "error", marshalErr.Error())
				wait = retryDelay
				return
			}
			if appErr := p.API.KVSet(googleGroupContinuousStateKey, data); appErr != nil {
				p.API.LogError("Save continuous Google Group sync state failed", "error", appErr.Error())
				wait = retryDelay
			}
		}()
	}
	for _, record := range records {
		if ctx.Err() != nil {
			return retryDelay
		}
		if !continuous && p.getConfiguration().GoogleGroupsSyncMode == "continuous" {
			return 0
		}
		key, data, job := record.Key, record.Data, record.Job
		now := time.Now()
		if now.UnixMilli() >= job.DeadlineAt {
			if appErr := p.API.KVDelete(key); appErr != nil {
				p.API.LogError("Expire Google Group sync failed", "group", job.GroupEmail, "error", appErr.Error())
			} else {
				p.API.LogInfo("Google Group sync expired", "group", job.GroupEmail, "channel_id", job.ChannelID)
			}
			continue
		}
		nextSyncAt := googleGroupNextSyncAt(job, now)
		if !continuous && job.LastSyncedAt > 0 && (nextSyncAt == 0 || now.UnixMilli() < nextSyncAt) {
			continue
		}
		if !p.googleGroupTeamAdmin(job.CreatedBy, job.TeamID) {
			p.API.LogError("Scheduled Google Group sync creator is no longer an admin", "group", job.GroupEmail, "channel_id", job.ChannelID)
			continue
		}
		if err := p.verifyGoogleGroupSyncRequester(job); err != nil {
			p.API.LogError("Scheduled Google Group sync creator cannot read group", "group", job.GroupEmail, "channel_id", job.ChannelID, "error", err.Error())
			continue
		}
		if job.AdminFallback {
			adminEmail, ok := p.googleOrganizationEmail(p.getConfiguration().GoogleGroupsSyncAdminEmail)
			if !ok || !strings.EqualFold(adminEmail, job.ActingEmail) {
				p.API.LogError("Scheduled Google Group sync admin account is unavailable or changed", "group", job.GroupEmail, "channel_id", job.ChannelID)
				continue
			}
		}
		var channel *model.Channel
		if job.ChannelID == "" {
			if message := p.googleGroupTeamPermissionMessage(job.CreatedBy, job.TeamID, model.PermissionAddUserToTeam, "add"); message != "" {
				p.API.LogError("Scheduled Google Group team sync creator lacks permission", "group", job.GroupEmail, "team_id", job.TeamID, "error", message)
				continue
			}
			team, appErr := p.API.GetTeam(job.TeamID)
			if appErr != nil && appErr.StatusCode != http.StatusNotFound {
				p.API.LogError("Load team for Google Group sync failed", "group", job.GroupEmail, "team_id", job.TeamID, "error", appErr.Error())
				continue
			}
			if team == nil || team.DeleteAt != 0 {
				if deleteErr := p.API.KVDelete(key); deleteErr != nil {
					p.API.LogError("Delete Google Group sync for inactive team failed", "group", job.GroupEmail, "team_id", job.TeamID, "error", deleteErr.Error())
				}
				continue
			}
		} else {
			var appErr *model.AppError
			channel, appErr = p.API.GetChannel(job.ChannelID)
			if appErr != nil {
				p.API.LogError("Load channel for Google Group sync failed", "group", job.GroupEmail, "channel_id", job.ChannelID, "error", appErr.Error())
				continue
			}
			if channel == nil || channel.DeleteAt != 0 || channel.TeamId != job.TeamID ||
				(channel.Type != model.ChannelTypeOpen && channel.Type != model.ChannelTypePrivate) {
				if deleteErr := p.API.KVDelete(key); deleteErr != nil {
					p.API.LogError("Delete Google Group sync for inactive channel failed", "group", job.GroupEmail, "error", deleteErr.Error())
				}
				continue
			}
		}
		snapshot, err := p.googleGroupSnapshot(job.GroupEmail, job.ActingEmail, true)
		if err != nil {
			p.API.LogError("Read Google Group for scheduled sync failed", "group", job.GroupEmail, "channel_id", job.ChannelID, "error", err.Error())
			continue
		}
		if message := p.googleGroupChannelPermissionMessage(job.CreatedBy, job.TeamID, len(snapshot.Members)); message != "" {
			p.API.LogError("Scheduled Google Group sync creator lacks permission for group size", "group", job.GroupEmail, "channel_id", job.ChannelID, "error", message)
			continue
		}
		if job.ChannelID == "" {
			added, syncErr := p.syncGoogleGroupTeamMembers(snapshot, job.TeamID)
			if syncErr != nil {
				p.API.LogError("Scheduled Google Group team sync completed with errors", "group", job.GroupEmail, "team_id", job.TeamID, "added", added, "error", syncErr.Error())
				continue
			}
		} else {
			added, syncErr := p.syncGoogleGroupMembers(snapshot, job)
			if syncErr != nil {
				p.API.LogError("Scheduled Google Group sync completed with errors", "group", job.GroupEmail, "channel_id", job.ChannelID, "added", added, "error", syncErr.Error())
				continue
			}
		}
		job.GroupName = googleGroupName(snapshot.Group)
		job.LastSyncedAt = time.Now().UnixMilli()
		updated, err := json.Marshal(job)
		if err != nil {
			p.API.LogError("Encode Google Group sync status failed", "group", job.GroupEmail, "error", err.Error())
			continue
		}
		saved, appErr := p.API.KVSetWithOptions(key, updated, model.PluginKVSetOptions{Atomic: true, OldValue: data})
		if appErr != nil || !saved {
			message := "sync record changed before its status could be saved"
			if appErr != nil {
				message = appErr.Error()
			}
			p.API.LogError("Save Google Group sync status failed", "group", job.GroupEmail, "error", message)
		}
	}
	return 0
}
