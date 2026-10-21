# Outrider for Kiro Crew

A Crew app backed by Outrider's Go notification poller. Choose repositories,
scan actionable PR activity, and click **Review snapshot**. Findings appear in
the app; nothing is posted to GitHub.

## Install from this checkout

1. Run `task install` at the Outrider repository root. This builds the version
   with `outrider crew scan`. Verify `outrider crew --help` works from the
   environment that launches Crew, and authenticate `gh` with `gh auth login`.
2. In Crew's App Store, choose **Install from local path** and select this
   `apps/kiro-crew` directory. Enable the app. It needs Python 3 and a current
   Crew gateway that supplies `KIROCREW_GATEWAY_ORIGIN` to app backends.
3. Grant this app session control in Crew's trust settings to permit reviews.
4. Open **Outrider**, enter repositories as `owner/repo`, and save. The first
   scan includes existing activity in the poller's default lookback window.
5. Click **Review snapshot** on an item. One review runs at a time. Crew may
   also ask for approval before starting its task.

There is no frontend build step: `ui/index.mjs` imports React and the SDK from
Crew's shared modules. The Python backend uses only the standard library.
The app starts idle until you configure repositories. Clear the repository
list to pause polling. Disabling the app stops its backend; already-dispatched
Crew tasks remain managed by Crew.

## What this version does

- Reuses Outrider's own-PR, mention, review-reply, and 👀 triggers and launch check.
- Stores a separate inbox and poll state under
  `$KIROCREW_HOME/apps/outrider/data` (default `~/.kiro/crew/apps/outrider/data`).
- Captures the PR diff, metadata, conversation, review comments, and failed
  checks using read-only `gh` requests. GitHub notifications stay unread.
- Checks the PR head again after capture and retries if it changed.
- Sends a bounded snapshot to the `outrider-reviewer` Kiro agent, then retrieves
  the task result for display. Snapshots may contain private repository data;
  they are sent to the model configured in Crew when you request a review.
- Saves task IDs across backend restarts. An ambiguous dispatch becomes
  **uncertain**, requiring you to check Crew tasks before explicitly retrying.
  Missing tasks (HTTP 404/410) or five consecutive status-poll failures also
  become uncertain and release the review slot. Authentication failures and
  definitive request rejections are failed reviews, not ambiguous launches.

## Session launch and configuration precedence

Clicking **Review snapshot** submits `POST /api/spawn` to the Crew gateway with
`agent: outrider-reviewer`, the captured snapshot as `task`, the app data directory
as `cwd`, and `backend: kiro`. Crew starts a new background task; the app stores
its ID and polls `GET /api/spawn/{id}` for findings. It does not open a terminal,
launch Claude/Codex directly, or continue the currently selected chat.

Crew owns model selection, reasoning effort, turn limits, and approval behavior.
The app omits `model`, `reasoning_effort`, `max_turns`, and `approval_mode` from the
request and ships no model pin in its reviewer definition. Crew therefore applies
its own subagent role and agent/default configuration. A model selected only in
an unrelated open chat is not inherited. You can configure the reviewer's model
in Crew without failing the app's policy check.

The fixed adapter constraints are the reviewer identity, snapshot working
directory, Kiro backend request, and no-tools review policy. Older Crew releases
may ignore the backend request; use their Kiro runtime. This initial adapter does
not support other execution engines or broader reviewer tools.

Outrider controls event selection and snapshot capture. The app uses its own
repository list and polling interval, plus the poller's built-in defaults. It
never implicitly reads the normal Outrider YAML. Even when using the standalone
scanner's explicit `--config`, Outrider agent/model/terminal/session settings are
not forwarded to Crew.

## Review boundary

These are static snapshot reviews. They do not check out code, run tests, edit
files, create commits, push, reply to reviewers, or publish GitHub comments.
The reviewer has an empty tool list, no MCP imports, and a `preToolUse` hook
that denies every tool call. Before dispatch the backend checks the installed
agent's policy and refuses if it changed. Keep Crew on its Kiro backend; this
app explicitly requests that backend, since other agent engines may interpret
policy differently.

