# Fysiksektionen Mattermost Plugin

Customizes things in mattermost.fysiksektionen.se. Currently inserts zero-width spaces in emojies to keep text emojies for :) and :D.

To easily deploy create `set_secret.sh` which exports MM\_ADMIN\_TOKEN as an environment variable. The script `deploy.sh` will then raise the file upload limit, build and upload, and lower the upload limit to the old value.

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
