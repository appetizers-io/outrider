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
Review profiles, custom playbooks, test evidence, and multi-step session
workflows are not executed by this first adapter. The regular terminal/tmux
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