The app expects Crew's standard agent materialization location,
`~/.kiro/agents/outrider--outrider-reviewer.json`. Custom agent directory layouts
are not supported yet. Do not add tools to this agent to work around a failed
policy check.

Diffs are capped at 20 KB; metadata at 7 KB; inline comments at 4 KB; failed
checks at 2 KB. Truncation is visible in the app and prompt. A review applies to
the displayed captured commit, which may no longer be the PR's latest head.
Review profiles, custom playbooks, and test evidence are not executed by this
adapter. Configured workflows supply ordered snapshot-review instructions; steps
requiring tools are reported as blocked. The regular terminal/tmux
workflow remains available for full repository reviews.

The backend accepts only gateway-signed requests, binds to loopback, and
exchanges the app secret for an app-scoped gateway token. The browser receives
queue metadata and findings, not the raw snapshot prompt. Findings are rendered
as plain text, not executable HTML.

## Standalone scanner

From the Outrider repository root:

```sh
go run . crew scan --repo owner/repo --state-dir /path/to/crew-inbox --process-existing
```

`--config /path/to/config.yaml` optionally supplies polling/trigger/classifier
settings. Without it, built-in defaults apply; the regular user's Outrider
config is not loaded implicitly. Repository globs are accepted by the command;
the app's UI accepts explicit repositories only. Use a dedicated state directory,
never the terminal watcher's state directory. Without `--process-existing`, the
first scan records a baseline instead of queuing existing activity.

A queued snapshot is written atomically before the event is acknowledged. On
macOS/Linux the snapshot file and queue directory are synced before acknowledgement,
and a process lock is released automatically after a crash. Windows
standalone scans flush snapshot files but cannot guarantee directory durability
after power loss, and use a directory lock: after a crash, confirm no scan remains
running before removing `<state-dir>/scan.lock`. The Crew app targets macOS/Linux.

## Development and verification

From the Outrider repository root:

```sh
task all
task crew:test
kiro-cli agent validate --path apps/kiro-crew/agents/reviewer.json
```

Tests mock GitHub and Crew, exercise HTTP signatures and dispatch recovery, and
never start paid reviews or post to GitHub. A live gateway/model review is a
separate integration check. To update an installed local app, use Crew's app
update flow so both the backend and materialized agent refresh.

### Existing Crew folders and chats

In **Repositories**, save your repositories, then choose an existing Crew folder
for each repository. After a review completes, **Save to new folder chat** creates
an Outrider-owned chat in that folder and appends the findings without starting a
model turn. Crew resolves the folder's project; Outrider supplies no model,
reasoning, approval, or turn-limit override. The reviewer remains tool-less.

For an existing personal chat, use **Copy findings to existing chat**, then paste
into that chat. Crew 0.9.0 restricts transcript writes to app-owned sessions; direct
insertion into personal sessions is not supported. Existing session settings apply
when you send the pasted findings. An ambiguous folder delivery is never retried
automatically; inspect the folder if delivery is marked uncertain.

Repository input accepts `owner/repo` and `https://github.com/owner/repo` URLs.
The backend probes `~/.local/bin/outrider` before PATH for `crew scan --state-dir`
support. `OUTRIDER_BIN` can select an explicit binary. An older Homebrew install
will no longer shadow the compatible local install.

The scanner subprocess also inherits Crew’s environment with missing user and
Homebrew executable directories appended to PATH, so `gh` is available in
GUI-launched Crew. Existing PATH precedence and GitHub authentication variables
are preserved; no shell startup files are sourced.

### Per-repository behavior

Use **Add repository** for setup, then select a repository from the dropdown in
**Repositories**. The inbox and repository settings are separate views.
Switch between **Form** and **YAML** to edit the same settings; YAML is validated
when switching or saving. Switching to Form reformats YAML and removes comments. Toggle own-PR activity, reaction opt-in,
review replies, and mentions. **Add workflow** provides a name, condition, and
ordered steps. The alternative YAML editor supports the same `triggers`, `ignore_authors`,
`launch_check`, `prompts`, and `workflows` fields as standalone Outrider, including
workflow match rules, reaction selection, thread/comment scope, and freshness.
Save validates against the standalone schema before replacing settings.

