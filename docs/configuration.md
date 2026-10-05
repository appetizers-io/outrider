# Configuration

Every setting is optional. outrider works without a config file; flags
override the file.

## The file

outrider reads the first of:

1. `--config PATH`
2. `$OUTRIDER_CONFIG`
3. `~/.config/outrider/config.yaml` (or `$XDG_CONFIG_HOME/outrider/config.yaml`), when it exists

```sh
outrider config generate            # print every key at its default, documented
outrider config generate --write    # write it to ~/.config/outrider/config.yaml
outrider config generate -o x.yaml  # or anywhere else (--force to overwrite)
outrider config check [PATH]        # validate
outrider config show [PATH]         # the effective config, defaults filled in
outrider config show --repo my-org/api --prs others  # the config of a PR there
outrider config schema              # the JSON Schema
```

Two files are the reference; this page explains them by topic and doesn't
repeat every description:

- [`config.example.yaml`](../internal/config/config.example.yaml): every key at
  its default, with comments. `config generate` prints it.
- [`config.schema.json`](../config.schema.json): types, allowed values and
  limits, generated from the code. `config generate` writes it next to the
  config, and the first line of the file points editors with YAML language
  support (e.g. VS Code with the Red Hat YAML extension) to it for completion
  and checks.

Unknown keys and wrong values are errors that name the key:

```text
config.yaml is invalid:
- at '/max_agents': minimum: got 0, want 1
```

## Example configs

Ready-made configs for common setups, in
[`examples/configs`](examples/configs/README.md). Each sets only the keys that
matter for its case.

| File | For |
|---|---|
| [`minimal.yaml`](examples/configs/minimal.yaml) | the smallest useful config |
| [`reviewer.yaml`](examples/configs/reviewer.yaml) | reviewing others' PRs: 👀 and @mentions, review only, posts ask |
| [`own-prs.yaml`](examples/configs/own-prs.yaml) | fixing CI and review feedback on your own PRs |
| [`autonomous.yaml`](examples/configs/autonomous.yaml) | a trusted repo: push and post without asking |
| [`read-only.yaml`](examples/configs/read-only.yaml) | no pushes, no GitHub writes |
| [`codex.yaml`](examples/configs/codex.yaml) | Codex instead of Claude Code |
| [`custom-classifier.yaml`](examples/configs/custom-classifier.yaml) | your own launch check and tool gate |
| [`many-repos.yaml`](examples/configs/many-repos.yaml) | many repos, include/exclude globs, ignored bots |
| [`headless.yaml`](examples/configs/headless.yaml) | a background service: tmux, no dialogs |
| [`overrides.yaml`](examples/configs/overrides.yaml) | different settings per repo, and for your own vs. others' PRs |

## Safety: mode, pushes and posts

| Key | Default | Values |
|---|---|---|
| `mode` | `supervised` | `supervised`, `autonomous` |
| `push` | `ask` in supervised, `allow` in autonomous | `ask`, `never`, `allow` |
| `github_writes` | `ask` in supervised, `allow` in autonomous | `ask`, `never`, `allow` |
| `others_prs.allow_push` | `false` in supervised, `true` in autonomous | `true`, `false` |
| `sandbox` | `off` | `off`, `read-only` |
| `others_prs.sandbox` | `null`: as `sandbox` | `off`, `read-only` |
| `others_prs.review_forks` | `null`: auto, your `origin` fork in a fork checkout | your own forks by exact name, e.g. `["me/repo"]` (globs work, but `doctor` warns); `[]`: off |

`sandbox: read-only` makes `push` and `github_writes` default to `never`;
setting either to anything else with it is an error at startup, and so is
`others_prs.allow_push: true` or `others_prs.review_forks` with a read-only
sandbox for others' PRs.

