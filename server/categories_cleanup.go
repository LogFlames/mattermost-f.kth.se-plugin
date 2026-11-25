package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

const (
	categoryCleanupArchive = "archive"
	categoryCleanupLeave   = "leave"
	categoryCleanupSweep   = "sweep"
)

func (p *Plugin) ChannelWillBeArchived(_ *plugin.Context, channel *model.Channel) string {
	if channel == nil || channel.TeamId == "" || channel.DefaultCategoryName == "" ||
		(channel.Type != model.ChannelTypeOpen && channel.Type != model.ChannelTypePrivate) {
		return ""
	}
	job := &categoryJob{
		Action: categoryCleanupArchive, ChannelID: channel.Id, TeamID: channel.TeamId,
		OldName: channel.DefaultCategoryName, CreatedAt: time.Now().UnixMilli(),
	}
	if err := p.saveCategoryJob(categoryJobPrefix+model.NewId(), job); err != nil {
		p.API.LogError("Cannot queue archived channel category cleanup", "channel_id", channel.Id, "error", err.Error())
		return "Could not schedule category cleanup. Please try again."
	}
	return ""
}

func (p *Plugin) queueLeftChannelCategory(member *model.ChannelMember) {
	job := &categoryJob{
		Action: categoryCleanupLeave, ChannelID: member.ChannelId, UserID: member.UserId,
		CreatedAt: time.Now().UnixMilli(),
	}
	channel, err := p.API.GetChannel(member.ChannelId)
	if err == nil {
		if channel.TeamId == "" || channel.DefaultCategoryName == "" ||
			(channel.Type != model.ChannelTypeOpen && channel.Type != model.ChannelTypePrivate) {
			return
		}
		job.TeamID, job.OldName = channel.TeamId, channel.DefaultCategoryName
	} else {
		p.API.LogError("Cannot look up left channel for category cleanup; queued for retry", "channel_id", member.ChannelId, "error", err.Error())
	}
	if err := p.saveCategoryJob(categoryJobPrefix+model.NewId(), job); err != nil {
		p.API.LogError("Cannot queue left channel category cleanup", "channel_id", member.ChannelId, "user_id", member.UserId, "error", err.Error())
	}
}

func (p *Plugin) queueTeamCategoryCleanupJobs(key string, job *categoryJob) (bool, error) {
	members, appErr := p.API.GetTeamMembers(job.TeamID, job.Page, categoryPageSize)
	if appErr != nil {
		return false, appErr
	}
	for _, member := range members {
		if member.DeleteAt != 0 {
			continue
		}
		childKey := key + "_clean_" + member.UserId
		if data, err := p.API.KVGet(childKey); err != nil {
			return false, err
		} else if len(data) != 0 {
			continue
		}
		child := &categoryJob{
			Action: categoryCleanupSweep, TeamID: job.TeamID, UserID: member.UserId,
			ParentKey: key,
		}
		if err := p.saveCategoryJob(childKey, child); err != nil {
			return false, err
		}
	}
	if len(members) < categoryPageSize {
		job.Scanned = true
	} else {
		job.Page++
	}
	return false, p.saveCategoryJob(key, job)
}

