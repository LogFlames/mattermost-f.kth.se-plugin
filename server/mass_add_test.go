package main

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

type massAddDisconnectedAPI struct{ plugin.API }

func (*massAddDisconnectedAPI) GetChannel(string) (*model.Channel, *model.AppError) {
	// Match the RPC client's zero-value returns after a transport failure.
	return nil, nil
}

func TestMassAddDisconnectedAPI(t *testing.T) {
	p := &Plugin{}
	p.SetAPI(&massAddDisconnectedAPI{})
	response, err := p.ExecuteCommand(nil, &model.CommandArgs{
		Command: "/mass_add", ChannelId: "channel", TeamId: "team", UserId: "admin",
	})
	if err != nil || response == nil || response.ResponseType != model.CommandResponseTypeEphemeral || !strings.Contains(response.Text, "Could not load this channel") {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}
