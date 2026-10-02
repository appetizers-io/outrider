# Getting started

## Prerequisites

| Tool | Why | Check |
|---|---|---|
| [`gh`](https://cli.github.com) | reads notifications and PRs with your login | `gh auth status` |
| `git` | worktrees for each PR | `git --version` |
| `claude` or `codex` | the agent that runs in a session | `claude --version` / `codex --version` |
| tmux | only for `--launcher tmux` (Linux servers, SSH) | `tmux -V` |
| [`jev-use`](https://www.npmjs.com/package/jev-use) or `npx` | optional: the default launch check | see [Classifiers](classifiers.md) |

Install outrider as described in the [README](../README.md#install).

## First run: a dry run

Run it inside a checkout. outrider then watches that checkout's GitHub repo
(the `origin` remote) and creates worktrees from it:

```sh
cd ~/dev/some-repo
outrider --dry-run --once
```

`--dry-run` logs what would launch and changes nothing: no worktrees, no
sessions, no saved state. `--once` polls once and exits. The log goes to
stderr and looks like this:

```text
level=INFO msg="config: no config file, built-in defaults"
level=INFO msg="GitHub user: octocat (prompts call you Mona)"
level=INFO msg="agent: codex (interactive)"
level=INFO msg="launcher: terminal"
level=INFO msg="terminal: iTerm2 (from $TERM_PROGRAM)"
level=INFO msg="max active agents: 1"
level=INFO msg="launch check: off (jev: no backend key (TYPESAFE_API_KEY, OPENROUTER_API_KEY, AI_GATEWAY_API_KEY, JEV_BACKEND))"
level=INFO msg="tool gate: off (jev: no backend key (TYPESAFE_API_KEY, OPENROUTER_API_KEY, AI_GATEWAY_API_KEY, JEV_BACKEND))"
level=INFO msg="GitHub notifications: READ ONLY"
level=INFO msg="review output: LOCAL SESSION ONLY"
level=INFO msg="repos: octo-org/some-repo"
level=INFO msg="local checkout for octo-org/some-repo: /Users/mona/dev/some-repo (remote origin)"
level=INFO msg="fetched 12 notifications from last 168h"
level=INFO msg="matched octo-org/some-repo#42: Add retries [my PR notification (comment)]"
level=INFO msg="dry-run summary: own_new=1 non_owned=3 watched=0 ignored=8"
```

Each `matched` line is a session that would start. The text in brackets is
the [trigger](triggers.md). [Troubleshooting](troubleshooting.md#reading-the-startup-log)
explains every startup line.

## A real run

```sh
outrider --agent claude
```

It polls every 60 seconds until you stop it with Ctrl-C.

- **The first live run records your existing notifications and launches
  nothing for your own PRs**, so you don't get a burst of sessions for old
  activity. The summary says `(first run: existing own notifications recorded
  only)`. Use `--process-existing` to launch for them anyway.
- A session opens in a new terminal window (or a tmux session) inside a
  worktree under `~/.cache/outrider/worktrees/`. It starts the agent with a
  prompt about the PR and what happened. When the agent exits, the window
  shows the exit status and waits for Enter.
- At most `--max-agents` sessions (default 1) run at once. Events that have
  to wait stay pending and are picked up on a later poll.

## Make it yours

Write the documented default config and edit it:

```sh
outrider config generate --write   # ~/.config/outrider/config.yaml (+ config.schema.json)
outrider config check              # validate it
outrider config show               # the effective config, defaults filled in
```

Flags override the file. See [Configuration](configuration.md).

## Where things live

| Path | What |
|---|---|
| `~/.config/outrider/config.yaml` | your config (or `--config`, or `$OUTRIDER_CONFIG`) |
| `~/.local/state/outrider/state.json` | what was already handled; `--reset-state` forgets it |
| `~/.cache/outrider/` | clones, worktrees, session files, locks and the guard links |

The paths are the same on macOS, Linux and Windows.

## Coming from llm-review-agent

outrider was called llm-review-agent. On the first start it moves the old
`~/.config`, `~/.cache` and `~/.local/state` directories to the new names
(when the new ones don't exist yet) and re-links the PR worktrees.
`$LLM_REVIEW_AGENT_CONFIG` still works, with a warning; use `$OUTRIDER_CONFIG`.
Remove the old `llm-review-agent` binary from your `PATH`.