`others_prs.review_forks` lets review-only sessions commit locally and push
evidence (failing tests, repro scripts, a CI workflow) to your own forks; each
push follows `push`. Unset, it is automatic: in a fork checkout watched with
`--remote upstream`, `origin` is the review fork when it is yours and a fork
of the watched repo. A list is used as is; `[]` turns it off. The startup log
and `outrider doctor` show the result, e.g.
`review forks: me/repo (auto: origin)`. See
[Review forks](safety.md#review-forks).

What each value does is on the [Safety](safety.md) page.

## Agent and sessions

| Key | Default | Meaning |
|---|---|---|
| `agent` | `codex` | `claude` or `codex` |
| `launcher` | `auto` | `auto`, `terminal`, `tmux`; see [Terminals](terminals.md) |
| `terminal` | `auto` | a terminal name, or a command list with `{cmd}` |
| `max_agents` | `1` | sessions running at once |
| `stale_lock_hours` | `12` | a session lock older than this is treated as dead |
| `owner_name` | first name from your GitHub profile | how prompts call you |
| `prompts.extra` | `""` | appended to every prompt, e.g. house rules |

## Polling

| Key | Default | Meaning |
|---|---|---|
| `interval_seconds` | `60` | pause between polls (at least 10); doubles on failures, up to 15 minutes |
| `lookback_hours` | `168` | notification window, and how long an unseen candidate PR is kept |
| `candidate_limit` | `50` | non-owned PRs checked for the opt-in reaction per poll |

## Repos and authors

| Key | Default | Meaning |
|---|---|---|
| `repos.include` | `[]` | `owner/repo` globs or GitHub URLs to watch. Empty: the checkout you run in, else every repo |
| `repos.exclude` | `[]` | globs that never trigger |
| `ignore_authors` | `[]` | login globs whose activity alone never triggers |

Repo globs support `*` (also across `/`), `?`, `[abc]`, `[!abc]` and `{a,b}`:

```yaml
repos:
  include: ["my-org/*", "https://github.com/other/tool"]
  exclude: ["my-org/{website,sandbox}"]
ignore_authors: ["*[bot]", "renovate"]
```

Login globs know only `*` and `?`; brackets are literal, so `"*[bot]"` matches
`dependabot[bot]`. A broken glob (an unclosed `[` or `{`) is an error at
startup instead of silently matching nothing.

## Overrides

`overrides` changes per-PR keys for some PRs only. Each entry has a `match`,
a list of identities; an entry applies when any identity matches the PR. In
an identity every attribute it sets must match, and an unset one matches
anything:

| Attribute | Matches |
|---|---|
| `repo` | an `owner/repo` glob |
| `url` | a GitHub repo URL |
| `prs` | `own` (you authored the PR) or `others` |

Every matching entry applies, in file order; a later one wins. An entry sets
keys like the top level: unset keys stay, lists replace, nested keys merge.

```yaml
ignore_authors: []
overrides:
  - match: [{prs: others}]              # bots never relaunch others' PRs
    ignore_authors: ["*[bot]"]
  - match:
      - {repo: "my-org/*", prs: others}
      - {url: "https://github.com/upstream-org/repo"}
    launch_check: {skip_below: 0.7}
```

Only per-PR keys can be overridden: `push`, `github_writes`, `sandbox`,
`agent`, `ignore_authors`, `triggers`, `others_prs`, `launch_check`,
`tool_gate`, `prompts` and `workflows`. Others are errors. Conflicts (e.g. a read-only
sandbox and `push: allow`) are checked at startup for each entry, and for all
entries on your own PRs and on others' together. `config show --repo`,
`doctor` and the session's `policy.json` name the entries that apply.

## Workflows

`workflows` selects named, ordered agent instructions using repo/ownership,
author, label, title, branch and event matches. It also supports condition
triggers when review threads are resolved or CI passes. See
[Workflows](workflows.md) for dependency review and technical review examples.

## Triggers, launch check and tool gate

- `triggers.*`: which activity starts a session. See [Triggers](triggers.md).
- `classifiers`, `launch_check`, `tool_gate`: the decision backends. See
  [Classifiers](classifiers.md).

## Flags

Flags override the file, overrides included. They are checked against the
same schema.

| Flag | Config key |
|---|---|
| `--agent` | `agent` |
| `--launcher` | `launcher` |
| `--terminal` | `terminal` |
| `--github-writes` | `github_writes` |
| `--sandbox` | `sandbox` |
| `--interval` | `interval_seconds` |
| `--lookback-hours` | `lookback_hours` |
| `--max-agents` | `max_agents` |
| `--candidate-limit` | `candidate_limit` |
| `--stale-lock-hours` | `stale_lock_hours` |
| `--repo` (repeatable) | `repos.include` |
| `--exclude-repo` (repeatable) | `repos.exclude` |
| `--jev-cmd` | `classifiers.jev.command` |
| `--no-jev` | `classifiers.jev.enabled: false` |

Flags without a config key:

| Flag | Meaning |
|---|---|
| `--config PATH` | the config file |
| `--remote NAME` | remote of the local checkout to watch (default `origin`; e.g. `upstream` in a fork) |
| `--once` | poll once and exit |
| `--dry-run` | log what would launch; change nothing |
| `--process-existing` | on the first run, launch for existing notifications too |
| `--reset-state` | forget what was handled before |
| `--log-level` | `debug`, `info`, `warn`, `error` (default `info`) |
| `--log-format` | `text` or `json` (default `text`) |
