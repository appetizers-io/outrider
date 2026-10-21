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

The portable MCP server remains read-only. Crew-specific folder discovery,
checkout inspection and repository registration are implemented by the app's
Python adapter in `apps/kiro-crew/backend/mcp_launcher.py` and `mcp_setup.py`.
They are not CLI tools or flags. See the [Crew app guide](../apps/kiro-crew/README.md#add-a-project-from-a-crew-conversation).

No portable MCP tool applies drafts, changes trust or posts to GitHub. Missing Crew settings/queue files produce
an empty instance; malformed files are reported rather than silently ignored.
