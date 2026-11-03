package main

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// TODO write better module description with warnings at the top of this file
// TODO prefix method names to avoid collisions

/*
	/mm is a set of administrative commands for managing the Mattermost instance.
*/

var adminCommandNames = []string{
	"move-town-square",
	"rename-team",
	"gdpr-clean-user",
	"soft-delete-posts",
	"undelete-posts",
	"reauthor-posts",
	"search-posts",
	// NEW COMMAND? add name here
}
var adminCommandUsage = map[string]string{
	"move-town-square":  "Usage: /mm move-town-square [team] ~[old-town-square] [new-name-for-old-town-square] ~[channel-to-make-town-square]",
	"rename-team":       "Usage: /mm rename-team [team] [new-team-name]",
	"gdpr-clean-user":   "Usage: /mm gdpr-clean-user @[user] [lite|soft|hard] [default|destination-ID]",
	"soft-delete-posts": "Usage: /mm soft-delete-posts [specification-message-id]",
	"undelete-posts":    "Usage: /mm undelete-posts [specification-message-id]",
	"reauthor-posts":    "Usage: /mm reauthor-posts [specification-message-id]",
	"search-posts":      "Usage: /mm search-posts \"[regex]\"",
	// NEW COMMAND? add usage string here
}
var adminCommandDescription = map[string]string{
	"move-town-square":  "Switches which channel is the Town Square channel for a team.",
	"rename-team":       "Renames a team (URL name).",
	"gdpr-clean-user":   "Clean's a user's data. Lite anonymizes the user; soft also marks posts and files as deleted; hard also queues posts and files for deletion from the filesystem. (Hard deletion is not implemented.)",
	"soft-delete-posts": "According to specification, marks an array of post IDs from the specified message ID as deleted, and saves which were deleted to a message.",
	"undelete-posts":    "According to specification, marks an array of post IDs from the specified message ID as undeleted, and saves which were undeleted to a message.",
	"reauthor-posts":    "According to specification, changes the author of an array of post IDs from the specified message ID to a new author, and saves which were reauthored to a message (and the original author).",
	"search-posts":      "Search for posts matching a regular expression.",
	// NEW COMMAND? add description string here
}

/*******************
* MOVE-TOWN-SQUARE *
*******************/

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

/* Move the town square from the old channel to the new channel, and rename the old town square channel to the new name. */
func (p *Plugin) moveTownSquare(fields []string, context adminCommandContext) error {
	// TODO rename
	if _, err := p.validateMoveTownSquare(fields, context); err != nil {
		return err
	}
	return fmt.Errorf("Not implemented.")
}

/**************
* RENAME-TEAM *
**************/

/*
	/mm rename-team [team] [new-team-name]

Checks:
1. That the command is exactly 4 fields long (including /mm)
2. That the team exists and is the current team
3. That the new team name is valid and does not already exist
*/
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

func (p *Plugin) renameTeam(fields []string, context adminCommandContext) error {
	if _, err := p.validateRenameTeam(fields, context); err != nil {
		return err // re-validate for good measure
	}
	return fmt.Errorf("Not implemented.")
}

/******************
* GDPR-CLEAN-USER *
******************/

