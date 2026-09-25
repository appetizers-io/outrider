# gh-review-agent

Polls your GitHub notifications and opens a local interactive coding agent
(Codex or Claude Code) for pull requests that need your attention.

Triggers:

- activity on **your own PRs**
- a 👀 reaction from you **anywhere on someone else's PR** (description,
  comment, review, inline comment) opts it in; later review activity
  relaunches the agent, removing the 👀 stops the watch
- **replies to your review comments**

Before launching, [Jev](https://www.npmjs.com/package/jev-use) judges whether
the new activity is actionable at all, so bot summaries and LGTMs don't cost
an agent session. Unsure or unreachable means launch anyway.

GitHub stays read-only: notifications are never marked read, and the agent's
`gh` is a guard that refuses writes. Review output stays in the local session;
the agent may only `git push` fixes to the PR branch.

## Install

```sh
uv tool install --editable .
```

## Run

From inside a checkout, it watches that repo and creates worktrees from it:

```sh
cd ~/dev/some-repo
gh-review-agent --agent claude --max-agents 3
gh-review-agent --remote upstream   # fork checkout: watch the upstream repo
```

Useful flags: `--once`, `--dry-run`, `--repo owner/name` (glob, repeatable),
`--exclude-repo`, `--launcher tmux|terminal`, `--no-jev`,
`--lookback-hours`. See `gh-review-agent --help`.

Jev needs `TYPESAFE_API_KEY` (or another jev-use backend key) in the
environment. PR comment text is sent to that provider.

## Develop

```sh
uv sync
uv run ruff check && uv run ruff format --check
uv run mypy
uv run pytest
```
