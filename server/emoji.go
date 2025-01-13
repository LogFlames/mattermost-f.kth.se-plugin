package main

import (
	"regexp"

	"github.com/mattermost/mattermost/server/public/model"
)

func (p *Plugin) compileEmojiRegexes() []*regexp.Regexp {
	var regexes []*regexp.Regexp = make([]*regexp.Regexp, 0)

	// Taken from: https://github.com/mattermost/mattermost-mobile/blob/ffbca2889a081904e44c698ade32bd55689effea/app/utils/emoji/helpers.ts#L19

	// slightly_smiling_face
	regexes = append(regexes, regexp.MustCompile(`(:-?\))`))
	// wink
	regexes = append(regexes, regexp.MustCompile(`(;-?\))`))
	// open_mouth
	regexes = append(regexes, regexp.MustCompile(`(?i)(:o)`))
	// scream
	regexes = append(regexes, regexp.MustCompile(`(?i)(:-o)`))
	// smirk
	regexes = append(regexes, regexp.MustCompile(`(:-?\])`))
	// smile
	regexes = append(regexes, regexp.MustCompile(`(?i)(:-?d)`))
	// stuck_out_tongue_closed_eyes
	regexes = append(regexes, regexp.MustCompile(`(?i)(x-d)`))
	// stuck_out_tongue
	regexes = append(regexes, regexp.MustCompile(`(?i)(:-?p)`))
	// rage
	regexes = append(regexes, regexp.MustCompile(`(:-?[[@])`))
	// slightly_frowning_face
	regexes = append(regexes, regexp.MustCompile(`(:-?\()`))
	// cry
	regexes = append(regexes, regexp.MustCompile("(:[`'’]-?\\(|:&#x27;\\(|:&#39;\\()"))
	// confused
	regexes = append(regexes, regexp.MustCompile(`(:-?\/)`))
	// confounded
	regexes = append(regexes, regexp.MustCompile(`(?i)(:-?s)`))
	// neutral_face
	regexes = append(regexes, regexp.MustCompile(`(:-?\|)`))
	// flushed
	regexes = append(regexes, regexp.MustCompile(`(:-?\?:$)`))
	// mask
	regexes = append(regexes, regexp.MustCompile(`(?i)(:-x)`))
	// heart
	regexes = append(regexes, regexp.MustCompile(`(<3|&lt;3)`))
	// broken_heart
	regexes = append(regexes, regexp.MustCompile(`(</3|&lt;&#x2F;3)`))

	return regexes
}

func (p *Plugin) InsertZeroWidthSpaces(post *model.Post, configuration *configuration) (*model.Post, string) {
	if configuration.ConvertToTextEmojies {
		p.debug("Inserting zero-width spaces into text-emojis")

		newMessage := post.Message
		for _, regex := range p.emoji_regexes {
			matches := regex.FindAllStringIndex(newMessage, -1)
			offset := 0
			l := len(newMessage)
			for _, match := range matches {
				left := match[0] + offset
				right := match[1] + offset
				if !(left == 0 || newMessage[left-1] == ' ' || newMessage[left-1] == '\n' || newMessage[left-1] == '\t' || newMessage[left-1] == '\r' || newMessage[left-1] == '\f' || newMessage[left-1] == '\v') {
					continue
				}

				if !(right == l || newMessage[right] == ' ' || newMessage[right] == '\n' || newMessage[right] == '\t' || newMessage[right] == '\r' || newMessage[right] == '\f' || newMessage[right] == '\v') {
					continue
				}

				newMessage = newMessage[:left+1] + "\u200b" + newMessage[left+1:]
				offset += 3
				l += 3
			}
		}

		post.Message = newMessage
	}

	return post, ""
}