```json
{
  "triggers": {"opt_in": {"reaction": "eyes"}, "mentions": {"scope": "comment"}},
  "ignore_authors": ["*[bot]"],
  "prompts": {"extra": "Pay particular attention to backwards compatibility."},
  "workflows": [{
    "name": "dependency-review",
    "match": [{"authors": ["renovate[bot]", "dependabot[bot]"]}],
    "on": "ci_passed",
    "steps": ["Inspect dependency changes in the snapshot.", "Summarize compatibility risks."]
  }]
}
```

Settings are translated to exact-repository overrides; omitted fields use the
standalone defaults. The first matching activity workflow wins. Conditional
workflows use the standalone poller's transition detection. Snapshots retain the
workflow instructions at capture time; changing settings affects future snapshots.
Execution remains a no-tools snapshot review: unavailable steps are reported as
blocked, and `post` does not authorize GitHub writes in Crew. Launcher, agent,
model, permissions, and tool policy are not imported from standalone settings.

### AI setup and MCP

In **Repositories**, choose a repository and expand **Set up with AI**. Describe
what should trigger reviews. Crew runs the no-tools `outrider-setup` agent using
its configured model defaults. The returned YAML must pass Outrider validation.
**Use draft** loads it into the editor; **Save settings** is the only action that
applies it. Generation failures never replace your existing configuration, and
ambiguous dispatches are not automatically repeated. This does not inherit the
model selection of an unrelated open chat.

The app contributes an `outrider` stdio MCP server, registered by Crew as
`outrider:outrider`, with four read-only tools: `list_repositories`,
`configuration_schema`, `list_reviews`, and `propose_repository_config`.
Proposals are validated YAML and do not write configuration or launch reviews.
Agents can use these tools subject to Crew's normal MCP permissions. Neither the
snapshot reviewer nor the setup assistant imports MCP tools. The manifest uses
Crew's Python interpreter resolution and installed app-directory lookup so it
works independently of the GUI PATH.

The app uses the official Outrider logo at `ui/logo.svg` in its header and sidebar.
The original logo and README banner are stored in `docs/brand/` at the repository
root. Keep the packaged `ui/logo.svg` copy identical to `docs/brand/logo.svg`.

### Reactions and CLI migration

The navigation stays visible in every view, with the active view highlighted.
The Form editor shows **Opt-in reaction**, defaulting to 👀 Eyes. Select any of
GitHub's eight reactions; your chosen reaction opts someone else's PR into the
watch. Form/YAML conversion expands the effective trigger defaults, so all four
trigger switches and the selected reaction are explicit in YAML.

For an existing standalone configuration, open **Repositories → Import CLI
configuration** and click **Load default config**, choose a YAML file, or paste
its content, then **Preview import**. Default loading follows the CLI: first
`OUTRIDER_CONFIG`, then legacy `LLM_REVIEW_AGENT_CONFIG`, then
`$XDG_CONFIG_HOME/outrider/config.yaml` (or `~/.config/outrider/config.yaml`).
These variables are read from Crew’s environment. The resolved path is shown;
a missing file leaves the current import text unchanged. Loading is read-only.
The preview resolves repository overrides using Outrider's own config loader and
lists removed keys. Agent, mode, launcher, permissions, sandbox and other execution
settings are stripped before validation, including inside overrides. `mode` is
not mapped to Crew trust. The preview and imported YAML omit these fields; the
source editor is replaced with the cleaned YAML when you use the draft. If own-PR and other-PR settings differ, choose a profile
explicitly; its values apply repository-wide in Crew. **Use import draft** loads
the preview into the YAML editor, and **Save settings** applies it. The original
CLI file is never changed or linked. Session settings remain with Crew; custom
classifier definitions are not imported, and a selected custom launch classifier
is disabled in the draft with a warning. Review profiles, tool-based workflow
execution, and standalone process settings are outside this import's scope.
