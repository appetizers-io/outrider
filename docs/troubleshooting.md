# Troubleshooting

Run `outrider doctor` first. It checks the config, `gh` and its login, `git`,
the agent and its version, the launcher and terminal, the approval dialogs,
the classifiers, the sandbox and outrider's files, and prints the fix for
everything that is not ok. It changes nothing and exits 1 when a check
failed.

| Check | Fails or warns when |
|---|---|
| `config` | the config file is unreadable or invalid (fail) |
| `github` | `gh` is missing, not logged in, the token lacks the `repo` scope, or `gh api user` fails (fail); a token without OAuth scopes (warn) |
| `git`, `agent` | not on `PATH` or `--version` fails (fail); `other agent` is only info |
| `launcher` | the resolved launcher can't work: no terminal, `tmux` missing, tmux on Windows (fail) |
| `dialogs` | `push` or `github_writes` is `ask` but no dialog can be shown, so `ask` denies (warn) |
| `launch check`, `tool gate` | the classifier can't run: `jev-use`/`npx` or a backend key missing, a command not executable (warn: the role is off) |
| `sandbox` | `sandbox: read-only` but the platform sandbox, `bwrap`/`socat`, the agent version or the Codex file login is missing (fail) |
| `checkout` | `--remote` outside a checkout or not on GitHub (fail); otherwise info |
| `files` | a config, cache or state dir can't be created, or the state file is unreadable (warn) |
| `llm-review-agent` | old directories next to the new ones, the old binary on `PATH`, `$LLM_REVIEW_AGENT_CONFIG` (warn) |

## Reading the startup log

| Line | Means |
|---|---|
| `config: …` | the config file in use, or `no config file, built-in defaults` |
| `GitHub user: <login> (prompts call you <name>)` | the `gh` login; `owner_name` changes the name |
| `agent: …`, `launcher: …`, `terminal: …` | where sessions will run; see [Terminals](terminals.md) |
| `launch check: on/off (…)` | the launch-check classifier, or why it is off |
| `tool gate: on/off (…)` | the tool-gate classifier, or why it is off (`off (autonomous mode)` in autonomous mode) |
| `repos: …` | the repos it watches; missing means every repo |
| `local checkout for <repo>: <path> (remote <name>)` | worktrees branch off this checkout |
| `moved <old> to <new> (renamed to outrider)` | a one-time move of an old `llm-review-agent` directory |

Each poll then logs `fetched N notifications …` and a `summary:` line
(`own_new`, `non_owned`, `watched`, `ignored`). `--log-level debug` and
`--log-format json` help when you file an issue.

## Common errors

| Message | Fix |
|---|---|
| `missing required command: <tool>` | install it or put it on `PATH`: `gh`, `git`, the agent, `tmux` for the tmux launcher, the terminal's command |
| `no terminal found: set terminal in the config, or use --launcher tmux` | Linux without `x-terminal-emulator` or `$TERMINAL`: set `terminal` or use tmux |
| `tmux is not supported on Windows; use --launcher terminal` | use the terminal launcher on Windows |
| `cannot determine GitHub user: …` | run `gh auth status`; without `--once` outrider retries every 30 seconds |
| `--remote needs to run inside a git checkout` | `cd` into the checkout first |
| `remote '<name>' is not a GitHub repo here` | `git remote -v`; the remote must point at github.com |
| `<file> is invalid: - at '/<key>': …` | a wrong value or unknown key; see [Configuration](configuration.md); `outrider config check` |
| `repos.exclude: bad glob "…"` | an unclosed `[` or `{` in a repo glob |
| `$LLM_REVIEW_AGENT_CONFIG is deprecated, use $OUTRIDER_CONFIG` | rename the variable |

## Why didn't it launch?

| Log line | Reason |
|---|---|
| `<repo>#<n>: already running; keeping event pending` | a session for that PR is open; it is retried after it ends |
| `<repo>#<n>: agent limit reached (N); keeping event pending` | `max_agents` sessions run; raise it or close one |
| `<repo>#<n>: nothing actionable [<trigger>]; not launching` | the launch check said no; check its note in the line before |
| `<repo>#<n>: only ignored authors; not launching` | all new activity is from `ignore_authors` |
| `<repo>#<n>: nothing new from others; not launching` | a watched PR changed, but only by you or ignored authors |
| `<repo>#<n>: launch failed; keeping event pending: …` | the worktree or the terminal failed; the error says which |
| `summary: … (first run: existing own notifications recorded only)` | the first live run; use `--process-existing` |

No `matched` line and none of these: the activity didn't match a
[trigger](triggers.md), or the repo is filtered out (`repos`, `--repo`).

## A session window closed right away

The window shows `agent exited: <status>` and waits for Enter. If it closed
without that, the runner couldn't start: run `outrider --dry-run --once`, check
`claude`/`codex` start in that terminal, and look for `launch failed` in the
log. A lock left behind by a session that died is removed on the next launch
(`removing stale lock …`), at the latest after `stale_lock_hours`.

## A push or post was denied

In `ask` mode, Outrider distinguishes an explicit **Deny**, a 5-minute timeout,
and a missing or failed native dialog backend. All refuse the operation. A
backend error includes a bounded, redacted diagnostic. Codex execution approval
is a separate prompt that must succeed before the Outrider guard can run. See
[Safety](safety.md#approval-dialogs).

If a push reports `outrider-push-blocked://`, check `command -v git` in the
agent's shell: it must resolve to the session's private guard directory before
Homebrew or system Git. A nested login shell can reset `PATH` after Outrider
prepends the guard; keep the session's guard directory first when starting
agent shell commands. The push trap refuses an unguarded push.