func (p *Plugin) runCategoryCleanupJob(ctx context.Context, rest *categoryREST, key string, job *categoryJob) (bool, error) {
	if job.Action == categoryCleanupSweep {
		data, err := p.API.KVGet(job.ParentKey)
		if err != nil {
			return false, err
		}
		if len(data) == 0 {
			return false, errors.New("category cleanup parent is missing")
		}
		var parent categoryJob
		if err := json.Unmarshal(data, &parent); err != nil {
			return false, err
		}
		return true, p.cleanupMemberCategories(ctx, rest, key, job, job.UserID, parent.Archived)
	}
	if job.Action != categoryCleanupArchive && job.Action != categoryCleanupLeave {
		return false, fmt.Errorf("unknown category cleanup action %q", job.Action)
	}
	channel, appErr := p.API.GetChannel(job.ChannelID)
	if appErr != nil {
		if appErr.StatusCode == http.StatusNotFound {
			return true, nil
		}
		return false, appErr
	}
	if job.TeamID == "" {
		job.TeamID, job.OldName = channel.TeamId, channel.DefaultCategoryName
	}
	if job.TeamID == "" || job.OldName == "" ||
		(channel.Type != model.ChannelTypeOpen && channel.Type != model.ChannelTypePrivate) {
		return true, nil
	}
	if job.Action == categoryCleanupLeave {
		return true, p.cleanupMemberCategories(ctx, rest, key, job, job.UserID, nil)
	}
	if channel.DeleteAt == 0 {
		if job.Confirmed {
			return true, nil
		}
		return time.Now().UnixMilli()-job.CreatedAt > time.Minute.Milliseconds(), nil
	}
	job.Confirmed = true
	if job.Pending == nil {
		job.Pending = make(map[string][]string)
	}
	if err := p.saveCategoryJob(key, job); err != nil {
		return false, err
	}
	var failures error
	process := func(userID string) error {
		if err := p.cleanupMemberCategories(ctx, rest, key, job, userID, nil); err != nil {
			failures = errors.Join(failures, fmt.Errorf("user %s: %w", userID, err))
			return nil
		}
		delete(job.Pending, userID)
		return p.saveCategoryJob(key, job)
	}
	for userID := range job.Pending {
		if err := process(userID); err != nil {
			return false, err
		}
	}
	for !job.Scanned {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		members, err := p.API.GetChannelMembers(job.ChannelID, job.Page, categoryPageSize)
		if err != nil {
			return false, err
		}
		for _, member := range members {
			if _, pending := job.Pending[member.UserId]; pending {
				continue
			}
			job.Pending[member.UserId] = nil
			if err := p.saveCategoryJob(key, job); err != nil {
				return false, err
			}
			if err := process(member.UserId); err != nil {
				return false, err
			}
		}
		if len(members) < categoryPageSize {
			job.Scanned = true
		} else {
			job.Page++
		}
		if err := p.saveCategoryJob(key, job); err != nil {
			return false, err
		}
	}
	return len(job.Pending) == 0, failures
}

func (p *Plugin) cleanupMemberCategories(ctx context.Context, rest *categoryREST, key string, job *categoryJob, userID string, archived map[string]string) error {
	member, memberErr := p.API.GetTeamMember(job.TeamID, userID)
	if memberErr != nil {
		if memberErr.StatusCode == http.StatusNotFound {
			return nil
		}
		return memberErr
	}
	if member.DeleteAt != 0 {
		return nil
	}
	categories, appErr := p.API.GetChannelSidebarCategories(userID, job.TeamID)
	if appErr != nil {
		return appErr
	}
	for _, category := range categories.Categories {
		if err := ctx.Err(); err != nil {
			return err
		}
		if category.Type != model.SidebarCategoryCustom {
			continue
		}
		if job.Action == categoryCleanupSweep {
			tracked := false
			for id, name := range archived {
				if !strings.EqualFold(name, category.DisplayName) {
					continue
				}
				channel, err := p.API.GetChannel(id)
				if err != nil {
					return err
				}
				if channel.TeamId == job.TeamID && channel.DeleteAt != 0 &&
					strings.EqualFold(channel.DefaultCategoryName, category.DisplayName) {
					tracked = true
					break
				}
			}
			if !tracked {
				continue
			}
		} else if !strings.EqualFold(category.DisplayName, job.OldName) {
			continue
		}
		name := category.DisplayName
		// Re-read before deletion so earlier writes or concurrent edits are seen.
		current, err := p.API.GetChannelSidebarCategories(userID, job.TeamID)
		if err != nil {
			return err
		}
		index := slices.IndexFunc(current.Categories, func(c *model.SidebarCategoryWithChannels) bool { return c.Id == category.Id })
		if index < 0 {
			continue
		}
		category = current.Categories[index]
		if category.Type != model.SidebarCategoryCustom || !strings.EqualFold(category.DisplayName, name) {
			continue
		}
		hasActive := false
		for _, id := range category.Channels {
			if job.Action == categoryCleanupLeave && id == job.ChannelID {
				continue
			}
			channel, err := p.API.GetChannel(id)
			if err != nil {
				return err
			}
			if channel.DeleteAt == 0 {
				hasActive = true
				break
			}
		}
		if hasActive {
			continue
		}
		if job.Action == categoryCleanupLeave {
			_, err := p.API.GetChannelMember(job.ChannelID, userID)
			if err == nil {
				return nil
			}
			if err.StatusCode != http.StatusNotFound {
				return err
			}
		}
		if job.Action == categoryCleanupArchive {
			channel, err := p.API.GetChannel(job.ChannelID)
			if err != nil {
				return err
			}
			if channel.DeleteAt == 0 {
				return nil
			}
		}
		deleted, deleteErr := rest.deleteCategory(ctx, userID, job.TeamID, category.Id)
		if deleteErr != nil {
			return deleteErr
		}
		if deleted {
			job.Deleted++
			if err := p.saveCategoryJob(key, job); err != nil {
				return err
			}
		}
	}
	return nil
}
