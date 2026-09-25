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

Useful flags: `--once`, `--dry-run`, `--config PATH`, `--repo owner/name`
(glob, repeatable), `--agent`, `--max-agents`, `--no-jev`. Flags override the
config file. See `gh-review-agent --help`.

## Configure

All rules live in an optional YAML file, `--config PATH`,
`$GH_REVIEW_AGENT_CONFIG`, or `~/.config/gh-review-agent/config.yaml`. Every key
is optional; [`config.example.yaml`](config.example.yaml) shows them all:

- which triggers run (`own_prs`, `opt_in`, `review_replies`), and whether each
  asks Jev first (`gate: jev | none`)
- the opt-in reaction and where it counts
- `ignore_own_activity` (default on): your own comments never relaunch
- `ignore_authors`: login globs (`*` and `?`) whose activity alone never
  triggers, e.g. `"*[bot]"`
- reply sessions scoped to the replied thread, and how fresh a reply must be
- Jev (`enabled: auto` turns it on when `jev-use`/`npx` and a backend key exist)
- `owner_name` for prompts (default: first name from your GitHub profile) and
  `prompts.extra` appended to every prompt

The pydantic models in `src/gh_review_agent/config.py` define every type,
default and constraint; [`config.schema.json`](config.schema.json) is generated
from them for editor completion and validation:

```sh
gh-review-agent config check [PATH]   # validate
gh-review-agent config show [PATH]    # effective config with defaults
gh-review-agent config schema         # JSON Schema
```

Jev needs `TYPESAFE_API_KEY` (or another jev-use backend key) in the
environment. PR comment text is sent to that provider.

## Develop

```sh
uv sync
uv run ruff check && uv run ruff format --check
uv run mypy
uv run pytest
uv run gh-review-agent config schema > config.schema.json   # after changing config.py
```
