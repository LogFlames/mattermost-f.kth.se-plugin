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
	teamDescription := "the team"
	if len(fields) == 4 {
		team, err := p.API.GetTeamByName(fields[3])
		if err != nil {
			return nil, err
		}
		if team == nil || team.DeleteAt != 0 {
			response.Text = fmt.Sprintf("Team %q was not found.", fields[3])
			return response, nil
		}
		teamID = team.Id
		teamDescription = fmt.Sprintf("team %s (`%s`)", team.DisplayName, team.Name)
	}
	if teamID == "" {
		response.Text = "Run /member from a team."
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
	if err != nil {
		return nil, err
	}
	if user == nil || user.DeleteAt != 0 {
		response.Text = fmt.Sprintf("User %q was not found.", username)
		return response, nil
	}

	if fields[1] == "add" {
		if _, err := p.API.CreateTeamMember(teamID, user.Id); err != nil {
			return nil, err
		}
		response.Text = fmt.Sprintf("Added @%s to %s.", user.Username, teamDescription)
		return response, nil
	}
	if err := p.API.DeleteTeamMember(teamID, user.Id, args.UserId); err != nil {
		return nil, err
	}
	response.Text = fmt.Sprintf("Removed @%s from %s.", user.Username, teamDescription)
	return response, nil
}
