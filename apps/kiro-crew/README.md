# Outrider for Kiro Crew

A Crew app backed by Outrider's Go notification poller. Choose repositories,
poll actionable PR activity, and process it through a PR work queue. Enable
automatic local validation per repository or start work manually. Crew owns
session settings; Outrider does not publish findings to GitHub.

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
5. Configure a Crew folder and validation remote, then enable **Automatic sessions**
   for new activity, or click **Validate locally** on a queued item. Set the global
   session limit under repository settings (default one). **Review without tools**
   remains available for captured context. Crew may ask for approval.

There is no frontend build step: `ui/index.mjs` imports React and the SDK from
Crew's shared modules. The Python backend uses only the standard library.
The app starts idle until you configure repositories. Clear the repository
list or use **Active / Inactive** to pause polling. Disabling the app stops its backend; already-dispatched
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
  Missing tasks (HTTP 404/410) become uncertain and release capacity. Repeated
  status-poll failures become uncertain and retain capacity until reconciled. Authentication failures and
  definitive request rejections are failed reviews, not ambiguous launches.

## Session launch and configuration precedence

Clicking **Review without tools** submits `POST /api/spawn` to the Crew gateway with
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
source editor is replaced with the cleaned YAML when you use the draft. Own-PR and other-PR differences are preserved automatically: the preview contains
base settings for other PRs and an ownership override for your PRs. Repository
matching and override order are resolved by the standalone loader. Scanning
scopes every ownership override to this repository. The Form editor changes
base settings and retains overrides; use YAML to edit ownership-specific values.
**Use import draft** loads
the preview into the YAML editor, and **Save settings** applies it. The original
CLI file is never changed or linked. Session settings remain with Crew; custom
classifier definitions are not imported, and a selected custom launch classifier
is disabled in the draft with a warning. Review profiles, tool-based workflow
execution, and standalone process settings are outside this import's scope.


### Validate upstream PRs in a local fork

In **Repositories**, select the upstream GitHub repository and the existing Crew
folder for your local fork. Click **Detect checkout and remotes**, select the
matching remote (usually `upstream`), and **Save settings**. The checkout comes
from the folder's `project_dir`, including inherited parent folders; set that
in Crew first if it is missing. Only remotes pointing to the watched GitHub
repository are offered. Both SSH and HTTPS GitHub remotes are supported.

The inbox now offers **Validate locally** alongside **Review without tools**. Local
validation fetches the upstream PR head and checks it still matches the captured
commit. It creates a detached worktree at
`<primary-checkout>/.claude/worktrees/outrider-<snapshot-id-prefix>` without
switching branches or touching uncommitted work in the original checkout.
A regular app-owned Crew chat is created in the selected folder and bound to the
worktree. No agent, model, backend, trust, or approval settings are supplied:
Crew's default chat agent and session configuration apply. This does not inherit
per-chat overrides from some other existing chat.

The chat receives the PR identity, pinned commit, and the selected workflow's
steps from the current saved repository settings. It can inspect code and run
validation tools subject to Crew approval. Validation instructions prohibit
committing, pushing or posting to GitHub; this is a task scope, not a replacement
for Crew's permission enforcement. Snapshot reviews retain their no-tools policy.

Progress, approvals, and final validation results live in the Crew chat. The
inbox records `started` as a successful handoff, not as completed validation.
Worktrees are retained for inspection; remove one with `git worktree remove`
when finished. A timeout or interrupted handoff is marked uncertain and never
automatically resent. Preflight failures before a worktree exists can be retried.
`/api/chat` permission is required for dispatch to the app's own chats; Crew
continues to enforce chat ownership. Local validation can be started manually or automatically for new activity in
repositories with automatic sessions enabled.

### Import previews, feedback, and polling status

**Preview import** opens a modal dialog with scrollable YAML, collapsible import
notes, Cancel, and Use import draft. Escape closes it without applying the draft.
Save settings and Add repository show a persistent, dismissible success toast.

**Active / Inactive** controls always-on polling and new automatic sessions.
The setting survives restarts; an active app resumes when Crew starts. Running
work finishes when polling is paused. The dedicated **Activity log** view beside Active / Inactive shows scan
progress, saved settings, dispatches, completion, and errors (last 200 events).
Crew notifications announce local session starts and dispatch failures. The Queue
navigation badge counts pending PRs; it is not an arbitrary Crew sidebar badge.

