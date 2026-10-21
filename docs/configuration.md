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

## Session network access

To reduce repeated GitHub network approval prompts for either agent, set:

```yaml
network_access: true
```

This applies only to sessions launched by Outrider, without editing the owner’s
global settings. It can also be scoped with `overrides`. Unset or `null` inherits
the owner’s settings; `false` blocks outbound access inside the applicable
network sandbox. `true` conflicts with `sandbox: read-only` or
`others_prs.sandbox: read-only`; use a scoped override for non-read-only sessions.

Codex receives `-c sandbox_workspace_write.network_access=true` (or `false`),
which applies in workspace-write mode. Claude receives session `--settings` with
`sandbox.network.allowedDomains: ["*"]` for `true`, or
`sandbox.network.deniedDomains: ["*"]` and `strictAllowlist: true` for `false`.
Claude’s existing denied domains and managed restrictions still apply.
See the [Codex configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference)
and [Claude sandbox settings](https://code.claude.com/docs/en/settings-reference#sandbox-network-alloweddomains).

The setting does not enable an agent sandbox: Claude’s Bash sandbox must already
be enabled for domain rules to take effect. It permits all outbound hosts when
true, not just GitHub. It does not change tool approval policies, Claude’s
WebFetch/MCP permissions, or Outrider’s push/post guards. With no active network
sandbox, `false` does not impose network isolation; use `sandbox: read-only`
for Outrider’s enforced no-network mode.

## Agent and sessions

| Key | Default | Meaning |
|---|---|---|
| `agent` | `codex` | `claude` or `codex` |
| `network_access` | `null`: inherit owner settings | `true`: allow sandbox network hosts; `false`: block sandboxed outbound access |
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

Only per-PR keys can be overridden: `push`, `github_writes`, `sandbox`, `isolation`, `network_access`,
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

## Docker Sandboxes isolation

`isolation.enabled: true` selects the existing `sbx` microVM runtime. It requires
`mode: autonomous` and push/post modes of `never` or `allow`; host approval dialogs
cannot run inside the VM. The private clone and agent home are writable, while
host inputs are read-only. See [setup, credential reuse and results](isolation.md).

| Key | Default | Purpose |
| --- | --- | --- |
| `isolation.enabled` | `false` | Opt into Docker Sandboxes; missing support refuses launch |
| `isolation.guard_binary` | `null` | Linux Outrider binary; defaults to `outrider-linux-runtime` next to the host executable |
| `isolation.read_only` | `[]` | Extra absolute host paths mounted read-only |

## Review profiles

`review` is opt-in and applies only to other people's PRs. Without it, prompts
are byte-for-byte unchanged. `review.default: null` disables the default while
leaving explicit rules available. Built-ins are `quick` (static summary and top
risks, no tests), `standard` (correctness, regressions and coverage), and `deep`
(scope against linked issues, sibling style, gaps, base docs and evidence).

```yaml
review:
  default: standard
  trusted_authors: [alice]        # exact, case-insensitive logins
  trusted_repos: [my-org/trusted] # explicit repository globs
  profiles:
    deep:
      playbook: profiles/deep.md  # optional, inside the owner config directory
      model: {claude: opus, codex: gpt-5.5}
      effort: high               # low | medium | high
      max_minutes: 120            # 1–1440; absent: no deadline
      evidence: {tests: local, comments: draft}
      context: {issues: true, docs: ["docs/**", "AGENTS.md"]}
  rules:
    - repos: [my-org/*]
      triggers: [opt_in]
      profile: deep
    - triggers: [mentions, review_replies]
      profile: quick
```

Rules are first-match-wins. Repos and triggers within a rule are ANDed; absent
lists match anything. Triggers are `opt_in`, `on_change`, `mentions` and
`review_replies`. A default `deep` review becomes `standard` on a discussion
delta (`on_change`), unless an explicit rule selects another profile. A scoped
mention or reply remains scoped; the profile does not widen it. Startup logs,
`policy.json`, `config show` and `doctor` show profile selection or configuration.

Built-in bodies are embedded and golden-tested. A custom profile name requires
a playbook. Playbooks replace the body, are limited to 64 KiB, must be regular
files inside the configuration directory, and cannot come from the PR checkout
or escape via symlinks. The SHA-256 is recorded in policy. Permission sections
follow the body, and `tool_gate.include_profile` must stay `false`: playbooks
never become gate rules. Models are passed with Claude `--model` or Codex `-m`;
effort uses Claude `--effort` or Codex `model_reasoning_effort`. No dollar budget
is claimed for interactive agents. At `max_minutes`, the runner warns, sends
Interrupt, then TERM and finally Kill after grace periods, and records
`ended: timeout`. A timed-out microVM is stopped and retained for inspection.

Evidence tests are `off`, `local` or `e2e`. Automatic execution requires an
explicit trusted author OR repository; org membership is not inferred. Quick
reviews never run tests. `e2e` also requires an explicit profile rule and an
effective `sandbox: off`; conflicting configs fail to load. Local tests in a
read-only sandbox need warm caches and must not write. Existing PR ownership,
edit, commit, push, post and sandbox rules always apply. Without a review fork,
evidence stays local and the PR is never pushed by a review-only session.

Comment output is `off` or `draft`. The `outrider-draft` helper takes Markdown
on stdin and writes only the session's `outbox/comments.md` (at most 1 MiB).
It grants no general Edit/Write permission; a read-only sandbox keeps them in the transcript. Drafting
never posts to GitHub. GitHub pending reviews, per-PR reaction escalation,
external spec repositories and automatic depth selection are deferred.

`context.issues` prefetches closing references, `#N`, `owner/repo#N` and GitHub
issue URLs in the PR body, plus one parent level. `context.docs` matches paths
at the PR's immutable base commit, never HEAD. A manifest records source URLs,
base SHA, availability and truncation; files are untrusted data, never prompt
instructions. Limits are 20 issues, 20 docs, 2 MiB per file and 8 MiB total.
Missing linked issues must be reported explicitly. Deep reviews with
`max_agents: 1` get a doctor warning because they occupy the only slot.
