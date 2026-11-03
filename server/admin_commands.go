package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// TODO write better module string
// TODO prefix method names to avoid collisions

/*
	/mm is a set of administrative commands for managing the Mattermost instance.
*/

var adminCommandUsage = map[string]string{
	"move-town-square":    "Usage: /mm move-town-square [team] ~[old-town-square] [new-name-for-old-town-square] ~[channel-to-make-town-square]",
	"rename-team":         "Usage: /mm rename-team [team] [new-team-name]",
	"gdpr-clean-user":     "Usage: /mm gdpr-clean-user @[user] [soft|hard]",
	"soft-delete-posts":   "Usage: /mm soft-delete-posts [message-id]",
	"soft-reauthor-posts": "Usage: /mm soft-reauthor-posts [message-id]",
	"search-posts":        "Usage: /mm search-posts \"[regex]\"",
	// NEW COMMAND? add usage string here
}
var adminCommandNames = []string{
	"move-town-square",
	"rename-team",
	"gdpr-clean-user",
	"soft-delete-posts",
	"soft-reauthor-posts",
	"search-posts",
	// NEW COMMAND? add name here
}

type adminCommandContext struct {
	UserID    string
	TeamID    string
	ChannelID string
}

func (p *Plugin) adminCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}

	if !p.adminCommandAllowed(args.UserId) {
		if !p.API.HasPermissionTo(args.UserId, model.PermissionManageSystem) {
			response.Text = "Only system admins can run /mm."
			return response, nil
		} else {
			response.Text = "Only system admins which are explicitly configured to be able to run /mm can do so. This is a potentially dangerous tool. Please contact the system administrators before using this tool."
			return response, nil
		}

	}

	fields, err := parseAdminCommand(args.Command)
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	context := adminCommandContext{UserID: args.UserId, TeamID: args.TeamId, ChannelID: args.ChannelId}
	if len(fields) >= 3 && isAdminCommandHash(fields[len(fields)-1]) {
		return p.validateAndRunCommand(fields, context, response)
	} else {
		return p.validateCommand(fields, context, response)
	}
}

/* DRY RUN: Validates the command. */
func (p *Plugin) validateCommand(fields []string, context adminCommandContext, response *model.CommandResponse) (*model.CommandResponse, *model.AppError) {
	summary, err := p.validateAdminCommand(fields, context)
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	hash := adminCommandHash(fields)
	response.Text = fmt.Sprintf("Proposed changes: %s\n\nThis command is not executed yet. Append `%s` to the command to confirm it.", summary, hash)
	return response, nil
}

/* LIVE: Checks the hash and validates the command before running it command. */
func (p *Plugin) validateAndRunCommand(fields []string, context adminCommandContext, response *model.CommandResponse) (*model.CommandResponse, *model.AppError) {
	commandFields := fields[:len(fields)-1]
	providedHash := strings.ToUpper(fields[len(fields)-1])

	if len(commandFields) != len(strings.Fields(adminCommandUsage[fields[1]])) {
		response.Text = fmt.Sprintf("%s\n\nWrong number of fields for /mm "+fields[1]+". The last field looks like a hash, please re-run the command without it for a dry run evaluation.", adminCommandUsage[fields[1]])
	} // We have the right number of fields

	if expectedHash := adminCommandHash(commandFields); expectedHash != providedHash {
		response.Text = fmt.Sprintf("%s\n\nCommand hash mismatch. Please re-run the command without the hash (%s) for a dry run evaluation.", adminCommandUsage[fields[1]], providedHash)
		return response, nil
	} // The hash matches the command

	if _, err := p.validateAdminCommand(commandFields, context); err != nil {
		response.Text = fmt.Sprintf("ERROR: THE LAST FIELD IS THE CORRECT HASH, BUT THE COMMAND IS NOW INVALID. Something must have changed since the hash was generated.\n\n%s", err.Error())
		return response, nil
	} // The command is re-validated
	if err := p.runCommand(commandFields, context); err != nil {
		// TODO
		response.Text = err.Error()
		return response, nil
	}

	return response, nil
}

func adminCommandHash(fields []string) string { // OK
	sum := sha256.Sum256([]byte(strings.Join(fields, "\x00")))
	return fmt.Sprintf("#%s", strings.ToUpper(hex.EncodeToString(sum[:])[:6]))
}

/* Format: #6CHARS */
func isAdminCommandHash(value string) bool {
	return regexp.MustCompile(`^#[A-Fa-f0-9]{6}$`).MatchString(value)
}

/* Only users who are system admin AND are explicitly configured to may run /mm. */
func (p *Plugin) adminCommandAllowed(userID string) bool { // OK
	if !p.API.HasPermissionTo(userID, model.PermissionManageSystem) {
		return false
	}

	for _, allowedUserID := range p.getConfiguration().AdminCommands_AllowedUsers {
		if allowedUserID == userID {
			return true
		}
	}
	return false
}

