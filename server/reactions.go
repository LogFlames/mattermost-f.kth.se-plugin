package main

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

func (p *Plugin) Reactions_Filter(post *model.Post, configuration *configuration) (*model.Post, string) {
	p.debug("Reactions: Filter called for post: " + post.Id)

	// Only works in threads
	if post.RootId == "" {
		p.debug("Reactions: Skipping - not a thread reply (RootId is empty)")
		return post, ""
	}

	p.debug(fmt.Sprintf("Reactions: Thread reply detected, RootId=%s", post.RootId))

	// Check if message starts with @reactions followed by end-of-string or non-alphanumeric
	if !strings.HasPrefix(post.Message, "@reactions") {
		p.debug("Reactions: Skipping - message does not start with '@reactions'")
		return post, ""
	}
	if len(post.Message) > len("@reactions") {
		next := post.Message[len("@reactions")]
		if (next >= 'a' && next <= 'z') || (next >= 'A' && next <= 'Z') || (next >= '0' && next <= '9') || next == '_' {
			p.debug(fmt.Sprintf("Reactions: Skipping - '@reactions' followed by alphanumeric: %c", next))
			return post, ""
		}
	}

	p.debug("Reactions: @reactions prefix found, fetching reactions on root post")

	// Fetch reactions on the root post
	reactions, err := p.API.GetReactions(post.RootId)
	if err != nil {
		p.debug("Reactions: Failed to get reactions: " + err.Error())
		return post, ""
	}

	p.debug(fmt.Sprintf("Reactions: Found %d reactions on root post", len(reactions)))

	if len(reactions) == 0 {
		p.debug("Reactions: No reactions found, leaving message as-is")
		return post, ""
	}

	// Collect unique user IDs
	userIdSet := make(map[string]bool)
	for _, reaction := range reactions {
		userIdSet[reaction.UserId] = true
	}

	p.debug(fmt.Sprintf("Reactions: %d unique reactors", len(userIdSet)))

	// Resolve usernames
	var usernames []string
	for userId := range userIdSet {
		user, err := p.API.GetUser(userId)
		if err != nil {
			continue
		}
		usernames = append(usernames, "@"+user.Username)
	}

	if len(usernames) == 0 {
		p.debug("Reactions: No usernames resolved, leaving message as-is")
		return post, ""
	}

	// Replace the leading @reactions with the expanded mention list
	mentionList := strings.Join(usernames, " ")
	replacement := "[@reactions](" + mentionList + ")"
	post.Message = replacement + post.Message[len("@reactions"):]

	p.debug("Reactions: Replacement done")
	return post, ""
}
