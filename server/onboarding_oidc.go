package main

import "github.com/mattermost/mattermost/server/public/model"

func (p *Plugin) OIDCOnboarding_UserCreated(user *model.User) error {
	if user == nil || user.AuthService != model.ServiceOpenid {
		return nil
	}

	configuration := p.getConfiguration()
	teams, err := p.getTeamsFromTeamIDs(configuration.OIDCOnboarding_DefaultTeams)
	if err != nil {
		return err
	}
	return p.addUserToTeams(user.Id, teams)
}
