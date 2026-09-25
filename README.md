# llm-review-agent

Polls your GitHub notifications and opens a local interactive coding agent
(Codex or Claude Code) for pull requests that need your attention.

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

GitHub stays read-only: notifications are never marked read, and the agent's
`gh` is a guard that refuses writes. Review output stays in the local session.

Sessions on **someone else's PR are review only**: the prompt forbids edits,
commits and pushes (even lint fixes), and a `git` guard refuses `git push`.
In the default **supervised** mode, Claude sessions also get deny rules for
Edit/Write/commit/push and, when its classifier is available, a **tool gate**
(a PreToolUse hook) on every Bash/Edit/Write call, told what the session may
do plus your `tool_gate.rules`. Each session writes a `policy.json` describing
all of this; the prompt points the agent to it. Only your own PRs get fixes
pushed (rebased, `--force-with-lease`). `mode: autonomous` drops the tool
gating and allows pushing to others' PRs unless `others_prs.allow_push: false`.
Codex sessions get the guards, the prompt and the gate's environment; its
hooks need a one-time trust (`/hooks`), so the gate hook isn't wired there.

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

```sh
uv tool install --editable .
```

## Run

From inside a checkout, it watches that repo and creates worktrees from it:

```sh
cd ~/dev/some-repo
llm-review-agent --agent claude --max-agents 3
llm-review-agent --remote upstream   # fork checkout: watch the upstream repo
```

Useful flags: `--once`, `--dry-run`, `--config PATH`, `--repo owner/name`
(glob, repeatable), `--agent`, `--max-agents`, `--no-jev`. Flags override the
config file. See `llm-review-agent --help`.

## Configure

All rules live in an optional YAML file, `--config PATH`,
`$LLM_REVIEW_AGENT_CONFIG`, or `~/.config/llm-review-agent/config.yaml`. Every key
is optional; [`config.example.yaml`](config.example.yaml) shows them all:

- `mode: supervised | autonomous` and `others_prs.allow_push`
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

The pydantic models in `src/llm_review_agent/config.py` define every type,
default and constraint; [`config.schema.json`](config.schema.json) is generated
from them for editor completion and validation:

```sh
llm-review-agent config check [PATH]   # validate
llm-review-agent config show [PATH]    # effective config with defaults
llm-review-agent config schema         # JSON Schema
```

Jev needs `TYPESAFE_API_KEY` (or another jev-use backend key) in the
environment. PR comment text is sent to that provider.

## Develop

```sh
uv sync
uv run ruff check && uv run ruff format --check
uv run mypy
uv run pytest
uv run llm-review-agent config schema > config.schema.json   # after changing config.py
```
