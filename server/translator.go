package main

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

type translatorChannel struct {
	Name                 string   `json:"name"`
	ChannelIDs           []string `json:"channel_ids"`
	Delivery             string   `json:"delivery"`
	AddToChannelIDs      []string `json:"add_to_channel_ids"`
	DirectMessagePrefix  string   `json:"direct_message_prefix"`
	MistralModelOverride string   `json:"model-override"`
}

/* Checks that required fields are set and non-conflicting. Note: this validation is also performed in the webapp. */
func validateTranslatorConfiguration(configuration *configuration) error {
	seen := make(map[string]struct{})
	for index, channel := range configuration.TranslatorBot_Custom_ChannelList {
		if len(channel.ChannelIDs) == 0 {
			return fmt.Errorf("translator channel %d has no channel IDs", index+1)
		}

		for _, id := range channel.ChannelIDs {
			if strings.TrimSpace(id) == "" {
				return fmt.Errorf("translator channel %d has an empty channel ID", index+1)
			}
			if _, ok := seen[id]; ok {
				return fmt.Errorf("translator channel list contains duplicate channel ID %q", id)
			}
			seen[id] = struct{}{}
		}

		if channel.Delivery != "reply" && channel.Delivery != "dm" {
			return fmt.Errorf("translator channel %q has invalid delivery %q; expected reply or dm", channel.Name, channel.Delivery)
		}
		if channel.Delivery == "dm" && strings.TrimSpace(channel.DirectMessagePrefix) == "" {
			return fmt.Errorf("translator channel %q requires a direct-message prefix", channel.Name)
		}
	}
	return nil
}

/* If a message is sent in a channel with autotranslation configured, translate it. */
func (p *Plugin) translatorMessageHasBeenPosted(post *model.Post) {
	configuration := p.getConfiguration()
	if post == nil {
		return
	}

	var channelConfiguration *translatorChannel
OuterLoop:
	for index := range configuration.TranslatorBot_Custom_ChannelList {
		for _, id := range configuration.TranslatorBot_Custom_ChannelList[index].ChannelIDs {
			if id == post.ChannelId {
				channelConfiguration = &configuration.TranslatorBot_Custom_ChannelList[index]
				break OuterLoop
			}
		}
	}
	if channelConfiguration == nil {
		return
	}
	// Letting the translator translate posts in channels it is not a member in is quite invisible behavior: this suggests that the translator should be a member of the channel, so we warn if it is not.
	if !p.translatorBotIsMember(post.ChannelId) {
		p.translatorError("translate post", fmt.Errorf("Warning: @translator-bot is not a member of channel %s, but translation is configured for it. It is suggested that you invite the bot to the channel.", post.ChannelId))
	}
	if post.UserId == p.translatorBot.UserId || strings.HasPrefix(post.Type, "system") ||
		post.RootId != "" || strings.TrimSpace(post.Message) == "" {
		return
	}

	for _, addToChannelID := range channelConfiguration.AddToChannelIDs {
		if strings.TrimSpace(addToChannelID) != "" {
			if _, err := p.API.AddUserToChannel(addToChannelID, post.UserId, p.translatorBot.UserId); err != nil {
				p.translatorError("add message author to configured channel", err)
			}
		}
	}

	translation, err := p.translate(post.Message, configuration, channelConfiguration.MistralModelOverride)
	if err != nil {
		p.translatorErrorWithChannel("translate post", err, configuration.TranslatorBot_ErrorChannelID)
		p.notifyTranslationFailure(post.UserId, post, channelConfiguration.Delivery, err.Error())
		return
	}
	p.deliverTranslation(post, translation, *channelConfiguration, configuration)
}

func (p *Plugin) translatorReactionHasBeenAdded(reaction *model.Reaction) {
	configuration := p.getConfiguration()
	if reaction == nil ||
		reaction.EmojiName != "english" || reaction.UserId == p.translatorBot.UserId ||
		!p.translatorBotIsMember(reaction.ChannelId) ||
		contains(configuration.TranslatorBot_Custom_ExcludedChannels, reaction.ChannelId) {
		return
	}
	reactions, appErr := p.API.GetReactions(reaction.PostId)
	if appErr != nil {
		p.translatorError("get post reactions", appErr)
		return
	}
	for _, existing := range reactions {
		if existing.UserId == p.translatorBot.UserId {
			return
		}
	}
	if _, err := p.API.AddReaction(&model.Reaction{
		UserId: p.translatorBot.UserId, PostId: reaction.PostId, EmojiName: "english",
	}); err != nil {
		p.translatorError("mark post as translated", err)
		return
	}

	post, appErr := p.API.GetPost(reaction.PostId)
	if appErr != nil || post == nil || strings.TrimSpace(post.Message) == "" {
		if appErr != nil {
			p.translatorError("get reaction target post", appErr)
		}
		return
	}
	translation, err := p.translate(post.Message, configuration, "")
	if err != nil {
		p.translatorErrorWithChannel("translate reaction target", err, configuration.TranslatorBot_ErrorChannelID)
		p.notifyTranslationFailure(reaction.UserId, post, "dm", err.Error())
		return
	}
	quoted := "> ###### :english: Translation by @translator-bot\n>\n"
	for _, line := range strings.Split(translation, "\n") {
		quoted += "> " + line + "\n"
	}
	post.Message += "\n\n" + quoted
	if _, appErr = p.API.UpdatePost(post); appErr != nil {
		p.translatorError("update reaction target post", appErr)
		return
	}
	p.translatorAudit(post, translation, configuration)
}

func (p *Plugin) translatorBotIsMember(channelID string) bool {
	if p.translatorBot.UserId == "" {
		return false
	}
	_, err := p.API.GetChannelMember(channelID, p.translatorBot.UserId)
	return err == nil
}

