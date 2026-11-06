package main

import (
	"fmt"

	"github.com/mattermost/mattermost/server/public/model"
)

func (p *Plugin) notifyGoogleMigration(user *model.User, email, temporaryPassword string) error {
	configuration := p.getConfiguration()
	if configuration.GoogleMigrationSendEphemeral {
		message := fmt.Sprintf("Your Mattermost account has been migrated to Google Workspace. Sign in with `%s`.", email)
		if temporaryPassword != "" {
			message += fmt.Sprintf(" Your temporary password is `%s`; Google will require you to change it at first login.", temporaryPassword)
		}
		if post := p.API.SendEphemeralPost(user.Id, &model.Post{UserId: user.Id, Message: message}); post == nil {
			return fmt.Errorf("Mattermost did not create the migration notification")
		}
	}
	if configuration.GoogleMigrationSendEmail {
		return p.sendGoogleMigrationEmail(user.Email, email, temporaryPassword)
	}
	return nil
}

func (p *Plugin) sendGoogleMigrationEmail(recipient, email, temporaryPassword string) error {
	return fmt.Errorf("Google migration email notifications are not implemented")
}