/*
	/mm gdpr-clean-user @[user] [lite|soft|hard] [default|destination-ID]

Checks:
1. That the command is exactly 5 fields long (including /mm)
2. That the user exists
3. That the destination ID is valid and not already occupied by another user
4. That the cleanup mode is valid (lite, soft or hard)
*/
func (p *Plugin) validateGDPRCleanUser(fields []string, search_posts_files bool) (string, error) {
	if len(fields) < 2 || fields[0] != "/mm" || fields[1] != "gdpr-clean-user" {
		return "", fmt.Errorf("Major internal error: validateGDPRCleanUser called with wrong initial fields.")
	}
	if len(fields) != 5 {
		return "", fmt.Errorf("Wrong number of fields for /mm gdpr-clean-user.")
	}

	username := strings.TrimPrefix(fields[2], "@")
	user, appErr := p.API.GetUserByUsername(username)
	if appErr != nil || user == nil {
		return "", fmt.Errorf("Invalid user @%s.", username)
	}

	destinationID := ""
	if fields[4] != "default" {
		if !regexp.MustCompile(`^[A-Za-z0-9]+$`).MatchString(fields[4]) {
			return "", fmt.Errorf("Invalid destination ID %q. Use only alphanumeric characters or pass - for the default ID.", fields[4])
		}
		destinationID = fields[4]
	}
	if destinationID == "" {
		destinationID = user.Id
		if user.AuthData != nil && *user.AuthData != "" {
			destinationID = *user.AuthData
		}
	}
	destinationEmail := fmt.Sprintf("deactivated+%s@mattermost.invalid", destinationID)
	destinationUsername := fmt.Sprintf("deactivated+%s", destinationID)

	destinationUser, lookupErr := p.API.GetUserByEmail(destinationEmail)
	if lookupErr != nil && lookupErr.StatusCode != http.StatusNotFound {
		return "", fmt.Errorf("Could not check whether destination e-mail %q is occupied.", destinationEmail)
	}
	if destinationUser != nil {
		return "", fmt.Errorf("Destination e-mail %q is already occupied.", destinationEmail)
	}

	destinationUser, lookupErr = p.API.GetUserByUsername(destinationUsername)
	if lookupErr != nil && lookupErr.StatusCode != http.StatusNotFound {
		return "", fmt.Errorf("Could not check whether destination username %q is occupied.", destinationUsername)
	}
	if destinationUser != nil {
		return "", fmt.Errorf("Destination username %q is already occupied.", destinationUsername)
	}

	switch fields[3] {
	case "lite": // Lite deletion: only anonymize the user, but keep posts and files
		return fmt.Sprintf(
			`Handle **lite** GDPR deletion of user @%s.

			With lite deletion, the following changes will occur:
			1. The first name will be changed to "Deactivated"
			2. The last name will be changed to "User"
			3. The email will be changed to %q
			4. The username will be changed to %q

			> Validation checks passed.
			> * :white_check_mark: User @%s exists.
			> * :white_check_mark: Destination e-mail %q is available.
			> * :white_check_mark: Destination username %q is available.
			> * :white_check_mark: %s cleanup mode is valid.`,
			username, destinationEmail, destinationUsername,
			username, destinationEmail, destinationUsername, fields[3]), nil

	case "soft": // Soft deletion: anonymize the user and mark all posts and files as deleted in the database
		var postCount = int64(-1)
		var fileCount = int64(-1)
		if search_posts_files {
			pC, err := p.postCountForUser(user.Id)
			if err != nil {
				return "", fmt.Errorf("Could not count posts for user @%s: %w", username, err)
			}
			fC, err := p.fileCountForUser(user.Id)
			if err != nil {
				return "", fmt.Errorf("Could not count files for user @%s: %w", username, err)
			}
			postCount = pC
			fileCount = fC
		}

		return fmt.Sprintf(
			`Handle **soft** GDPR deletion of user @%s.

			With soft deletion, the following changes will occur:
			1. The first name will be changed to "Deactivated"
			2. The last name will be changed to "User"
			3. The email will be changed to %q
			4. The username will be changed to %q
			5. All posts will be marked as deleted in the database (but not removed from the filesystem)
			  * There are %d undeleted posts in the database for this user. (-1 means not calculated.)
			6. All files will be marked as deleted in the database (but not removed from the filesystem)
			  * There are %d undeleted files in the database for this user. (-1 means not calculated.)

			> Validation checks passed.
			> * :white_check_mark: User @%s exists.
			> * :white_check_mark: Destination e-mail %q is available.
			> * :white_check_mark: Destination username %q is available.
			> * :white_check_mark: %s cleanup mode is valid.`,
			username, destinationEmail, destinationUsername, postCount, fileCount,
			username, destinationEmail, destinationUsername, fields[3]), nil
	case "hard": // Hard deletion: anonymize the user and delete all posts and files in the database, and queue them for deletion from the filesystem.
		return "", fmt.Errorf("The **hard** deletion mode is NOT implemented. Do so at your own risk if you need it.")

		/* NOT IMPLEMENTED
		postCount, err := p.postCountForUser(user.Id)
		if err != nil {
			return "", fmt.Errorf("Could not count posts for user @%s: %w", username, err)
		}

		fileCount, err := p.fileCountForUser(user.Id)
		if err != nil {
			return "", fmt.Errorf("Could not count files for user @%s: %w", username, err)
		}

		return `# THIS IS A DANGEROUS METHOD. PROCEED WITH CAUTION.

			Handle **HARD** GDPR deletion of user @%s.
			With **HARD** deletion, the following changes will occur:
			1. The first name will be changed to "Deactivated"
			2. The last name will be changed to "User"
			3. The email will be changed to %q
			4. The username will be changed to %q
			5. All posts will be marked as deleted in the database and queued for deletion from the filesystem.
			  * There are %d undeleted posts in the database for this user. (-1 means not calculated.)
			6. All files will be marked as deleted in the database and queued for deletion from the filesystem.
			  * There are %d undeleted files in the database for this user. (-1 means not calculated.)

			> Validation checks passed.
			> * :white_check_mark: User @%s exists.
			> * :white_check_mark: Destination e-mail %q is available.
			> * :white_check_mark: Destination username %q is available.
			> * :white_check_mark: %s cleanup mode is valid.`,
		*/
	default:
		return "", fmt.Errorf("Invalid cleanup mode %q. Use lite, soft or hard.", fields[3])
	}
}