func parseAdminCommand(command string) ([]string, error) { // OK
	fields, err := shellFields(strings.TrimSpace(command))
	if err != nil {
		return nil, fmt.Errorf("%s\n\n%s", err, adminUsage())
	}
	if len(fields) == 0 || fields[0] != "/mm" {
		return nil, fmt.Errorf("%s\n\n%s", "Invalid /mm command.", adminUsage())
	}
	return fields, nil
}

/*
	Call this function without the hash string.

Validates the admin command, returning a (dry-run) summary of the command if it is valid or a descriptive error otherwise.
*/
func (p *Plugin) validateAdminCommand(fields []string, context adminCommandContext) (string, error) { // OK
	// Return general reference if a subcommand is not given
	if len(fields) < 2 {
		return "", fmt.Errorf("%s", adminUsage())
	}
	if _, ok := adminCommandUsage[fields[1]]; !ok {
		return "", fmt.Errorf("%s", adminUsage())
	}

	// Return reference for subcommand
	if len(fields) == 2 {
		return "", fmt.Errorf("%s", adminCommandUsage[fields[1]])
	}

	// Ensure the command has the same number of fields as its usage string.
	if len(fields) != len(strings.Fields(adminCommandUsage[fields[1]])) {
		return "", fmt.Errorf("%s\n\nWrong number of fields for /mm %s.", adminCommandUsage[fields[1]], fields[1])
	}

	var s string
	var err error

	switch fields[1] {
	case "move-town-square":
		s, err = p.validateMoveTownSquare(fields, context)
	case "rename-team":
		s, err = p.validateRenameTeam(fields, context)
	case "gdpr-clean-user":
		s, err = p.validateGDPRCleanUser(fields)
	case "soft-delete-posts":
		s, err = p.validateSoftDeletePosts(fields)
	case "soft-reauthor-posts":
		s, err = p.validateSoftReauthorPosts(fields)
	case "search-posts":
		s, err = p.validateSearchPosts(fields)
	default:
		return "", fmt.Errorf("%s", adminUsage())
	}

	if err != nil {
		return "", fmt.Errorf("%s\n\n%s", adminCommandUsage[fields[1]], err)
	} else {
		return s, nil
	}
}

/*
	/mm move-town-square [team] ~[old-town-square] [new-name-for-old-town-square] ~[channel-to-make-town-square]

Checks:
1. That the command is exactly 6 fields long (including /mm)
2. That the team exists and is the current team
3. That the old-town-square channel exists and is the current ~town-square channel
4. That the new name for the old town square is valid and is not currently a channel
5. That the channel to make town square exists
*/
func (p *Plugin) validateMoveTownSquare(fields []string, context adminCommandContext) (string, error) {
	// Initial validation
	if fields[0] != "/mm" || fields[1] != "move-town-square" {
		return "", fmt.Errorf("Major internal error: validateMoveTownSquare called with wrong initial fields.")
	}
	if len(fields) != 6 {
		return "", fmt.Errorf("Wrong number of fields for /mm move-town-square.")
	}

	// Validate team: exists and is the current team
	team, appErr := p.API.GetTeamByName(fields[2])
	if appErr != nil || team == nil {
		return "", fmt.Errorf("Invalid team %q.", fields[2])
	}
	if context.TeamID != team.Id {
		return "", fmt.Errorf("Team %q is not the current team. Run this command in the team you wish to re-configure.", fields[2])
	}

	// Build strings
	oldTownSquareChannelName := strings.TrimPrefix(fields[3], "~")
	newTownSquareChannelName := strings.TrimPrefix(fields[5], "~")

	// Check formatting of all fields.
	if !validAdminName(fields[2]) || !validAdminName(oldTownSquareChannelName) || !validAdminName(fields[4]) || !validAdminName(newTownSquareChannelName) {
		return "", fmt.Errorf("Invalid formatting of one or more fields. Use only valid characters (alphanumeric or _.-) and don't make them too long (32 characters max).")
	}

	/* Redundant, we will allow unreferenced channel names
	if !strings.HasPrefix(fields[3], "~") || !strings.HasPrefix(fields[5], "~") {
		return "", fmt.Errorf("The old and new Town Square channels must be prefixed with ~.")
	} */

	// Check that the old town square channel exists and is the current town square channel
	oldTownSquare, appErr := p.API.GetChannelByName(team.Id, oldTownSquareChannelName, false)
	if appErr != nil || oldTownSquare == nil {
		return "", fmt.Errorf("Channel ~%s does not exist in team %q.", oldTownSquareChannelName, fields[2])
	}
	townSquare, appErr := p.API.GetChannelByName(team.Id, model.DefaultChannelName, false)
	if appErr != nil || townSquare == nil {
		return "", fmt.Errorf("Team %q does not have a Town Square channel.", fields[2])
	}
	if oldTownSquare.Id != townSquare.Id {
		return "", fmt.Errorf("~%s is not the current Town Square channel.", oldTownSquareChannelName)
	}

	// Check that the new name for the old town square is not already a channel in the team
	if replacement, lookupErr := p.API.GetChannelByName(team.Id, fields[4], false); lookupErr == nil && replacement != nil {
		return "", fmt.Errorf("Channel %q already exists in team %q.", fields[4], fields[2])
	} else if lookupErr != nil && lookupErr.StatusCode != http.StatusNotFound {
		return "", fmt.Errorf("Could not check whether channel %q exists.", fields[4])
	}

	// Check that the new town square channel exists in the team
	destination, appErr := p.API.GetChannelByName(team.Id, newTownSquareChannelName, false)
	if appErr != nil || destination == nil {
		return "", fmt.Errorf("Channel ~%s does not exist in team %q.", newTownSquareChannelName, fields[2])
	}

	return fmt.Sprintf(
		`Rename Town Square ~%s to %s and make ~%s the new Town Square channel in team %s (%s).
		
		> Validation checks passed.
		> * :white_check_mark: Team %s (display name: %s) exists and is the current team.
		> * :white_check_mark: Channel ~%s exists and is the current Town Square channel.
		> * :white_check_mark: Channel ~%s does not exist in team %s and is a valid channel name.
		> * :white_check_mark: Channel ~%s exists in team %s.`,
		oldTownSquareChannelName, fields[4], newTownSquareChannelName, fields[2], team.DisplayName,
		fields[2], team.DisplayName,
		oldTownSquareChannelName,
		fields[4], fields[2],
		newTownSquareChannelName, fields[2]), nil
}