func (p *Plugin) translate(message string, configuration *configuration, modelOverride string) (string, error) {
	if strings.TrimSpace(configuration.TranslatorBot_SystemPrompt) == "" {
		return "", fmt.Errorf("Mistral system prompt is not configured")
	}
	if strings.TrimSpace(configuration.TranslatorBot_UserPrompt) == "" {
		return "", fmt.Errorf("Mistral user prompt is not configured")
	}
	if strings.TrimSpace(configuration.TranslatorBot_DictionaryPostID) == "" {
		return "", fmt.Errorf("Mattermost dictionary post ID is not configured")
	}
	dictionaryPost, appErr := p.API.GetPost(configuration.TranslatorBot_DictionaryPostID)
	if appErr != nil || dictionaryPost == nil || strings.TrimSpace(dictionaryPost.Message) == "" {
		if appErr != nil {
			return "", fmt.Errorf("dictionary post %s: %w", configuration.TranslatorBot_DictionaryPostID, appErr)
		}
		return "", fmt.Errorf("dictionary post %s was not found or is empty", configuration.TranslatorBot_DictionaryPostID)
	}

	userPrompt := strings.ReplaceAll(configuration.TranslatorBot_UserPrompt, "{message}", message)
	if userPrompt == configuration.TranslatorBot_UserPrompt {
		userPrompt += message
	}
	model := modelOverride
	if model == "" {
		model = configuration.TranslatorBot_MistralModel.ID
	}
	client := NewMistralClient(configuration.TranslatorBot_APIKey, model)
	if model == configuration.TranslatorBot_MistralModel.ID && configuration.TranslatorBot_MistralModel.ReasoningEffort != "" {
		client.reasoningEffort = configuration.TranslatorBot_MistralModel.ReasoningEffort
	}
	return client.Complete(
		configuration.TranslatorBot_SystemPrompt+"\n"+dictionaryPost.Message,
		userPrompt,
	)
}

func (p *Plugin) notifyTranslationFailure(userID string, post *model.Post, delivery string, error string) {
	message := "The translation of post [" + post.Id + "](https://mattermost.fysiksektionen.se/fysiksektionen/pl/" + post.Id + ") could not be produced. The error was: \n\n```text\n" + error + "\n```\n\n Please contact the Mattermost administrators for assistance. You can reach us in Mattermost or at mattermaster@f.kth.se."

	switch delivery {
	case "reply":
		if p.API.SendEphemeralPost(userID, &model.Post{
			UserId: p.translatorBot.UserId, ChannelId: post.ChannelId, RootId: post.Id, Message: message,
		}) == nil {
			p.translatorError("notify user of translation failure", fmt.Errorf("send ephemeral reply failed"))
		}
	case "dm":
		dm, err := p.API.GetDirectChannel(userID, p.translatorBot.UserId)
		if err != nil {
			p.translatorError("get translation failure direct-message channel", err)
			return
		}
		if p.API.SendEphemeralPost(userID, &model.Post{
			UserId: p.translatorBot.UserId, ChannelId: dm.Id, Message: message,
		}) == nil {
			p.translatorError("notify user of translation failure", fmt.Errorf("send ephemeral direct message failed"))
		}
	default:
		p.translatorError("notify user of translation failure", fmt.Errorf("unsupported delivery mode %q", delivery))
	}
}

func (p *Plugin) deliverTranslation(post *model.Post, translation string, channel translatorChannel, configuration *configuration) {
	switch channel.Delivery {
	case "reply":
		if _, err := p.API.CreatePost(&model.Post{
			UserId: p.translatorBot.UserId, ChannelId: post.ChannelId, RootId: post.Id, Message: translation,
		}); err != nil {
			p.translatorError("create translation reply", err)
			return
		}
	case "dm":
		dm, err := p.API.GetDirectChannel(post.UserId, p.translatorBot.UserId)
		if err != nil {
			p.translatorError("get translation direct-message channel", err)
			return
		}
		for _, message := range []string{channel.DirectMessagePrefix, translation} {
			if _, appErr := p.API.CreatePost(&model.Post{
				UserId: p.translatorBot.UserId, ChannelId: dm.Id, Message: message,
			}); appErr != nil {
				p.translatorError("create translation direct message", appErr)
				return
			}
		}
	default:
		p.translatorError("deliver translation", fmt.Errorf("unsupported delivery mode %q", channel.Delivery))
		return
	}
	p.translatorAudit(post, translation, configuration)
}

func (p *Plugin) translatorAudit(post *model.Post, translation string, configuration *configuration) {
	if configuration.TranslatorBot_AuditChannelID == "" {
		return
	}
	if _, err := p.API.CreatePost(&model.Post{
		UserId: p.translatorBot.UserId, ChannelId: configuration.TranslatorBot_AuditChannelID,
		Message: fmt.Sprintf("Translated post `%s`:\n\n%s", post.Id, translation),
	}); err != nil {
		p.translatorError("write translation audit", err)
	}
}

func (p *Plugin) translatorError(operation string, err error) {
	p.translatorErrorWithChannel(operation, err, p.getConfiguration().TranslatorBot_ErrorChannelID)
}

func (p *Plugin) translatorErrorWithChannel(operation string, err error, channelID string) {
	message := fmt.Sprintf("translator: %s: %v", operation, err)
	p.API.LogError(message)
	if channelID != "" {
		if _, appErr := p.API.CreatePost(&model.Post{
			UserId: p.translatorBot.UserId, ChannelId: channelID, Message: message,
		}); appErr != nil {
			p.API.LogError(fmt.Sprintf("translator: post error report: %v", appErr))
		}
	}
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