### Queue processing and deduplication

The default queue shows the newest pending item per PR plus running or uncertain
work. Completed, dismissed, and older activity is available through the history
checkbox. **Dismiss** removes an item from automatic processing; it does not stop
watching the PR, so later activity can enqueue new work.

Automatic sessions are opt-in per repository and require a Crew folder and
validation remote. Enabling them applies to activity captured after enabling;
existing backlog remains available for manual processing. A global session limit
(default one, maximum 20) includes both local validation and no-tools reviews.
Only one session per PR can run at a time, even with spare global capacity.

The poller persists handled notification state and watched-PR fingerprints.
Identical captured prompts share a deterministic SHA-256 queue ID. The ID includes
trigger, scope, workflow, head, and captured context: different triggers or scopes
can still produce distinct items for the same commit. Automatic dispatch selects
the latest pending capture per PR; older captures remain in history. Activity
arriving during a session waits for that session to finish before a follow-up.

Dispatch intent and Crew identifiers are persisted. Restarting does not resend
accepted work; ambiguous handoffs remain uncertain and reserve capacity rather
than risking a duplicate session. This is not an exactly-once delivery guarantee:
check Crew if a handoff is uncertain. Chat completion is tracked using Crew's
running/queue state and observed response; missing or unrecognized status retains
capacity.

Each scanner pass has a 150-second cooperative budget, below the backend's
180-second emergency timeout. It saves completed poll progress before returning
`{"complete": false}` when that budget expires. The UI labels this as partial
progress, not a successful full scan; subsequent passes continue pending work.
This avoids discarding completed notification bookkeeping when a large initial
scan exceeds the backend deadline. `crew scan --timeout` can change the scanner
budget for manual invocations. Last scan outcome survives backend restarts.

### Session defaults and repository overrides

In **Settings**, configure global automatic sessions, the reviewer Crew member,
and session trust. Each repository can inherit those settings or override them.
Existing explicit per-repository on/off values remain overrides after upgrading.
The default reviewer is Crew's default; an explicit selection sends the member's
name and namespace when creating the chat. Trust defaults to **Inherit Crew**;
Normal, Reads, and Trust apply only to newly created Outrider chats. Crew requires
its session-approval disclosure to be accepted when enabling this app. A refused
trust change stops dispatch and leaves the created chat available for inspection.
YOLO is not offered because it changes Crew globally. Imported CLI agent/mode
values still do not set these controls.

Sessions are titled with repository, PR number, and PR title, pinned against Crew's
automatic naming. **Start session** is the main queue action; text-only reviews
are under Advanced. Their previous errors are separate from local-session status.

**Settings → Polling and session limits** includes Lookback hours (default 168), passed to
the isolated scanner config. This controls the notification window and candidate
retention; it does not erase existing work. The Queue repository filter scopes
its pending count and **Clear pending queue**. Clearing requires confirmation,
dismisses pending/failed items, retains active/uncertain sessions and history,
and permits future activity to enqueue new work. The Crew sidebar badge counts
unread notifications, independently of pending PRs.

Activity log is a dedicated navigation view. GitHub source and support links are
available in the footer of every view.

Queue entries include **Open Crew session** once a local validation chat exists.
The link targets that exact chat and remains available in history after completion.

Closed/deleted unfinished chats become **stopped**, release capacity, and offer
**Retry session** or **Dismiss**. Retry is explicit, uses a new chat/worktree, and
retains the previous attempt. Completed work remains in history. Connection and
permission failures keep capacity reserved until the session can be reconciled.

### UI code organization

The UI remains build-free ES modules served by Crew, using its React and app SDK.
`ui/index.mjs` composes the layout and selects a view. `ui/views/` contains Queue,
Repositories, Settings, Add repository, and Activity log. `ui/components/` owns
shared layout, session controls, queue cards, configuration panels and the import
dialog. `ui/hooks/` separates gateway polling/actions, repository drafts, and
AI/import drafts from view rendering. The controller coordinates saved settings
and preserves drafts across navigation; view-specific props are passed explicitly.
`ui/queue.mjs` contains pure queue selection and deduplication display logic.

Run `task crew:test` for backend lifecycle regressions and the Node UI tests.
The UI harness loads the entire ES module graph with host React/SDK substitutes,
renders every view, and checks key actions and save boundaries. It requires no
npm install or bundle step; it does not replace visual checks in Crew.
