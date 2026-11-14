package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

func (p *Plugin) teamCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	fields := strings.Fields(args.Command)
	if len(fields) < 2 {
		response.Text = "Usage: /team join <team> | /team leave <team> | /team list"
		return response, nil
	}

	switch fields[1] {
	case "join":
		if len(fields) != 3 {
			response.Text = "Usage: /team join <team>"
			return response, nil
		}
		return p.joinTeamCommand(args, fields[2])
	case "leave":
		if len(fields) != 3 {
			response.Text = "Usage: /team leave <team>"
			return response, nil
		}
		return p.leaveTeamCommand(args, fields[2])
	case "list":
		if len(fields) != 2 {
			response.Text = "Usage: /team list"
			return response, nil
		}
		return p.listTeamsCommand(args)
	default:
		response.Text = "Usage: /team join <team> | /team leave <team> | /team list"
		return response, nil
	}
}

func (p *Plugin) joinTeamCommand(args *model.CommandArgs, teamName string) (*model.CommandResponse, *model.AppError) {
	team, err := p.API.GetTeamByName(teamName)
	if err != nil {
		return nil, err
	}
	if team == nil || team.DeleteAt != 0 {
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: fmt.Sprintf("Team %q was not found.", teamName)}, nil
	}
	if team.AllowOpenInvite == false {
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: "You can only join public teams with this command."}, nil
	}
	memberTeams, err := p.API.GetTeamsForUser(args.UserId)
	if err != nil {
		return nil, err
	}
	for _, memberTeam := range memberTeams {
		if memberTeam != nil && memberTeam.Id == team.Id {
			return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: fmt.Sprintf("You are already a member of %s (`%s`).", team.DisplayName, team.Name)}, nil
		}
	}
	if _, err := p.API.CreateTeamMember(team.Id, args.UserId); err != nil {
		return nil, err
	}
	return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: fmt.Sprintf("Joined %s (`%s`).", team.DisplayName, team.Name)}, nil
}

func (p *Plugin) leaveTeamCommand(args *model.CommandArgs, teamName string) (*model.CommandResponse, *model.AppError) {
	team, err := p.API.GetTeamByName(teamName)
	if err != nil {
		return nil, err
	}
	if team == nil || team.DeleteAt != 0 {
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: fmt.Sprintf("Team %q was not found.", teamName)}, nil
	}
	memberTeams, err := p.API.GetTeamsForUser(args.UserId)
	if err != nil {
		return nil, err
	}
	isMember := false
	for _, memberTeam := range memberTeams {
		if memberTeam != nil && memberTeam.Id == team.Id {
			isMember = true
			break
		}
	}
	if !isMember {
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: fmt.Sprintf("You are not a member of %s (`%s`).", team.DisplayName, team.Name)}, nil
	}
	if err := p.API.DeleteTeamMember(team.Id, args.UserId, args.UserId); err != nil {
		return nil, err
	}
	return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: fmt.Sprintf("Left %s (`%s`).", team.DisplayName, team.Name)}, nil
}

func (p *Plugin) listTeamsCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	memberTeams, err := p.API.GetTeamsForUser(args.UserId)
	if err != nil {
		return nil, err
	}
	allTeams, err := p.API.GetTeams()
	if err != nil {
		return nil, err
	}

	memberIDs := make(map[string]bool, len(memberTeams))
	teams := make(map[string]*model.Team, len(memberTeams)+len(allTeams))
	for _, team := range memberTeams {
		if team != nil && team.DeleteAt == 0 {
			memberIDs[team.Id] = true
			teams[team.Id] = team
		}
	}
	for _, team := range allTeams {
		if team == nil || team.DeleteAt != 0 {
			continue
		}
		if memberIDs[team.Id] || team.AllowOpenInvite == true { // team.AllowOpenInvite <=> public teams
			teams[team.Id] = team
		}
	}

	joinableTeams := make([]*model.Team, 0, len(teams))
	memberTeamsToList := make([]*model.Team, 0, len(teams))
	for _, team := range teams {
		if memberIDs[team.Id] {
			memberTeamsToList = append(memberTeamsToList, team)
		} else {
			joinableTeams = append(joinableTeams, team)
		}
	}
	sortTeams := func(teams []*model.Team) {
		sort.Slice(teams, func(i, j int) bool {
			if teams[i].DisplayName == teams[j].DisplayName {
				return teams[i].Name < teams[j].Name
			}
			return teams[i].DisplayName < teams[j].DisplayName
		})
	}
	sortTeams(joinableTeams)
	sortTeams(memberTeamsToList)

	renderTeams := func(teams []*model.Team, joinable bool) []string {
		lines := make([]string, 0, len(teams)*4)
		for _, team := range teams {
			lines = append(lines, fmt.Sprintf("**%s**, `%s`", team.DisplayName, team.Name))
			if team.Description != "" {
				lines = append(lines, fmt.Sprintf("> %s", team.Description))
			}
			if joinable {
				lines = append(lines, "> ", fmt.Sprintf("> Join with `/team join %s`", team.Name))
			}
			lines = append(lines, "")
		}
		return lines
	}

	lines := []string{}
	if len(joinableTeams) == 0 && len(memberTeamsToList) == 0 {
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: "No teams found."}, nil
	}
	lines = append(lines, "#### Teams you can join", "")
	if len(joinableTeams) > 0 {
		lines = append(lines, renderTeams(joinableTeams, true)...)
	} else {
		lines = append(lines, "There are no public teams that you are not already a part of.", "")
	}
	if len(memberTeamsToList) > 0 {
		lines = append(lines, "---", "", "#### Your teams", "")
		lines = append(lines, renderTeams(memberTeamsToList, false)...)
	}
	return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: strings.TrimSpace(strings.Join(lines, "\n"))}, nil
}
