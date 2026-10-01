# llm-review-agent

Polls your GitHub notifications and opens a local interactive coding agent
(Codex or Claude Code) for pull requests that need your attention. A single
binary for macOS, Linux and Windows.

Triggers:

- activity on **your own PRs**
- an **@mention** of you on someone else's PR (session limited to that comment)
- a 👀 reaction from you **anywhere on someone else's PR** (description,
  comment, review, inline comment) opts it in; later review activity
  relaunches the agent, removing the 👀 stops the watch
- **replies to your review comments**

Before launching, a **launch check** classifier (by default
[Jev](https://www.npmjs.com/package/jev-use)) judges whether the new activity
is actionable at all, so bot summaries and LGTMs don't cost an agent session.
Unsure or unreachable means launch anyway.

Notifications are never marked read, and review output stays in the local
session unless you ask the agent to post it. The agent's `gh` is a guard:
reads pass, and comments, inline review comments and replies, reviews and
reactions **on the session's PR** follow `github_writes`. With `ask` (the
default in supervised mode) each one opens a native dialog showing the command
and the text, and runs only if you click **Post**. `never` keeps GitHub
read-only, `allow` posts without asking (the default in autonomous mode).
The guard refuses merging, closing, editing the PR, graphql mutations and
writes to other PRs, and it runs writes against the session's repo.

Sessions on **someone else's PR are review only**: the prompt forbids edits,
commits and pushes (even lint fixes), and a `git` guard refuses `git push`.
In the default **supervised** mode, Claude sessions also get deny rules for
Edit/Write/commit/push and, when its classifier is available, a **tool gate**
(a PreToolUse hook) on every Bash/Edit/Write call, told what the session may
do plus your `tool_gate.rules`. Each session writes a `policy.json` describing
all of this; the prompt points the agent to it. Only your own PRs get fixes
pushed (rebased, `--force-with-lease`). `mode: autonomous` drops the tool
gating and allows pushing to others' PRs unless `others_prs.allow_push: false`.

**Pushes need your approval.** With `push: ask` (the default in supervised
mode) every `git push` in a session opens a native dialog, and the push runs
only if you click **Push** (it denies after 5 minutes or where there is no
dialog). `push: never` keeps commits local, and `push: allow` pushes without
asking (the default in autonomous mode). Apart from the `git` guard, sessions
get `pushInsteadOf` rewrites in their environment, so a plain push through the
real git binary goes to a dead URL. Claude sessions get deny rules for the
dialog tool (`osascript`, `zenity`/`kdialog`, `powershell`); they only stop
the obvious calls, not a determined agent.
Codex sessions get the guards, the prompt and the gate's environment; its
hooks need a one-time trust (`/hooks`), so the gate hook isn't wired there.

The guards are the `llm-review-agent` binary itself: the `bin/` directory
under `~/.cache/llm-review-agent` holds `gh` and `git` links to it, put first
on each session's `PATH`.

**What the guards are, and what they are not.** The `gh` and `git` guards,
the `pushInsteadOf` trap, the deny rules and the approval dialogs are
guardrails against an agent's mistakes. They are not a sandbox. The agent runs
as you, with your credentials and your environment: it can change the
`LLM_REVIEW_AGENT_*` and `GIT_CONFIG_*` variables of its own commands, call
the real binaries by path, set a `pushurl` (which `pushInsteadOf` ignores), or
use the GitHub API directly. A PR from someone you don't trust can carry a
prompt injection in its diff, comments or checked-in agent settings. For
those, keep the tool gate on (supervised mode) or run the agent in a sandbox
(the Claude Code sandbox, a container or a VM) without write access to
`~/.cache/llm-review-agent` and with your push credentials out of reach.

### Classifiers

`classifiers` defines named decision backends used by `launch_check` and
`tool_gate`. A role whose classifier is unavailable (disabled, binary missing,
Jev without a backend key) is off; the startup log says why.

- `kind: jev` (built in as `jev`): `jev-use judge` for launch checks,
  `jev-use hook gate` as the tool gate.
- `kind: command`: any local classifier.
  - `launch_command` reads a JSON request on stdin (`repo`, `pr`, `author`,
    `own`, `trigger`, `activity`, `failing_checks`, `question`, `state_text`)
    and prints `{"launch": bool}` or `{"probability": 0..1}`, optionally with
    `"reason"`. Anything else launches.
  - `hook_command` is a Claude Code / Codex PreToolUse hook (exit 2 or a
    `permissionDecision` denies). It gets the session rules in
    `$LLM_REVIEW_AGENT_GATE_TEXT`, the policy in `$LLM_REVIEW_AGENT_POLICY_FILE`
    and `tool_gate.threshold` in `$LLM_REVIEW_AGENT_GATE_THRESHOLD`.

## Install

Download the archive for your platform from the
[releases](https://github.com/appetizers-io/llm-review-agent/releases) and put
`llm-review-agent` on your `PATH`, or build it with Go 1.27:

```sh
go install github.com/appetizers-io/llm-review-agent/cmd/llm-review-agent@latest
```

From a checkout, `task install` builds it into `~/.local/bin`
(`task install INSTALL_DIR=/other/dir` to change that).

It needs [`gh`](https://cli.github.com) (logged in), `git`, and the agent CLI
(`codex` or `claude`); tmux for the tmux launcher. Nothing else: no Python, no
shell scripts.

## Run

From inside a checkout, it watches that repo and creates worktrees from it:

```sh
cd ~/dev/some-repo
llm-review-agent --agent claude --max-agents 3
llm-review-agent --remote upstream   # fork checkout: watch the upstream repo
```

Useful flags: `--once`, `--dry-run`, `--config PATH`, `--repo owner/name`
(glob, repeatable), `--exclude-repo`, `--agent`, `--max-agents`, `--no-jev`,
`--github-writes ask|never|allow`, `--launcher auto|terminal|tmux`,
`--terminal NAME`, `--log-level debug|info|warn|error`,
`--log-format text|json`. Flags override the config file. See
`llm-review-agent --help`.

Logs go to stderr through `log/slog`: text by default, JSON lines with
`--log-format json`.

## Platforms

Sessions open in a new terminal window or in tmux (`launcher`):

- `auto` (default): on macOS the terminal in a desktop (Aqua) session, else
  tmux. On Linux the terminal when a display is present and a terminal
  resolves, else tmux. On Windows always the terminal.
- `terminal` uses the `terminal` key (`--terminal`). `auto` detects it once at
  startup and logs it, e.g. `terminal: iTerm2 (from $TERM_PROGRAM)`. The first
  match wins: `$TERM_PROGRAM` (tmux ignored); `$LC_TERMINAL`,
  `$__CFBundleIdentifier`, `$GHOSTTY_RESOURCES_DIR`, `$WEZTERM_EXECUTABLE`,
  `$KITTY_WINDOW_ID`, `$WT_SESSION` (these survive inside tmux); `$TERMINAL`;
  then Terminal.app on macOS, `x-terminal-emulator` on Linux, Windows Terminal
  (`wt.exe`) or else `cmd` on Windows. A name forces an app: `terminal-app`,
  `iterm`, `ghostty`, `wezterm`, `kitty`, `windows-terminal`,
  `x-terminal-emulator`, `cmd`. Anything else is a command list with a `{cmd}`
  placeholder, e.g. `terminal: [alacritty, -e, "{cmd}"]`.
- `tmux`: a detached session per PR; the log says how to attach.

Terminal.app and iTerm2 open without stealing focus (`open -g`).

Approval dialogs (`push: ask`, `github_writes: ask`): `osascript` on macOS;
`zenity`, else `kdialog`, on a Linux desktop (`$DISPLAY` or
`$WAYLAND_DISPLAY`); a PowerShell message box on Windows. The dialog tool is
only taken from fixed system locations, never from `PATH`. Without a dialog the answer is
"deny".

State and caches live in the same places on every OS:
`~/.config/llm-review-agent/config.yaml`,
`~/.local/state/llm-review-agent/state.json` and `~/.cache/llm-review-agent`
(worktrees, sessions, locks).

Known limitations:

- **Windows**: tmux is not available. The gate hook command is quoted for a
  POSIX shell (Claude Code on Windows runs hooks through Git Bash). The guards
  are copies of the binary (`gh.exe`, `git.exe`) instead of links. The
  PowerShell message box has Yes/No buttons, so its text says which one posts
  or pushes. Launching in Windows Terminal is not verified on a real machine
  yet.
- Ghostty, WezTerm and kitty on macOS are opened with `open -n -a … --args`;
  only Terminal.app and iTerm2 are verified.

## Configure

All rules live in an optional YAML file, `--config PATH`,
`$LLM_REVIEW_AGENT_CONFIG`, or `~/.config/llm-review-agent/config.yaml`. Every
key is optional; [`config.example.yaml`](config.example.yaml) shows them all:

- `mode: supervised | autonomous`, `push: ask | never | allow`,
  `github_writes: ask | never | allow` and `others_prs.allow_push`
- `launcher` and `terminal` (see Platforms)
- which triggers run (`own_prs`, `opt_in`, `review_replies`, `mentions`), and
  whether each runs the launch check first (`check`)
- `classifiers`, `launch_check` and `tool_gate` (see above)
- the opt-in reaction and where it counts
- `ignore_own_activity` (default on): your own comments never relaunch
- `ignore_authors`: login globs (`*` and `?`) whose activity alone never
  triggers, e.g. `"*[bot]"`
- reply sessions scoped to the replied thread, and how fresh a reply must be
- Jev (`classifiers.jev.enabled: auto` turns it on when `jev-use`/`npx` and a
  backend key exist)
- `owner_name` for prompts (default: first name from your GitHub profile) and
  `prompts.extra` appended to every prompt

The structs in [`internal/config`](internal/config/config.go) define every
type, default and constraint; [`config.schema.json`](config.schema.json) is
generated from them for editor completion and validation:

```sh
llm-review-agent config check [PATH]   # validate
llm-review-agent config show [PATH]    # effective config with defaults
llm-review-agent config schema         # JSON Schema
llm-review-agent config generate       # every key, documented (-o PATH | --write)
```

Jev needs `TYPESAFE_API_KEY` (or another jev-use backend key) in the
environment. PR comment text is sent to that provider.

## Develop

Needs Go 1.27, [Task](https://taskfile.dev) and
[golangci-lint](https://golangci-lint.run) v2.

```sh
task lint     # golangci-lint + go vet
task test     # go test -race ./...
task build    # bin/llm-review-agent
task all      # all three
task schema   # regenerate config.schema.json after changing internal/config
```

Layout: `cmd/llm-review-agent` (cobra commands, multi-call guard dispatch),
`internal/config`, `internal/github` (a thin `gh` wrapper),
`internal/poll` (triggers and launch decisions), `internal/session`
(worktrees, prompt, policy, launchers, the session runner),
`internal/guard` (the `gh` and `git` guards), `internal/approve` (native
dialogs), `internal/classifier`, and `prompts/` (the prompt template).
`testdata/python-parity.json` pins prompts, gate rules, launch-check requests
and activity fingerprints to what the former Python version produced.

Releases are built by GoReleaser when a `v*` tag is pushed.

## License

[Apache-2.0](LICENSE)