func (p *Plugin) validateRenameTeam(fields []string, context adminCommandContext) (string, error) {
	if len(fields) < 2 || fields[0] != "/mm" || fields[1] != "rename-team" {
		return "", fmt.Errorf("Major internal error: validateRenameTeam called with wrong initial fields.")
	}
	if len(fields) != 4 {
		return "", fmt.Errorf("Wrong number of fields for /mm rename-team.")
	}

	team, appErr := p.API.GetTeamByName(fields[2])
	if appErr != nil || team == nil {
		return "", fmt.Errorf("Invalid team %q.", fields[2])
	}
	if context.TeamID != team.Id {
		return "", fmt.Errorf("Team %q is not the current team. Run this command in the team you wish to rename.", fields[2])
	}
	if !validAdminName(fields[2]) || !validAdminName(fields[3]) {
		return "", fmt.Errorf("Invalid formatting of one or more fields. Use only valid characters (alphanumeric or _.-) and don't make them too long (32 characters max).")
	}
	if fields[2] == fields[3] {
		return "", fmt.Errorf("The new team name must be different from the current team name.")
	}

	if existingTeam, lookupErr := p.API.GetTeamByName(fields[3]); lookupErr == nil && existingTeam != nil {
		return "", fmt.Errorf("Team %q already exists.", fields[3])
	} else if lookupErr != nil && lookupErr.StatusCode != http.StatusNotFound {
		return "", fmt.Errorf("Could not check whether team %q exists.", fields[3])
	}

	return fmt.Sprintf(
		`Rename team %s to %s.

		> Validation checks passed.
		> * :white_check_mark: Team %s (display name: %s) exists and is the current team.
		> * :white_check_mark: Team %s does not exist and is a valid team name.`,
		fields[2], fields[3],
		fields[2], team.DisplayName,
		fields[3]), nil
}

func (p *Plugin) validateGDPRCleanUser(fields []string) (string, error) {
	if len(fields) < 2 || fields[0] != "/mm" || fields[1] != "gdpr-clean-user" {
		return "", fmt.Errorf("Major internal error: validateGDPRCleanUser called with wrong initial fields.")
	}
	if len(fields) != 4 {
		return "", fmt.Errorf("Wrong number of fields for /mm gdpr-clean-user.")
	}

	username := strings.TrimPrefix(fields[2], "@")
	user, appErr := p.API.GetUserByUsername(username)
	if appErr != nil || user == nil {
		return "", fmt.Errorf("Invalid user @%s.", username)
	}

	switch fields[3] {
	case "soft":
		// Soft deletion: only anonymize the user, but keep posts and files
		return "", nil // TODO
	case "hard":
		// Hard deletion: anonymize the user and mark all posts as deleted in the database
		return "", nil // TODO
	default:
		return "", fmt.Errorf("Invalid cleanup mode %q. Use soft or hard.", fields[3])
	}
}

