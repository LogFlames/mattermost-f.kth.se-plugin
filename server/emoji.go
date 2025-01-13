package main

import (
	"regexp"

	"github.com/mattermost/mattermost/server/public/model"
)

func (p *Plugin) compileEmojiRegexes() []*regexp.Regexp {
	var regexes []*regexp.Regexp = make([]*regexp.Regexp, 0)

	// Taken from: https://github.com/mattermost/mattermost-mobile/blob/ffbca2889a081904e44c698ade32bd55689effea/app/utils/emoji/helpers.ts#L19

	// slightly_smiling_face
	regexes = append(regexes, regexp.MustCompile("(?:^|\\s)(:-?\\))(?:$|\\s)"))
	// wink
	regexes = append(regexes, regexp.MustCompile("(?:^|\\s)(;-?\\))(?:$|\\s)"))
	// open_mouth
	regexes = append(regexes, regexp.MustCompile("(?:^|\\s)(:o)(?:$|\\s)"))
	// scream
	regexes = append(regexes, regexp.MustCompile("(?:^|\\s)(:-o)(?:$|\\s)"))
	// smirk
	regexes = append(regexes, regexp.MustCompile("(?:^|\\s)(:-?\\])(?:$|\\s)"))
	// smile
	regexes = append(regexes, regexp.MustCompile("(?:^|\\s)(:-?d)(?:$|\\s)"))
	// stuck_out_tongue_closed_eyes
	regexes = append(regexes, regexp.MustCompile("(?:^|\\s)(x-d)(?:$|\\s)"))
	// stuck_out_tongue
	regexes = append(regexes, regexp.MustCompile("(?:^|\\s)(:-?p)(?:$|\\s)"))
	// rage
	regexes = append(regexes, regexp.MustCompile("(?:^|\\s)(:-?[@])(?:$|\\s)"))
	// slightly_frowning_face
	regexes = append(regexes, regexp.MustCompile("(?:^|\\s)(:-?\\()(?:$|\\s)"))
	// cry
	regexes = append(regexes, regexp.MustCompile("(?:^|\\s)(:[`'’]-?\\(|:&#x27;\\(|:&#39;\\()(?:$|\\s)"))
	// confused
	regexes = append(regexes, regexp.MustCompile("(?:^|\\s)(:-?\\/)(?:$|\\s)"))
	// confounded
	regexes = append(regexes, regexp.MustCompile("(?:^|\\s)(:-?s)(?:$|\\s)"))
	// neutral_face
	regexes = append(regexes, regexp.MustCompile("(?:^|\\s)(:-?\\|)(?:$|\\s)"))
	// flushed
	regexes = append(regexes, regexp.MustCompile("(?:^|\\s)(:-?\\?:$)($|\\s)"))
	// mask
	regexes = append(regexes, regexp.MustCompile("(?:^|\\s)(:-x)(?:$|\\s)"))
	// heart
	regexes = append(regexes, regexp.MustCompile("(?:^|\\s)(<3|&lt;3)(?:$|\\s)"))
	// broken_heart
	regexes = append(regexes, regexp.MustCompile("(?:^|\\s)(</3|&lt;&#x2F;3)(?:$|\\s)"))

	return regexes
}

func (p *Plugin) InsertZeroWidthSpaces(post *model.Post, configuration *configuration) (*model.Post, string) {
	if configuration.ConvertToTextEmojies {
		p.debug("Inserting zero-width spaces into text-emojis")

		newMessage := post.Message
		for _, regex := range p.emoji_regexes {
			newMessage = regex.ReplaceAllStringFunc(newMessage, func(match string) string {
				return match[:1] + "\u200b" + match[1:]
			})
		}

		post.Message = newMessage
	}

	return post, ""
}
