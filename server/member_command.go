package main

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

func (p *Plugin) memberCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	fields := strings.Fields(args.Command)
	if (len(fields) != 3 && len(fields) != 4) || (fields[1] != "add" && fields[1] != "remove") {
		response.Text = "Usage: /member add|remove @<username> [team]"
		return response, nil
	}
	teamID := args.TeamId

	if len(fields) == 3 {
		channel, err := p.API.GetChannel(args.ChannelId)
		if err != nil {
			return nil, err
		}
		if channel.Type != model.ChannelTypeOpen && channel.Type != model.ChannelTypePrivate {
			response.Text = "Specify the team or run /member from a team channel."
			return response, nil
		}
		if channel.TeamId != args.TeamId {
			response.Text = "TeamID mismatch between team and channel."
			return response, nil
		}
	}
	if len(fields) == 4 {
		team, err := p.API.GetTeamByName(fields[3])
		if err != nil {
			return nil, err
		}
		teamID = team.Id
	}
	team, err := p.API.GetTeam(teamID)
	if err != nil {
		return nil, err
	}
	if team == nil || team.DeleteAt != 0 {
		response.Text = fmt.Sprintf("Team %q was not found.", team.Name)
		return response, nil
	}
	teamID = team.Id
	teamDescription := fmt.Sprintf("team %s / `%s`", team.DisplayName, team.Name)
	if teamID == "" {
		response.Text = "Specify the team or run /member from a team channel."
		return response, nil
	}

	permission := model.PermissionAddUserToTeam
	action := "add"
	if fields[1] == "remove" {
		permission = model.PermissionRemoveUserFromTeam
		action = "remove"
	}
	if !p.API.HasPermissionToTeam(args.UserId, teamID, permission) {
		response.Text = fmt.Sprintf("You do not have permission to %s members in %s.", action, teamDescription)
		return response, nil
	}

	username := strings.TrimPrefix(fields[2], "@")
	if username == "" {
		response.Text = "Usage: /member add|remove @<username> [team]"
		return response, nil
	}
	user, err := p.API.GetUserByUsername(username)
	if err != nil && !(err.StatusCode == 404 && err.Id == "app.user.get_by_username.app_error") {
		return nil, err
	}
	if (err != nil && err.StatusCode == 404) || user == nil || user.DeleteAt != 0 {
		response.Text = fmt.Sprintf("User %q was not found.", username)
		return response, nil
	}

	if fields[1] == "add" {
		teamMember, err := p.API.GetTeamMember(teamID, user.Id)
		if err != nil && !(err.StatusCode == 404 && err.Id == "app.team_member.get.app_error") {
			return nil, err
		} // ignore 404 errors
		if teamMember != nil && teamMember.DeleteAt == 0 {
			response.Text = fmt.Sprintf("@%s is already a member of %s.", user.Username, teamDescription)
			return response, nil
		}
		if _, err := p.API.CreateTeamMember(teamID, user.Id); err != nil {
			return nil, err
		}
		response.Text = fmt.Sprintf("Added @%s to %s.", user.Username, teamDescription)
		return response, nil
	} else {
		teamMember, err := p.API.GetTeamMember(teamID, user.Id)
		if err != nil && !(err.StatusCode == 404 && err.Id == "app.team_member.get.app_error") {
			return nil, err
		}
		if (err != nil && err.StatusCode == 404) || teamMember == nil || teamMember.DeleteAt != 0 {
			response.Text = fmt.Sprintf("@%s is not a member of %s.", user.Username, teamDescription)
			return response, nil
		}
		if err := p.API.DeleteTeamMember(teamID, user.Id, args.UserId); err != nil {
			return nil, err
		}
		response.Text = fmt.Sprintf("Removed @%s from %s.", user.Username, teamDescription)
		return response, nil
	}
}
