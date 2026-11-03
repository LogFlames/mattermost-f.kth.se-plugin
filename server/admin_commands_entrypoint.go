package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

type adminCommandContext struct {
	UserID    string
	TeamID    string
	ChannelID string
	Command   string
}

/* Entrypoint: checks that the user is allowed to run the command, parses it, and passes it on for validation (live if it ends in a hash, dry otherwise). */
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

	context := adminCommandContext{UserID: args.UserId, TeamID: args.TeamId, ChannelID: args.ChannelId, Command: args.Command}
	if len(fields) >= 3 && isAdminCommandHash(fields[len(fields)-1]) {
		return p.validateAndRunCommand(fields, context, response)
	} else {
		return p.validateCommand(fields, context, response)
	}
}

/* DRY RUN: Validates the command. */
func (p *Plugin) validateCommand(fields []string, context adminCommandContext, response *model.CommandResponse) (*model.CommandResponse, *model.AppError) { // OK
	summary, err := p.validateAdminCommand(fields, context, true)
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

	if validation, err := p.validateAdminCommand(commandFields, context, false); err != nil {
		response.Text = fmt.Sprintf("### ERROR: THE LAST FIELD IS THE CORRECT HASH, BUT THE COMMAND IS NOW INVALID. Something must have changed since the hash was generated.\n\n%s", err.Error())
		return response, nil
	} else {
		p.API.SendEphemeralPost(context.UserID, &model.Post{
			ChannelId: context.ChannelID,
			Message:   fmt.Sprintf("%s\n\nValided the command and the hash is valid. The server is now running this command:\n\n```text\n%s\n```", validation, context.Command),
		})
	} // The command is re-validated

	// THE COMMAND IS NOW LIVE, WE RUN IT.
	if err := p.runCommand(commandFields, context); err != nil {
		response.Text = fmt.Sprintf("Result for %s: \n\n ### COMMAND FAILED WITH ERROR: \n\n```text\n%s\n```.", context.Command, err.Error())
		return response, nil
	}

	return response, nil
}

/* Create hash on format #6CHARS of the command. */
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

/* Construct the fields and verify that the command begins with /mm */
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

func adminUsage() string {
	return "/mm " + strings.Join(adminCommandNames, "|") + "\n\nUse `/mm [command]` for usage information on a specific command."
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

const MAX_NAME_LENGTH = 32 // Mattermost supports longer channel names, usernames, team names etc. in many occasions but we limit it here to avoid problems

/* Validates that a name contains only allowed characters (alphanumeric or ._-), starts with an alphanumeric character, and is not longer than 32. */
func validAdminName(value string) bool {
	var allowedNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	return allowedNamePattern.MatchString(value) && len(value) <= MAX_NAME_LENGTH
}

/*
	Call this function without the hash string.

Validates the admin command, returning a (dry-run) summary of the command if it is valid or a descriptive error otherwise.
*/
func (p *Plugin) validateAdminCommand(fields []string, context adminCommandContext, do_expensive_operations bool) (string, error) { // OK
	// Return general reference if a subcommand is not given
	if len(fields) < 2 {
		return "", fmt.Errorf("%s", adminUsage())
	}
	if _, ok := adminCommandUsage[fields[1]]; !ok {
		return "", fmt.Errorf("%s", adminUsage())
	}

	// Return reference for subcommand with description
	if len(fields) == 2 {
		return "", fmt.Errorf("%s\n\n%s", adminCommandUsage[fields[1]], adminCommandDescription[fields[1]])
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
		s, err = p.validateGDPRCleanUser(fields, do_expensive_operations)
	case "soft-delete-posts":
		s, err = p.validateSoftDeletePosts(fields)
	case "undelete-posts":
		s, err = p.validateUndeletePosts(fields)
	case "reauthor-posts":
		s, err = p.validateReauthorPosts(fields)
	case "search-posts":
		s, err = p.validateSearchPosts(fields)
	// NEW COMMAND? Add case here for validation function.
	default:
		return "", fmt.Errorf("%s", adminUsage())
	}

	if err != nil {
		return "", fmt.Errorf("%s\n\n%s", adminCommandUsage[fields[1]], err)
	} else {
		return s, nil
	}
}

func (p *Plugin) runCommand(fields []string, context adminCommandContext) error {
	if len(fields) < 2 {
		return fmt.Errorf("%s", adminUsage())
	}
	switch fields[1] {
	case "move-town-square":
		return p.moveTownSquare(fields, context)
	case "rename-team":
		return p.renameTeam(fields, context)
	case "gdpr-clean-user":
		return p.GDPRCleanUser(fields, context)
	case "soft-delete-posts":
		return p.softDeletePosts(fields, context)
	case "undelete-posts":
		return p.undeletePosts(fields, context)
	case "reauthor-posts":
		return p.reauthorPosts(fields, context)
	case "search-posts":
		return p.searchPosts(fields, context)
	// NEW COMMAND? Add case here for live function.
	default:
		return fmt.Errorf("unknown administrative command %q", fields[1])
	}
}