/* WARNING: Untested, may take a long time. */
func (p *Plugin) postCountForUser(userID string) (int64, error) {
	pageSize := 200

	teams, appErr := p.API.GetTeamsForUser(userID)
	if appErr != nil {
		return 0, fmt.Errorf("get teams for user %q: %w", userID, appErr)
	}

	var count int64
	for _, team := range teams {
		if team == nil {
			continue
		}

		page := 0
		for {
			searchParams := model.SearchParameter{
				Page:    &page,
				PerPage: &pageSize,
			}
			results, searchErr := p.API.SearchPostsInTeamForUser(team.Id, userID, searchParams)
			if searchErr != nil {
				return 0, fmt.Errorf("search posts in team %q for user %q: %w", team.Id, userID, searchErr)
			}
			if results == nil || results.PostList == nil {
				break
			}

			count += int64(len(results.Order))
			if results.HasNext == nil || !*results.HasNext {
				break
			}
			page++
		}
	}

	return count, nil
}

/* WARNING: Untested, may take a long time. */
func (p *Plugin) fileCountForUser(userID string) (int64, error) {
	const pageSize = 200
	options := &model.GetFileInfosOptions{
		UserIds:        []string{userID},
		IncludeDeleted: false,
	}

	var count int64
	for page := 0; ; page++ {
		files, appErr := p.API.GetFileInfos(page, pageSize, options)
		if appErr != nil {
			return 0, fmt.Errorf("get files for user %q: %w", userID, appErr)
		}

		count += int64(len(files))
		if len(files) < pageSize {
			return count, nil
		}
	}
}

func (p *Plugin) GDPRCleanUser(fields []string, context adminCommandContext) error {
	if _, err := p.validateGDPRCleanUser(fields, false); err != nil {
		return err // re-validate for good measure
	}
	return fmt.Errorf("Not implemented.")
}

/********************
* SOFT-DELETE-POSTS *
********************/

func (p *Plugin) validateSoftDeletePosts(fields []string) (string, error) {
	return "", fmt.Errorf("Not implemented.")

	/* if len(fields) != 3 || !model.IsValidId(fields[2]) {
		return "", fmt.Errorf("%s", adminCommandUsage["soft-delete-posts"])
	}
	return fmt.Sprintf("Soft-delete post %s.", fields[2]), nil */
}

func (p *Plugin) softDeletePosts(fields []string, context adminCommandContext) error {
	if _, err := p.validateSoftDeletePosts(fields); err != nil {
		return err // re-validate for good measure
	}
	return fmt.Errorf("Not implemented.")
}

/*****************
* UNDELETE-POSTS *
******************/

func (p *Plugin) validateUndeletePosts(fields []string) (string, error) {
	return "", fmt.Errorf("Not implemented.")

	/* if len(fields) != 3 || !model.IsValidId(fields[2]) {
		return "", fmt.Errorf("%s", adminCommandUsage["undelete-posts"])
	}
	return fmt.Sprintf("Undelete post %s.", fields[2]), nil */
}

func (p *Plugin) undeletePosts(fields []string, context adminCommandContext) error {
	if _, err := p.validateUndeletePosts(fields); err != nil {
		return err // re-validate for good measure
	}
	return fmt.Errorf("Not implemented.")
}

/*****************
* REAUTHOR-POSTS *
*****************/

func (p *Plugin) validateReauthorPosts(fields []string) (string, error) {
	return "", fmt.Errorf("Not implemented.")

	/* if len(fields) != 3 || !model.IsValidId(fields[2]) {
		return "", fmt.Errorf("%s", adminCommandUsage["reauthor-posts"])
	}
	return fmt.Sprintf("Reauthor post %s.", fields[2]), nil */
}

func (p *Plugin) reauthorPosts(fields []string, context adminCommandContext) error {
	if _, err := p.validateReauthorPosts(fields); err != nil {
		return err // re-validate for good measure
	}
	return fmt.Errorf("Not implemented.")
}

/***************
* SEARCH-POSTS *
****************/

func (p *Plugin) validateSearchPosts(fields []string) (string, error) {
	return "", fmt.Errorf("Not implemented.")

	/* if len(fields) != 3 {
		return "", fmt.Errorf("%s", adminCommandUsage["search-posts"])
	}
	if _, err := regexp.Compile(fields[2]); err != nil {
		return "", fmt.Errorf("Invalid search regex: %w", err)
	}
	return fmt.Sprintf("Search posts using regular expression %q.", fields[2]), nil */
}

func (p *Plugin) searchPosts(fields []string, context adminCommandContext) error {
	if _, err := p.validateSearchPosts(fields); err != nil {
		return err // re-validate for good measure
	}
	return fmt.Errorf("Not implemented.")
}

// NEW COMMAND? Copy the below (please keep the reference)

/**************
* NEW-COMMAND *
**************/

func (p *Plugin) validateNewCommand(fields []string) (string, error) {
	return "", fmt.Errorf("Not implemented.")
}

func (p *Plugin) newCommand(fields []string, context adminCommandContext) error {
	if _, err := p.validateNewCommand(fields); err != nil {
		return err // re-validate for good measure
	}
	return fmt.Errorf("Not implemented.")
}
