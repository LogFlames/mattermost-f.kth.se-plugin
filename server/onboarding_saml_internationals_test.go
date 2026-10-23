package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestPrioritizeFysiksektionen(t *testing.T) {
	teams := []*model.Team{
		{Id: "fsn", Name: "FSN"},
		{Id: "fysik", Name: "Fysiksektionen"},
		{Id: "other", Name: "Other"},
	}

	got := prioritizeFysiksektionen(teams)
	if got[0].Name != "Fysiksektionen" || got[1].Name != "FSN" || got[2].Name != "Other" {
		t.Fatalf("unexpected team order: %v", got)
	}
}

func TestPrioritizeFysiksektionenPreservesOtherTeamOrder(t *testing.T) {
	teams := []*model.Team{
		{Id: "one", Name: "One"},
		{Id: "two", Name: "Two"},
	}

	got := prioritizeFysiksektionen(teams)
	if got[0].Id != "one" || got[1].Id != "two" {
		t.Fatalf("unexpected team order: %v", got)
	}
}
