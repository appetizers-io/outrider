# Outrider MCP server

Outrider includes a portable MCP server using the
[official Go SDK](https://github.com/modelcontextprotocol/go-sdk). The server
runs over stdio in the same binary as the CLI; no Python or Node MCP framework
is needed. Keep stdout reserved for protocol messages; diagnostics use stderr.

Both paths are required. The server does not discover a host config from
`OUTRIDER_CONFIG`, XDG, or `~/.config` and does not start polling or launch agents.

```json
{
  "mcpServers": {
    "outrider": {
      "command": "/absolute/path/to/outrider",
      "args": [
        "mcp", "--config-format", "cli",
        "--config", "/absolute/path/to/config.yaml",
        "--state-dir", "/absolute/path/to/outrider-state"
      ]
    }
  }
}
```

For a Crew instance use `--config-format crew`, point `--config` to that app's
`data/settings.json`, and `--state-dir` to its `data` directory. Crew's app
manifest registers a small launcher that supplies these paths automatically.
It lives under Crew's installed app directory only as an adapter; the MCP server
implementation is `internal/mcpserver` in Outrider.

## Tools

- `list_repositories`: read the selected config's repository/task settings. CLI
  mode omits execution settings and classifier commands/environment. Crew mode
  returns watched repositories and their per-repository task configuration.
- `configuration_schema`: return the Outrider schema and supported repository
  draft fields. Crew execution settings are not accepted in repository drafts.
- `list_reviews`: read the queue under `inbox/queue` and `runs.json` /
  `validations.json` in the explicit state directory. Returns up to 100 recent
  entries, optional repository filter, and linked session status. It does not
  expose raw captured PR text. Standalone watcher state is a different format;
  this tool does not claim to list standalone terminal sessions.
- `propose_repository_config`: validate task YAML and return normalized YAML,
  settings and `applied: false`. Ownership overrides, triggers, workflows,
  push/post task restrictions and review-fork settings are supported. Save the
  reviewed draft yourself through Crew or the appropriate CLI config file.

Standalone MCP remains read-only. With `--config-format crew --crew-control`
(the app launcher supplies both), three additional tools use the running app:

- `list_crew_folders`: discover folder IDs and project directories.
- `inspect_crew_folder`: resolve inherited checkout paths and GitHub remotes.
- `add_repository`: register an owner/repo with a Crew folder and matching remote.
  Existing registrations are unchanged; unrelated settings and Crew permissions
  are preserved. New repositories inherit polling and automatic-session defaults,
  so adding one can lead to reviews under the user's existing configuration.

For example: **“Add this project to Outrider.”** The agent discovers the matching
Crew folder, checks its remotes, and calls `add_repository`. If there are multiple
possible projects or upstream repositories, it asks which one to use. No UI
automation or manual configuration-file edits are needed.

Setup tools require an enabled, running app and a compatible Outrider binary.
The backend exposes only these setup routes via a rotating local bearer token
in its private data directory. The MCP client connects only to 127.0.0.1, does
not forward credentials through proxies or redirects, and never retries writes.
The token cannot access other backend APIs. No tool changes trust, applies an
arbitrary draft, marks GitHub notifications or posts to GitHub. Missing Crew settings/queue files produce
an empty instance; malformed files are reported rather than silently ignored.
