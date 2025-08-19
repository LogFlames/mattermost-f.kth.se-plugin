package main

import (
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

func (p *Plugin) MassAdd_GetCommand() *model.Command {
	return &model.Command{
		Trigger:          "mass_add",
		AutoComplete:     true,
		AutoCompleteHint: "",
		AutoCompleteDesc: "Add all users of team to this channel",
	}
}

func (p *Plugin) MassAdd_ExecuteCommand(c *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	channel, err := p.API.GetChannel(args.ChannelId)
	if err != nil {
		return nil, err
	}

	hasPerm := ((channel.Type == model.ChannelTypePrivate && p.API.HasPermissionToChannel(args.UserId, args.ChannelId, model.PermissionManagePrivateChannelMembers)) ||
		(channel.Type == model.ChannelTypeOpen && p.API.HasPermissionToChannel(args.UserId, args.ChannelId, model.PermissionManagePublicChannelMembers)))

	if !hasPerm {
		post := &model.Post{
			UserId:    p.pluginBot.UserId,
			ChannelId: args.ChannelId,
			Message:   "You do not have permission to add users to this channel.",
			Type:      model.PostTypeEphemeral,
		}
		_ = p.API.SendEphemeralPost(args.UserId, post)
		return &model.CommandResponse{}, nil
	}

	users, err := p.API.GetUsersInTeam(args.TeamId, 0, 10000)
	if err != nil {
		return nil, err
	}

	for _, user := range users {
		if user.Id == args.UserId {
			continue // skip the command issuer
		}
		if _, err := p.API.AddUserToChannel(args.ChannelId, user.Id, args.UserId); err != nil {
			return nil, err
		}
	}

	post := &model.Post{
		UserId:    p.pluginBot.UserId,
		ChannelId: args.ChannelId,
		Message:   "All users of team added to channel.",
		Type:      model.PostTypeEphemeral,
	}
	_ = p.API.SendEphemeralPost(args.UserId, post)

	return &model.CommandResponse{}, nil
}
