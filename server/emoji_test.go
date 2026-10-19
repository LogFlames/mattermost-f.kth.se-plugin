package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestInsertZeroWidthSpacesAngryFace(t *testing.T) {
	p := &Plugin{}
	p.emoji_regexes = p.compileEmojiRegexes()
	for _, tc := range []struct {
		name    string
		message string
		want    string
	}{
		{"standalone", ">:(", "\u200b>:\u200b("},
		{"mixed", "å >:(\t>:(\n:( :) >:(", "å \u200b>:\u200b(\t\u200b>:\u200b(\n:\u200b( :\u200b) \u200b>:\u200b("},
		{"boundaries", "x>:( >:(x (>:( >:(! >:-(", "x>:( >:(x (>:( >:(! >:-("},
		{"already converted", "\u200b>:\u200b(", "\u200b>:\u200b("},
	} {
		t.Run(tc.name, func(t *testing.T) {
			post := &model.Post{Message: tc.message}
			for pass := 0; pass < 2; pass++ {
				result, reason := p.InsertZeroWidthSpaces(post, &configuration{ConvertToTextEmojies: true})
				if reason != "" || result.Message != tc.want {
					t.Fatalf("pass %d: message = %q, reason = %q; want %q", pass, result.Message, reason, tc.want)
				}
			}
		})
	}
	post, reason := p.InsertZeroWidthSpaces(&model.Post{Message: ">:("}, &configuration{})
	if reason != "" || post.Message != ">:(" {
		t.Fatalf("disabled conversion changed message: %q, reason = %q", post.Message, reason)
	}
}
