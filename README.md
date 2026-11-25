# Fysiksektionen Mattermost Plugin

Customizes things in mattermost.fysiksektionen.se. Currently inserts zero-width spaces in emojies to keep text emojies for :) and :D.

To easily deploy create `set_secret.sh` which exports MM\_ADMIN\_TOKEN as an environment variable. The script `deploy.sh` will then raise the file upload limit, build and upload, and lower the upload limit to the old value.

The `/keycloak` command requires system management permission. Its desktop
autocomplete suggestion is restricted to system admins; command lists and some
clients may still display it to other users. Execution remains permission-checked.
Configure **Keycloak: Moderation log channel** to record successful Keycloak
ban and unban decisions, including the ban reason. Ensure the plugin bot can post
in that channel; clear the selection to disable the log. A ban deactivates the Mattermost
account, while an unban does not reactivate it. Log delivery failures are reported
to the command issuer and the server log.

`/keycloak get @user` displays the user's Mattermost authentication service and
auth data alongside their linked Keycloak profile and all its attributes. It
matches Keycloak's `mattermostID` against the Mattermost auth data and shows the
result only to the caller.

`/keycloak set-login-method @user google|saml` sets Google/OpenID or Keycloak/SAML
authentication when both providers have an account for the Mattermost
authentication ID. This enables Keycloak's Mattermost
login permission for SAML and disables it for OpenID, including when the
requested method is already selected.

`/keycloak delete-user by-email <mattermost-email> [--also-deactivate]`
permanently deletes the Keycloak account whose `mattermostEmail` attribute
matches. `/keycloak delete-user by-authdata @user [--also-deactivate]` matches
the Mattermost user's auth data against Keycloak's `mattermostID` instead.
The optional flag also deactivates the Mattermost user; the email form resolves
that user by email. Without it, a user still using SAML can no longer log in
through Keycloak.

`/keycloak migrate-to-google create-kth` and `bind` default to Google/OpenID;
the optional final `--keep-mamo-login-through-keycloak` flag keeps Keycloak/SAML.
`create-external` defaults to Keycloak/SAML; use the optional final
`--force-mamo-login-through-google` flag to select Google/OpenID instead.
Keycloak's Mattermost login permission is enabled for SAML and disabled for
Google/OpenID. `/keycloak migrate-to-keycloak` migrates in the other direction.
Migration notification examples state the resulting method and permission; configured
templates are preserved, and `{{account}}` is the full Google email address.
`{{login_instructions}}` inserts instructions for the actual login path: external
accounts use the KTH button with their `@fysiksektionen.se` account, KTH accounts
use the KTH button directly, and accounts retaining SAML can still use their
existing SAML login. Existing templates using `{{login_method}}` remain supported;
templates without either placeholder have the appropriate instructions appended.
Review existing customized templates for login instructions before sending them.

## Message statistics

Signed-in users can open `/plug/se.fysiksektionen.mattermost.plugin/stats` to compare
message counts, character totals, and characters per message. Filter by team or public
channel and select users individually, by channel membership, or by thread authors.
Press **Show** to load results. The URL preserves the filters for sharing.

Defaults to **All time** and cumulative totals; **Per day** shows daily counts.
Days use UTC. Deleted and system posts are excluded. Custom ranges allow up to
366 days, with a maximum of 200 users per query.

All/team totals include private channels, and **Include DMs** adds all selected users'
direct and group messages. Channel selectors only list active public channels across
all teams; thread selection requires read access. Reports return aggregates, not message text.

## Translator

The Translator module uses a configurable Mistral model to translate configured
Mattermost posts and posts marked with the `english` reaction. The
`translator-bot` account must be a member of a channel for the module to
process reactions, but this is not required for explicitly configured channels. Automatic translations can be replies or direct messages.

Configure the Mistral API key and model, the dictionary text,
system and user prompts, and optional test/error and audit channel IDs in the
Mistral and Translator settings. The model selector loads chat models from `/models`
using the saved key and shows context, capabilities, and pricing when provided.
Reasoning levels follow Mistral's documentation, since `/models` does not list them.
Large 4 defaults to `low`; it cannot disable thinking. Save to apply changes.
The default model is `ministral-14b-2512`. Requests allow 120 seconds; translations
include final-answer text only.
System prompt, user prompt, and dictionary are required when a translation is
requested. Copy the current dictionary post into **Translator: Dictionary** and
save; the bot no longer reads that post. Errors are written to the Mattermost
log, posted to the test/error channel when configured, and reported to the
requesting user in the configured reply or direct-message context.
Successful translations are posted to the audit channel only when an audit
channel is configured.

## Google Groups