func (p *Plugin) validateSoftDeletePosts(fields []string) (string, error) {
	if len(fields) != 3 || !model.IsValidId(fields[2]) {
		return "", fmt.Errorf("%s", adminCommandUsage["soft-delete-posts"])
	}
	return fmt.Sprintf("Soft-delete post %s.", fields[2]), nil
}

func (p *Plugin) validateSoftReauthorPosts(fields []string) (string, error) {
	if len(fields) != 3 || !model.IsValidId(fields[2]) {
		return "", fmt.Errorf("%s", adminCommandUsage["soft-reauthor-posts"])
	}
	return fmt.Sprintf("Soft-reauthor post %s.", fields[2]), nil
}

func (p *Plugin) validateSearchPosts(fields []string) (string, error) {
	if len(fields) != 3 {
		return "", fmt.Errorf("%s", adminCommandUsage["search-posts"])
	}
	if _, err := regexp.Compile(fields[2]); err != nil {
		return "", fmt.Errorf("Invalid search regex: %w", err)
	}
	return fmt.Sprintf("Search posts using regular expression %q.", fields[2]), nil
}

func (p *Plugin) runCommand(fields []string, context adminCommandContext) error {
	if len(fields) < 2 {
		return fmt.Errorf("%s", adminUsage())
	}
	switch fields[1] {
	case "move-town-square":
		return p.renameTownSquare(fields, context)
	case "rename-team":
		return p.renameTeam(fields, context)
	case "gdpr-clean-user":
		return p.GDPRCleanUser(fields, context)
	case "soft-delete-posts":
		return p.softDeletePosts(fields, context)
	case "soft-reauthor-posts":
		return p.softReauthorPosts(fields, context)
	case "search-posts":
		return p.searchPosts(fields, context)
	default:
		return fmt.Errorf("unknown administrative command %q", fields[1])
	}
}

func (p *Plugin) renameTownSquare(fields []string, context adminCommandContext) error {
	if _, err := p.validateMoveTownSquare(fields, context); err != nil {
		return err
	}
	return nil
}

func (p *Plugin) renameTeam(fields []string, context adminCommandContext) error {
	if _, err := p.validateRenameTeam(fields, context); err != nil {
		return err
	}
	return nil
}

func (p *Plugin) GDPRCleanUser(fields []string, context adminCommandContext) error {
	if _, err := p.validateGDPRCleanUser(fields); err != nil {
		return err
	}
	return nil
}

func (p *Plugin) softDeletePosts(fields []string, context adminCommandContext) error {
	if _, err := p.validateSoftDeletePosts(fields); err != nil {
		return err
	}
	return nil
}

func (p *Plugin) softReauthorPosts(fields []string, context adminCommandContext) error {
	if _, err := p.validateSoftReauthorPosts(fields); err != nil {
		return err
	}
	return nil
}

func (p *Plugin) searchPosts(fields []string, context adminCommandContext) error {
	if _, err := p.validateSearchPosts(fields); err != nil {
		return err
	}
	return nil
}

func adminUsage() string {
	usages := make([]string, 0, len(adminCommandNames))
	for _, name := range adminCommandNames {
		usages = append(usages, adminCommandUsage[name])
	}
	return strings.Join(usages, " | ")
}

const MAX_NAME_LENGTH = 32 // Mattermost supports longer channel names, usernames, team names etc. in many occasions but we limit it here to avoid problems

/* Validates that a name contains only allowed characters (alphanumeric or ._-), starts with an alphanumeric character, and is not longer than 32. */
func validAdminName(value string) bool {
	var allowedNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	return allowedNamePattern.MatchString(value) && len(value) <= MAX_NAME_LENGTH
}

/* Builds fields from string, with support for regex inside quotation marks */
func shellFields(command string) ([]string, error) { // OK
	var fields []string
	var current strings.Builder
	var quote rune
	escaped := false

	for _, character := range command {
		switch {
		case escaped:
			current.WriteRune(character)
			escaped = false
		case character == '\\' && quote != '\'':
			escaped = true
		case quote != 0:
			if character == quote {
				quote = 0
			} else {
				current.WriteRune(character)
			}
		case character == '\'' || character == '"':
			quote = character
		case character == ' ' || character == '\t' || character == '\n' || character == '\r':
			if current.Len() > 0 {
				fields = append(fields, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(character)
		}
	}
	if escaped || quote != 0 {
		return nil, fmt.Errorf("Unterminated quote or escape in command.")
	}
	if current.Len() > 0 {
		fields = append(fields, current.String())
	}
	return fields, nil
}
