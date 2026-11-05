package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

type onboardingCategoryTestAPI struct {
	*adminTestAPI
}

func (a *onboardingCategoryTestAPI) GetChannelsForTeamForUser(teamID, userID string, includeDeleted bool) ([]*model.Channel, *model.AppError) {
	var result []*model.Channel
	for _, channel := range a.channels {
		if channel.TeamId != teamID || (!includeDeleted && channel.DeleteAt != 0) {
			continue
		}
		if _, err := a.GetChannelMember(channel.Id, userID); err == nil {
			result = append(result, &channel)
		}
	}
	return result, nil
}

func (a *onboardingCategoryTestAPI) GetPublicChannelsForTeam(teamID string, page, perPage int) ([]*model.Channel, *model.AppError) {
	var result []*model.Channel
	for _, channel := range a.channels {
		if channel.TeamId == teamID && channel.Type == model.ChannelTypeOpen && channel.DeleteAt == 0 {
			result = append(result, &channel)
		}
	}
	slices.SortFunc(result, func(a, b *model.Channel) int { return strings.Compare(a.Id, b.Id) })
	return result[min(page*perPage, len(result)):min((page+1)*perPage, len(result))], nil
}

func (a *onboardingCategoryTestAPI) GetChannelSidebarCategories(userID, teamID string) (*model.OrderedSidebarCategories, *model.AppError) {
	result := &model.OrderedSidebarCategories{}
	for _, category := range a.categories[userID] {
		if category.TeamId == teamID {
			result.Categories = append(result.Categories, cloneCategory(category))
		}
	}
	return result, nil
}

func TestInternationalOnboardingPlacesBuiltInDefaultChannels(t *testing.T) {
	p, base := newAdminTestPlugin(t)
	api := &onboardingCategoryTestAPI{adminTestAPI: base}
	p.SetAPI(api)
	user := &model.User{Id: "new-user", Username: "new-user", Email: "new@example.org", AuthService: model.UserAuthServiceSaml}
	api.users = []*model.User{user}
	api.settings["internationalstudents_onoffbool"] = true
	api.settings["internationalstudents_defaultenglishteams"] = []string{"fint"}
	api.settings["internationalstudents_defaultswedishteams"] = []string{"fysiksektionen"}
	api.settings["internationalstudents_defaultchannels"] = []string{"events", "info-eng"}
	api.settings["keycloakclientid"] = "test-client"
	api.settings["keycloakclientsecret"] = "test-secret"
	if err := p.OnConfigurationChange(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			_, _ = w.Write([]byte(`{"access_token":"test-token"}`))
		} else if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[{"attributes":{"isActuallyInternational":["Yes"]}}]`))
		} else {
			t.Errorf("unexpected Keycloak request: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	oldURL := keycloakURL
	keycloakURL = server.URL
	t.Cleanup(func() { keycloakURL = oldURL })
	api.channels = map[string]model.Channel{
		"fint-town":  {Id: "fint-town", TeamId: "fint", Name: "town-square", Type: model.ChannelTypeOpen, DefaultCategoryName: "International Hub"},
		"fint-off":   {Id: "fint-off", TeamId: "fint", Name: "off-topic", Type: model.ChannelTypeOpen, DefaultCategoryName: "International Hub"},
		"fy-town":    {Id: "fy-town", TeamId: "fysiksektionen", Name: "town-square", Type: model.ChannelTypeOpen, DefaultCategoryName: "Fysiksektionen"},
		"events":     {Id: "events", TeamId: "fysiksektionen", Name: "events", Type: model.ChannelTypeOpen, DefaultCategoryName: "Physics Chapter"},
		"info-eng":   {Id: "info-eng", TeamId: "fysiksektionen", Name: "info-eng", Type: model.ChannelTypePrivate, DefaultCategoryName: "Physics Chapter"},
		"no-default": {Id: "no-default", TeamId: "fint", Name: "no-default", Type: model.ChannelTypeOpen},
		"not-joined": {Id: "not-joined", TeamId: "fint", Name: "not-joined", Type: model.ChannelTypeOpen, DefaultCategoryName: "Do not join"},
		"archived":   {Id: "archived", TeamId: "fint", Name: "archived", Type: model.ChannelTypeOpen, DefaultCategoryName: "Old", DeleteAt: 1},
	}
	api.channelMembers = map[string]model.ChannelMembers{}
	for id := range api.channels {
		if id != "not-joined" {
			api.channelMembers[id] = model.ChannelMembers{
				{UserId: user.Id, ChannelId: id, NotifyProps: model.StringMap{model.MarkUnreadNotifyProp: model.ChannelMarkUnreadMention}},
				{UserId: "existing-user", ChannelId: id},
			}
		}
	}
	api.categories = map[string][]*model.SidebarCategoryWithChannels{}
	for _, entry := range []struct {
		team     string
		channels []string
	}{
		{"fint", []string{"fint-town", "fint-off", "no-default", "archived"}},
		{"fysiksektionen", []string{"fy-town", "events", "info-eng"}},
	} {
		category := testCategory(entry.team+"-channels", "Channels", model.SidebarCategoryChannels, entry.channels...)
		category.UserId, category.TeamId = user.Id, entry.team
		api.categories[user.Id] = append(api.categories[user.Id], category)
		existing := testCategory(entry.team+"-favorites", "Favorites", model.SidebarCategoryFavorites, entry.channels...)
		existing.UserId, existing.TeamId = "existing-user", entry.team
		api.categories["existing-user"] = append(api.categories["existing-user"], existing)
	}
	existingBefore, _ := json.Marshal(api.categories["existing-user"])
	membersBefore, _ := json.Marshal(api.channelMembers)
	p.UserHasBeenCreated(nil, user)
	p.runCategoryJobs(context.Background())
	got := map[string][]string{}
	for _, category := range api.categories[user.Id] {
		channels := append([]string{}, category.Channels...)
		slices.Sort(channels)
		got[category.TeamId+"/"+category.DisplayName] = channels
	}
	want := map[string][]string{
		"fint/Channels":                  {"archived", "no-default"},
		"fint/International Hub":         {"fint-off", "fint-town"},
		"fysiksektionen/Channels":        {},
		"fysiksektionen/Fysiksektionen":  {"fy-town"},
		"fysiksektionen/Physics Chapter": {"events", "info-eng"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wrong onboarding sidebar placement:\ngot  %v\nwant %v", got, want)
	}
	existingAfter, _ := json.Marshal(api.categories["existing-user"])
	membersAfter, _ := json.Marshal(api.channelMembers)
	if string(existingAfter) != string(existingBefore) || string(membersAfter) != string(membersBefore) {
		t.Fatal("onboarding changed existing members or added unrelated memberships")
	}
}