Configure **Google Groups Sync: Service account JSON key** with a service
account authorized for domain-wide delegation and the Google Directory API
`admin.directory.group.readonly` and `admin.directory.user.readonly`, and Cloud
Identity API `cloud-identity.groups.readonly` and `cloud-identity.groups`
scopes. Enable the Cloud Identity API for the service account's Google Cloud
project.
Group reads are delegated to the invoking user's account in the configured
**Google Groups Sync: organization domain**. Mattermost users without an
organization address use **Google Groups Sync: fallback account email** for
group reads. Select Mattermost users in **Google Groups Sync: admin delegation
users** and configure **Google Groups Sync: admin email** to automatically use
that admin account for their channel operations instead. Other users still use
their own or the fallback account. Google Group management always uses the
caller's own organization account, never switching to admin or fallback
delegation. `/google group manage` resolves groups, reads direct members, and
adds or removes members through the Cloud Identity API, so Google Groups
owners and managers can manage groups without Directory API group permissions.
For channel operations, the plugin reads the group and its direct and indirect
members through the Directory API using the delegated account. If Google
denies that read, the plugin checks direct-member access with Cloud Identity
using the same account, then retries the Directory read using the configured
admin email. Other errors are not retried this way.

Cloud Identity's direct membership listing is used to check access, not to
expand nested groups: its [transitive membership API](https://docs.cloud.google.com/identity/docs/reference/rest/v1/groups.memberships/searchTransitiveMemberships)
requires permission to view all transitive memberships and a qualifying Google
Workspace or Cloud Identity edition. The plugin uses the resulting Directory
membership list with the Mattermost API to change channel membership; it does
not add users to Mattermost through Cloud Identity.

In `/google group` commands, group names may omit the domain; the configured
organization domain is appended (for example, `team` becomes
`team@fysiksektionen.se` with the default domain). Full group addresses remain
supported. **Google Groups Sync: alternate Mattermost email domain** defaults to
`f.kth.se`. User addresses in that domain are treated as the same local part in
the configured organization domain for `/google` delegation, group management,
and channel membership matching. Clear the setting to disable the mapping.
When both addresses exist in Mattermost, channel operations prefer the exact
Google member address.

System or team admins can run these commands:

- `/google group sync start <group-email> <YYYY-MM-DD>` previews the group and opens a
  confirmation dialog. On confirmation, active group members with a matching
  Mattermost account who already belong to the channel's team are added to the
  channel. The sync runs hourly through 23:59:59 UTC on the deadline date.
  Team admins can choose a deadline up to one year from the current UTC date;
  only system admins can choose a later deadline.
- `/google group sync stop <group-email>` confirms and stops future additions.
  Existing channel members are never removed by sync or by removing a sync.
- `/google group sync list` shows active syncs configured for the current channel.
- `/google group sync now` previews and immediately runs all active syncs
  for the current channel, without changing their hourly schedules or deadlines.

Channel sync includes users with direct or indirect (nested-group) memberships;
only user members are added, never groups. Manual syncs use the requesting
admin's delegated account, subject to the admin retry described above. Scheduled
syncs use the account recorded when the sync was created, including the admin
account if it was needed for the initial read. Sync creation and every manual
or scheduled run check the creator's own organization account for direct-member
access. Scheduled syncs pause if the creator loses that access or the required
admin permissions, or if a recorded account changes. Commands require membership
in the target channel, and listing syncs requires access to every listed group.
Sync reports identify the creator's Mattermost user ID and the Google account
used for scheduled runs.
System admins can view configured syncs across teams in the Google Workspace
Integration settings. The read-only overview includes expired records awaiting
cleanup; use the sync commands to change them.

The confirmation shows the group name and address, member count, first five
members alphabetically by email, and channel name. It expires after five
minutes, and the group membership is rechecked before applying it. Failed
scheduled syncs are logged and retried on the next hourly run.

`/google group add <group-email>` and `/google group remove <group-email>` add or remove
matching direct and indirect Google Group users from the current channel once,
without changing Google Group membership or sync configuration. They require
confirmation and Google read access. Groups above the configurable team-admin
threshold (default 30 members) require a system or team admin; groups above
the system-admin threshold (default 100) require a system admin. Thresholds
must be non-negative, and the system-admin threshold must be at least the
team-admin threshold.
These limits also apply to sync additions, removals, and runs. You cannot remove
yourself. An active sync may add removed members again.

`/google group manage <group-email> get` lists all direct members, including
directly nested groups, without expanding those groups. The result is visible
only to the caller. Google determines whether the caller can read the group.

`/google group manage <group-email> add/remove @<username>` adds or removes
the selected Mattermost user's Google account directly in Google Groups after
confirmation. Both the caller and target must have accounts in the configured
organization domain. Google decides whether the caller can change that group;
the plugin does not use group settings to infer permission. Only Google users,
not groups or the configured fallback account, can be added; removal applies
only to direct user membership. You cannot remove yourself, including through
another address for the same Google account. Removal requires Google Directory
access to verify both accounts.

## Default channels

Enable **Default Channels: Module enabled?** in the plugin settings. Team admins
(including system admins) can run:

- `/default_channel set`: make the current public or private channel a default
  for its team and queue a one-time addition of all active team members, including
  guests. An ephemeral message confirms completion. Repeating `set` is a no-op.
- `/default_channel unset`: stop automatic additions without removing members or
  changing sidebar categories.
- `/default_channel list`: list this team's active default channels and their
  current Mattermost default categories (`Channels` when no category is set).
  Town Square appears separately as a built-in team channel managed by Mattermost,
  even if it isn't in the plugin config.

New team members are added automatically. Additions run in the background, retry
failures, and resume after restarts. Archived channels, deactivated users, and
users who have left the team are skipped. Mattermost membership restrictions still
apply; failed additions are logged and remain pending rather than reporting success.
Disabling the module pauses pending work and ignores new join events. Members may
leave channels after being added; membership is not continuously enforced.

In the System Console, Add and Remove only edit the draft. Press **Save** to
persist the channel list and queue a backfill for newly added defaults. Leaving
without saving discards the draft. If queueing fails after the config is saved,
press Save again to retry; retries preserve running and completed backfills.
Slash commands still apply immediately. Startup does not backfill existing defaults.

Defaults are stored as a flat channel-ID list in `DefaultChannels_Custom`.
Configure categories in Mattermost's channel settings. Existing members' personal
placement is not changed by adding them again. Avoid simultaneous System Console edits and commands,
since the server's configuration API does not support compare-and-swap updates.

## Default channel category synchronization

Requires Mattermost 11.11.0+, a reachable `ServiceSettings.SiteURL`, and the Go version in `go.mod`.

**Team Join Category Fix: Enabled?** is enabled by default. When a user joins a
team, their joined channels with a default category are queued for placement in
that category, including Mattermost's built-in default channels. This setting is
independent of the Default Channels module and does not cancel already queued
jobs when disabled. Failures to look up channels or queue jobs are logged.
International onboarding's configured-channel placement is controlled separately.

Changing a channel's default category moves it for all members, overriding Favorites
and personal placement, then deletes affected custom categories with no active channels.
Archived channels are kept and fall back to Channels if restored. Clearing the default
moves the channel to Channels. Sync runs in the background, retries failures, and
resumes after restarts; it does not enforce placement continuously. Team admins can run
`/force_sync_categories` to apply existing defaults to all members of all active public
and private channels in the current team, with the same placement and cleanup behavior.
An ephemeral completion report shows channels moved (including member-sidebar moves)
and categories created/deleted. Counts track successful operations; a crash between a
change and saving its count can undercount.

Startup ensures `f.kth.se-plugin-bot` exists with `system_admin`. REST requests use
short-lived bot sessions; tokens stay in memory and existing access tokens are untouched.
Concurrent manual sidebar edits can race with synchronization, including empty-category deletion.

## Team leave cleanup workaround

For server-initiated team leaves, the plugin webapp applies Mattermost's per-channel
client cleanup and clears remaining stale post-order lists. This works around a
Mattermost 11.11 issue when rejoining without a reload. It only applies while the
plugin webapp is loaded; it does not make additional channel-leave API requests.

* Issue related to this: https://github.com/mattermost/mattermost/issues/40273

Verify with `go test -race ./...`, `go vet ./...`, and `make dist`.

Se template readme below:

# Plugin Starter Template [![CircleCI branch](https://img.shields.io/circleci/project/github/mattermost/mattermost-plugin-starter-template/master.svg)](https://circleci.com/gh/mattermost/mattermost-plugin-starter-template)

This plugin serves as a starting point for writing a Mattermost plugin. Feel free to base your own plugin off this repository.

To learn more about plugins, see [our plugin documentation](https://developers.mattermost.com/extend/plugins/).

This template requires node v16 and npm v8. You can download and install nvm to manage your node versions by following the instructions [here](https://github.com/nvm-sh/nvm). Once you've setup the project simply run `nvm i` within the root folder to use the suggested version of node.

## Getting Started
Use GitHub's template feature to make a copy of this repository by clicking the "Use this template" button.

Alternatively shallow clone the repository matching your plugin name:
```
git clone --depth 1 https://github.com/mattermost/mattermost-plugin-starter-template com.example.my-plugin
```

Note that this project uses [Go modules](https://github.com/golang/go/wiki/Modules). Be sure to locate the project outside of `$GOPATH`.

Edit the following files:
1. `plugin.json` with your `id`, `name`, and `description`:
```json
{
    "id": "com.example.my-plugin",
    "name": "My Plugin",
    "description": "A plugin to enhance Mattermost."
}
```

2. `go.mod` with your Go module path, following the `<hosting-site>/<repository>/<module>` convention:
```
module github.com/example/my-plugin
```

3. `.golangci.yml` with your Go module path:
```yml
linters-settings:
  # [...]
  goimports:
    local-prefixes: github.com/example/my-plugin
```

Build your plugin:
```
make
```

This will produce a single plugin file (with support for multiple architectures) for upload to your Mattermost server:

```
dist/com.example.my-plugin.tar.gz
```

## Development

To avoid having to manually install your plugin, build and deploy your plugin using one of the following options. In order for the below options to work, you must first enable plugin uploads via your config.json or API and restart Mattermost.

```json
    "PluginSettings" : {
        ...
        "EnableUploads" : true
    }
```

### Deploying with Local Mode

If your Mattermost server is running locally, you can enable [local mode](https://docs.mattermost.com/administration/mmctl-cli-tool.html#local-mode) to streamline deploying your plugin. Edit your server configuration as follows:

```json
{
    "ServiceSettings": {
        ...
        "EnableLocalMode": true,
        "LocalModeSocketLocation": "/var/tmp/mattermost_local.socket"
    },
}
```

and then deploy your plugin:
```
make deploy
```

You may also customize the Unix socket path:
```bash
export MM_LOCALSOCKETPATH=/var/tmp/alternate_local.socket
make deploy
```

If developing a plugin with a webapp, watch for changes and deploy those automatically:
```bash
export MM_SERVICESETTINGS_SITEURL=http://localhost:8065
export MM_ADMIN_TOKEN=j44acwd8obn78cdcx7koid4jkr
make watch
```

### Deploying with credentials

Alternatively, you can authenticate with the server's API with credentials:
```bash
export MM_SERVICESETTINGS_SITEURL=http://localhost:8065
export MM_ADMIN_USERNAME=admin
export MM_ADMIN_PASSWORD=password
make deploy
```

or with a [personal access token](https://docs.mattermost.com/developer/personal-access-tokens.html):
```bash
export MM_SERVICESETTINGS_SITEURL=http://localhost:8065
export MM_ADMIN_TOKEN=j44acwd8obn78cdcx7koid4jkr
make deploy
```

### Releasing new versions

The version of a plugin is determined at compile time, automatically populating a `version` field in the [plugin manifest](plugin.json):
* If the current commit matches a tag, the version will match after stripping any leading `v`, e.g. `1.3.1`.
* Otherwise, the version will combine the nearest tag with `git rev-parse --short HEAD`, e.g. `1.3.1+d06e53e1`.
* If there is no version tag, an empty version will be combined with the short hash, e.g. `0.0.0+76081421`.

To disable this behaviour, manually populate and maintain the `version` field.

## How to Release

To trigger a release, follow these steps:

1. **For Patch Release:** Run the following command:
    ```
    make patch
    ```
   This will release a patch change.

2. **For Minor Release:** Run the following command:
    ```
    make minor
    ```
   This will release a minor change.

3. **For Major Release:** Run the following command:
    ```
    make major
    ```
   This will release a major change.

4. **For Patch Release Candidate (RC):** Run the following command:
    ```
    make patch-rc
    ```
   This will release a patch release candidate.

5. **For Minor Release Candidate (RC):** Run the following command:
    ```
    make minor-rc
    ```
   This will release a minor release candidate.

6. **For Major Release Candidate (RC):** Run the following command:
    ```
    make major-rc
    ```
   This will release a major release candidate.

## Q&A

### How do I make a server-only or web app-only plugin?

Simply delete the `server` or `webapp` folders and remove the corresponding sections from `plugin.json`. The build scripts will skip the missing portions automatically.

### How do I include assets in the plugin bundle?

Place them into the `assets` directory. To use an asset at runtime, build the path to your asset and open as a regular file:

```go
bundlePath, err := p.API.GetBundlePath()
if err != nil {
    return errors.Wrap(err, "failed to get bundle path")
}

profileImage, err := ioutil.ReadFile(filepath.Join(bundlePath, "assets", "profile_image.png"))
if err != nil {
    return errors.Wrap(err, "failed to read profile image")
}

if appErr := p.API.SetProfileImage(userID, profileImage); appErr != nil {
    return errors.Wrap(err, "failed to set profile image")
}
```

### How do I build the plugin with unminified JavaScript?
Setting the `MM_DEBUG` environment variable will invoke the debug builds. The simplist way to do this is to simply include this variable in your calls to `make` (e.g. `make dist MM_DEBUG=1`).
