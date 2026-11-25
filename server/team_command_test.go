package main

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

type teamCommandTestAPI struct {
	plugin.API
	teams       []*model.Team
	memberTeams []*model.Team
	joined      string
	left        string
}

func (a *teamCommandTestAPI) GetTeamByName(name string) (*model.Team, *model.AppError) {
	for _, team := range a.teams {
		if team.Name == name {
			return team, nil
		}
	}
	return nil, nil
}

func (a *teamCommandTestAPI) GetTeamsForUser(string) ([]*model.Team, *model.AppError) {
	return a.memberTeams, nil
}

func (a *teamCommandTestAPI) GetTeams() ([]*model.Team, *model.AppError) {
	return a.teams, nil
}

func (a *teamCommandTestAPI) CreateTeamMember(teamID, _ string) (*model.TeamMember, *model.AppError) {
	a.joined = teamID
	return &model.TeamMember{TeamId: teamID}, nil
}

func (a *teamCommandTestAPI) DeleteTeamMember(teamID, _, _ string) *model.AppError {
	a.left = teamID
	return nil
}

func TestTeamCommandJoinLeaveAndList(t *testing.T) {
	member := &model.Team{Id: "member-id", Name: "member", DisplayName: "Member", Description: "Member team", AllowOpenInvite: false}
	public := &model.Team{Id: "public-id", Name: "public", DisplayName: "Public", Description: "Public team", AllowOpenInvite: true}
	private := &model.Team{Id: "private-id", Name: "private", DisplayName: "Private", Description: "Private team", AllowOpenInvite: false}
	api := &teamCommandTestAPI{teams: []*model.Team{private, public, member}, memberTeams: []*model.Team{member, private}}
	p := &Plugin{}
	p.SetAPI(api)

	response, err := p.ExecuteCommand(nil, &model.CommandArgs{UserId: "user", Command: "/team join public"})
	if err != nil || !strings.Contains(response.Text, "Joined Public") || api.joined != public.Id {
		t.Fatalf("join response=%+v err=%v joined=%s", response, err, api.joined)
	}

	response, err = p.ExecuteCommand(nil, &model.CommandArgs{UserId: "user", Command: "/team leave member"})
	if err != nil || !strings.Contains(response.Text, "Left Member") || api.left != member.Id {
		t.Fatalf("leave response=%+v err=%v left=%s", response, err, api.left)
	}

	response, err = p.ExecuteCommand(nil, &model.CommandArgs{UserId: "user", Command: "/team list"})
	wantSections := "#### Teams you can join\n\n**Public**, `public`\n> Public team\n> \n> Join with `/team join public`\n\n---\n\n#### Your teams\n\n**Member**, `member`\n> Member team\n\n**Private**, `private`\n> Private team"
	if err != nil || response.Text != wantSections {
		t.Fatalf("list response=%+v err=%v", response, err)
	}
	if strings.Contains(response.Text, "Join with `/team join member`") || strings.Contains(response.Text, "Join with `/team join private`") {
		t.Fatalf("joined teams should not include join instructions: %s", response.Text)
	}
}

func TestTeamCommandRejectsPrivateTeamJoin(t *testing.T) {
	private := &model.Team{Id: "private-id", Name: "private", DisplayName: "Private", AllowOpenInvite: false}
	api := &teamCommandTestAPI{teams: []*model.Team{private}}
	p := &Plugin{}
	p.SetAPI(api)

	response, err := p.ExecuteCommand(nil, &model.CommandArgs{UserId: "user", Command: "/team join private"})
	if err != nil || !strings.Contains(response.Text, "only join public") || api.joined != "" {
		t.Fatalf("private join response=%+v err=%v joined=%s", response, err, api.joined)
	}
}

type memberCommandTestAPI struct {
	plugin.API
	user        *model.User
	team        *model.Team
	permission  *model.Permission
	added       string
	removed     string
	addedTeam   string
	removedTeam string
	member      bool
}

func (a *memberCommandTestAPI) HasPermissionToTeam(_ string, _ string, permission *model.Permission) bool {
	return permission == a.permission
}

func (a *memberCommandTestAPI) GetUserByUsername(string) (*model.User, *model.AppError) {
	return a.user, nil
}

func (a *memberCommandTestAPI) GetTeamByName(string) (*model.Team, *model.AppError) {
	return a.team, nil
}

func (a *memberCommandTestAPI) GetChannel(string) (*model.Channel, *model.AppError) {
	return &model.Channel{TeamId: "team", Type: model.ChannelTypeOpen}, nil
}

func (a *memberCommandTestAPI) GetTeam(string) (*model.Team, *model.AppError) {
	return a.team, nil
}

func (a *memberCommandTestAPI) GetTeamMember(_, _ string) (*model.TeamMember, *model.AppError) {
	if a.member {
		return &model.TeamMember{}, nil
	}
	return nil, &model.AppError{StatusCode: 404, Id: "app.team_member.get_by_teamid_userid.app_error"}
}

func (a *memberCommandTestAPI) CreateTeamMember(teamID, userID string) (*model.TeamMember, *model.AppError) {
	a.addedTeam = teamID
	a.added = userID
	a.member = true
	return &model.TeamMember{}, nil
}

func (a *memberCommandTestAPI) DeleteTeamMember(teamID, userID, _ string) *model.AppError {
	a.removedTeam = teamID
	a.removed = userID
	a.member = false
	return nil
}

func TestMemberCommandRequiresOperationPermissionAndSupportsAtSign(t *testing.T) {
	api := &memberCommandTestAPI{user: &model.User{Id: "target-id", Username: "target"}, team: &model.Team{Id: "other-team", Name: "other", DisplayName: "Other"}}
	p := &Plugin{}
	p.SetAPI(api)

	response, err := p.ExecuteCommand(nil, &model.CommandArgs{UserId: "admin", TeamId: "team", Command: "/member add @target"})
	if err != nil || !strings.Contains(response.Text, "permission") || api.added != "" {
		t.Fatalf("unauthorized add response=%+v err=%v added=%s", response, err, api.added)
	}

	api.permission = model.PermissionAddUserToTeam
	response, err = p.ExecuteCommand(nil, &model.CommandArgs{UserId: "admin", TeamId: "team", Command: "/member add target"})
	if err != nil || !strings.Contains(response.Text, "Added @target") || api.added != "target-id" {
		t.Fatalf("add response=%+v err=%v added=%s", response, err, api.added)
	}

	api.permission = model.PermissionRemoveUserFromTeam
	response, err = p.ExecuteCommand(nil, &model.CommandArgs{UserId: "admin", TeamId: "team", Command: "/member remove @target"})
	if err != nil || !strings.Contains(response.Text, "Removed @target") || api.removed != "target-id" {
		t.Fatalf("remove response=%+v err=%v removed=%s", response, err, api.removed)
	}

	api.permission = model.PermissionAddUserToTeam
	response, err = p.ExecuteCommand(nil, &model.CommandArgs{UserId: "admin", Command: "/member add @target other"})
	if err != nil || !strings.Contains(response.Text, "team Other") || api.added != "target-id" {
		t.Fatalf("explicit-team add response=%+v err=%v added=%s", response, err, api.added)
	}
}
