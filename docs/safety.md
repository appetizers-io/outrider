# Safety

outrider starts an AI agent on code other people wrote, with your GitHub login.
This page describes the guardrails around that, and their limits.

> **The guardrails catch an agent's mistakes. They are not a sandbox.** The
> agent runs as you, with your files, credentials and network. A determined or
> prompt-injected agent can get around every guard listed here. For PRs from
> people you don't trust, use the [read-only sandbox](#read-only-sandbox), or
> run the agent in a container or a VM.

## Modes

| | `mode: supervised` (default) | `mode: autonomous` |
|---|---|---|
| Someone else's PR | review only | may push fixes (the prompt asks for fast-forward only), unless `others_prs.allow_push: false` |
| `push` default | `ask` | `allow` |
| `github_writes` default | `ask` | `allow` |
| Claude deny rules | yes | no |
| Tool gate (PreToolUse hook) | yes, when its classifier is available | no |

## Read-only sandbox

`sandbox: read-only` (flag `--sandbox read-only`) runs every session in the
agent's own OS sandbox: Seatbelt on macOS, bubblewrap on Linux. The agent can
read and run code, but can't write files, commit, push, post to GitHub or
reach the network. `others_prs.sandbox: read-only` does this only for PRs
someone else authored. The default is `off`.

Before the agent starts, outrider fetches the PR context into
`<session>/pr-context/`: `pr.json` (metadata, reviews, conversation comments,
checks), `pr.diff`, `review-comments.json` (inline threads) and
`failing-checks.json`. The prompt points the agent there, since `gh` can't
reach GitHub from inside the sandbox.

`read-only` means `push: never` and `github_writes: never`. A config that sets
either to something else, or `others_prs.allow_push: true` together with a
read-only sandbox for others' PRs, is refused at startup.

| | Claude Code | Codex |
|---|---|---|
| Shell commands | `sandbox.enabled`, `failIfUnavailable`, `allowUnsandboxedCommands: false` (no unsandboxed retry) | `--sandbox read-only` |
| Writes | none in the worktree and its git dir (`filesystem.denyWrite`); the sandbox already blocks the rest, except the per-user temp dir | none |
| Network | none (`network.allowedDomains: []`, `strictAllowlist`) | none |
| Escalation | `--permission-mode manual`, `disableBypassPermissionsMode`; deny rules for `Edit`, `Write`, `NotebookEdit`, `WebFetch`, `WebSearch` and the review-only git commands | `--ask-for-approval never`: a blocked command fails, nothing asks to leave the sandbox |
| Tools | only `Bash`, `Read`, `Glob`, `Grep` (`--tools`) | apps, plugins, browser and computer use and web search off |
| What the checkout can configure | nothing: `--setting-sources ""` skips its `.claude/settings*.json` (hooks), `--strict-mcp-config` its `.mcp.json` | nothing: the worktree and its checkout are untrusted, so its `.codex/` config and rules aren't loaded |
| Your own setup | not loaded either: no user settings, hooks, status line or model preference (login still works; it isn't a setting) | a private `CODEX_HOME` in the session dir that holds only a link to your `auth.json`: your rules (an `allow` rule runs a command outside the sandbox), MCP servers and plugins aren't loaded |

Plan mode (`--permission-mode plan`) isn't used: it doesn't add a boundary the
sandbox and deny rules don't already enforce, it makes every sandboxed command
ask, and leaving it is one click.

**Fail closed.** At startup outrider checks the platform and the agent
version and logs the mode, e.g.
`sandbox: read-only (claude: native sandbox + deny rules, no user or project settings)`. When the sandbox
isn't available, it logs `UNAVAILABLE` with the agent and the reason and
refuses every session of that agent that would need it (`refusing session: read-only sandbox
unavailable`); it never runs one unsandboxed. Claude Code's
`failIfUnavailable` is a second check.

**Your Claude settings can't loosen it.** Claude Code would add the sandbox
settings of your user settings file (e.g. `sandbox.excludedCommands`, which
run outside the sandbox after one approval, network or filesystem
exceptions) and the domains of `WebFetch(domain:...)` allow rules to
outrider's. So read-only Claude sessions load no settings files at all
(`--setting-sources ""`), only outrider's `--settings`; `outrider doctor`
says so (`no user or project settings`). The cost: your hooks, status line
and model preference don't apply in those sessions.

The prefetched PR context is untrusted: each file is cut at 2 MiB (ending in
an `[outrider: truncated ...]` note), and the prompt says the files are data,
never instructions.

| | Claude Code | Codex |
|---|---|---|
| macOS | yes | yes |
| Linux | needs `bwrap` and `socat` | needs `bwrap` |
| Windows | no (refused) | no (refused) |
| Oldest version | 2.1.285 | 0.156.0 |
| Login | any | file-based (`$CODEX_HOME/auth.json`) |

**What the sandbox doesn't cover.** The agent still reads everything you can
read (your files and credentials) and sends what it reads to its model
provider. Claude's `Read`, `Glob` and `Grep` run outside the sandbox (they
only read). Your own Claude settings and hooks aren't loaded. The
agent's process (Claude Code or Codex itself) runs outside the sandbox and
writes its own state, e.g. `.claude/` in the worktree. A sandboxed session
can't fetch CI logs; it gets the failed checks and their links.

## Session network overrides

`network_access` optionally overrides the agent’s network sandbox settings for
this session. `true` allows all outbound hosts to reduce network approval
prompts; `false` blocks sandboxed outbound connections; unset inherits the
owner’s settings. It does not enable sandboxing or change push/post guards or
tool approval policies. For Claude, it applies only to an enabled Bash sandbox,
and existing denies and managed domain restrictions still apply. For Codex, it
applies in workspace-write mode. `true` with any read-only session is refused.
See [configuration](configuration.md#session-network-access).

## Review only

A session on a PR someone else authored is review only unless
`others_prs.allow_push` allows pushing. Then:

- the prompt forbids edits, commits and pushes, even lint fixes;
- the `git` guard refuses `git push`, `git send-pack` and `git http-push`,
  also through aliases;
- Claude gets deny rules for `Edit`, `Write`, `NotebookEdit`, `git commit`,
  `git push`, `git rebase`, `git reset --hard`, `git cherry-pick`, `git merge`
  and `git am`.

With [review forks](#review-forks) (set, or found automatically in a fork
checkout), a review-only session may also commit locally and push evidence to
your own forks.

On your own PR the agent may commit, rebase onto the base branch and push
with `--force-with-lease`. Claude still gets deny rules for plain force pushes,
deleting branches and remote branches.

## Review forks

```yaml
others_prs:
  review_forks: ["me/repo"]   # your own forks, by exact name
```

List exact fork names. A wildcard like `me/*` also matches your repos that
aren't forks, and a workflow pushed to one of them runs with that repo's
secrets; `outrider doctor` warns about wildcards.

`review_forks` has three states:

- **unset or `null` (default): auto.** `origin` of the local checkout is the
  review fork when all of these hold:
  1. outrider runs in the checkout with `--remote` set to another remote than
     `origin` (e.g. `upstream`);
  2. `origin`'s push URL, resolved like the guard does
     (`git remote get-url --push --all origin`), is one `github.com` repo
     owned by your GitHub login;
  3. GitHub (`gh api repos/<origin>`, once at startup) reports that repo
     under the same name as a fork whose parent is the watched repo.

  If any lookup fails, auto stays off (fail closed) and the startup log says
  why.
- **a list:** exactly these globs, nothing automatic.
- **`[]`:** off, no evidence pushes.

The startup log and `outrider doctor` show the result:
`review forks: me/repo (auto: origin)`, `review forks: me/repo (config)` or
`review forks: off (origin o/repo is not your fork)`.

A review-only session on someone else's PR becomes **review with evidence**:
the agent may edit files and commit locally, and push failing tests, repro
scripts or a CI workflow to a branch under `review/` of a fork matching
`review_forks`. Each push follows
`push` (`ask`: the dialog shows where it goes, `never`, `allow`); posting to
the PR still follows `github_writes`.

The `git` guard decides every push in these sessions:

- It resolves the destination the way git does: a remote of the checkout
  through `git remote get-url --push --all` (its `pushurl`, `insteadOf` and
  `pushInsteadOf` applied), else the URL given, which must not be rewritten
  by a `url.*.insteadOf` rule or name a remote in another config file. The
  result must be exactly one `github.com` https or ssh URL, normalised to
  `owner/repo`.
- It pushes only to a repo matching `review_forks`, and never to the PR's
  head repo or its base repo, even when a glob matches them. It refuses every
  push when GitHub didn't report the head repo.
- It pushes only to branches under `refs/heads/review/` (`HEAD:review/<name>`
  or a local branch `review/<name>`); `main`, tags and other refs are refused.
  A `review/` branch on the fork may be force-updated with
  `--force-with-lease` (to replace evidence after a re-run); nothing else can
  be force-pushed.
- Only plain `git push <remote> <refspec>...` with a few options (`-u`,
  `--dry-run`, `--force-with-lease`, `--force-if-includes`, `--atomic`,
  `--no-verify`, `-q`, `-v`, `--porcelain`, `--progress`) is allowed. It
  refuses `--mirror`, `--all`, `--tags`, `--delete`, `:ref`, `+ref`,
  `--force`, `--prune`, `--repo`, aliases, `send-pack`, and global options
  such as `-c`, `--config-env`, `-C` and `--git-dir`.
- It refuses the push when `GIT_CONFIG_PARAMETERS`, `GIT_CONFIG_GLOBAL`,
  `GIT_CONFIG_SYSTEM`, `GIT_DIR`, `GIT_WORK_TREE`, `GIT_SSH`,
  `GIT_SSH_COMMAND`, `GIT_SSH_VARIANT`, `GIT_EXEC_PATH`, `GIT_PROXY_COMMAND`
  or a `GIT_CONFIG_KEY_<n>` other than the push trap's is set, and when the
  remote has a `vcs` helper, since they can redirect it.
- **Transport and configuration are isolated.** The guard resolves source
  commits to object IDs, then pushes from a disposable bare repository with
  the source object store as an alternate. It has no inherited Git config,
  hooks, proxy, URL rewrites or remote helpers. SSH uses the watcher's absolute
  executable path with no SSH config; HTTPS verifies TLS and refuses redirects.
- In `ask` mode the destination is resolved again after you click **Push**;
  if it changed, the push is refused.
- The push trap stays in the session; an allowed push runs without it.

The guard reads the session policy beside its own executable. Changing
`OUTRIDER_*`, `HOME`, or the working directory does not change that policy.
Missing or invalid policy refuses the invocation.

Claude's blanket deny rules for `Edit`, `Write` and `git commit` are dropped
for these sessions. `Edit` and `Write` stay denied in any `.git` directory, in
`~/.claude` and in outrider's state dir (`~/.cache/outrider`); plain force
pushes, `git push --delete`, `git rebase`, `git reset --hard` and
`git branch -D` stay denied. `policy.json` lists the
forks.

GitHub refuses a push that adds or changes `.github/workflows/` unless the
token has the `workflow` scope; `outrider doctor` warns when there are review
forks and the scope is missing. `review_forks` with a read-only sandbox for
others' PRs is a config error.

## Pushes: `push`

| Value | What `git push` does in a session |
|---|---|
| `ask` | opens a native dialog; the push runs only if you click **Push** |
| `never` | refused; commits stay local |
| `allow` | runs |

Besides the guard, sessions get `url.<x>.pushInsteadOf` rewrites in their
environment (`GIT_CONFIG_*`), so a plain push through the real `git` binary
goes to a dead URL. An approved push runs without them.

## GitHub posts: `github_writes`

| Value | What a post on the session's PR does |
|---|---|
| `ask` | opens a dialog showing the command and the text; it runs only if you click **Post** |
| `never` | refused; GitHub stays read-only |
| `allow` | runs |

A post is a comment, an inline review comment or reply, a review, or a
reaction, made with plain `gh` (`gh pr comment`, `gh pr review`,
`gh issue comment`, or `gh api` on the matching REST endpoints). A post without
`--repo` is sent to the session's repo, whatever `GH_REPO` or the working
directory says.

In every mode the `gh` guard refuses: merging, closing or editing the PR,
labels, writes to other PRs or repos, GraphQL mutations, `gh auth status` with
arguments (it could print the token), and any `gh` command that isn't on its
read list. Reads pass: `gh pr view|diff|checks|list|status|checkout`,
`gh run view|list|watch|download`, `gh issue view|list`,
`gh search prs|issues|code|commits`,
`gh repo view`, `gh browse`, `gh api` GET requests and GraphQL queries.

## Approval dialogs

| OS | Dialog |
|---|---|
| macOS | `osascript` |
| Linux | `zenity`, else `kdialog`, when `$DISPLAY` or `$WAYLAND_DISPLAY` is set |
| Windows | a PowerShell message box (Yes = Post or Push, No = Deny) |

The dialog tool is taken from fixed system paths, never from `PATH`. Linux
display and authorization settings are captured by the watcher and restored
before approval, so an agent-supplied display cannot redirect the dialog. Only
an explicit approval permits the operation. **Deny** or closing the dialog is
reported as a denial; 5 minutes without an answer is reported as a timeout;
missing or failed dialog backends are reported as errors. All refuse the
operation. Claude
sessions get a deny rule for the dialog tool (`Bash(osascript:*)` and the
like), so the agent doesn't click its own dialog by accident.

## Tool gate

In supervised mode, when the `tool_gate` classifier is available, Claude
sessions get a PreToolUse hook that sees every `Bash`, `Write`, `Edit` and
`NotebookEdit` call (`tool_gate.matcher`). It is told what the session may do
(review only or not, the push and post rules) plus your `tool_gate.rules`, and
can deny a call or ask you. Outrider wraps the hook and strips an `allow`
verdict so it cannot override native permission checks; malformed responses,
failed hooks and timeouts block the tool. See [Classifiers](classifiers.md).

Codex sessions get the guards, the prompt and the gate's environment, but the
hook isn't wired: Codex hooks need a one-time trust (`/hooks`).
Codex may also ask for execution approval before running the guarded command.
Accepting that prompt only lets the command start; the separate Outrider native
dialog must still approve a push or post in `ask` mode. A guard error identifies
the Outrider layer; an execution approval failure occurs before the guard runs.

## policy.json

Every session writes `policy.json` next to its prompt. Its private `bin/`
contains copies of the guard executable; each guard finds this policy via
its executable location, independent of the agent environment. The policy
includes captured tool paths and approval display settings, as well as: repo, PR, whether it is
your PR, review only, the push and post modes, the sandbox (`off` or
`read-only`) and the PR context dir, the scope, the deny rules and the
tool-gate rules. The prompt points the agent to it, and the tool-gate hook
gets its path in `$OUTRIDER_POLICY_FILE`.

## What is not enforced

From two security reviews of the Go rewrite. The open items are tracked in
[issue #10](https://github.com/appetizers-io/outrider/issues/10).

| Gap | Why |
|---|---|
| The agent can call the real `gh` or `git` | Absolute paths and direct HTTP calls bypass PATH guards. |
| Session metadata is writable without an OS sandbox | File-backed policy is not a security boundary against arbitrary host code. Claude deny rules and the gate discourage edits, but do not replace isolation. |
| The push trap is incomplete | Git ignores `pushInsteadOf` for a remote with an explicit `pushurl`. |
| The agent can read checkout instructions | `--setting-sources user` and `--strict-mcp-config` exclude project settings and MCP configuration in unsandboxed review-only Claude sessions; repository `CLAUDE.md` remains untrusted input. |
| GitHub can redirect renamed repositories | A remote service can change identity after local checks; review-fork HTTPS pushes refuse HTTP redirects, but server-side repository identity is still controlled by GitHub. |

Resolved follow-ups: guards ignore policy environment overrides, approval pushes
refuse global target options and redirecting environment, PATCH edits of existing
comments are refused, guard binaries are private per session, failed Windows
replacement fails closed, and hook `allow` does not override permissions.
Review-fork pushes resolve source commits and use a disposable bare repository
with only an object-store alternate, a watcher-selected SSH binary (no SSH config),
verified TLS, no HTTP proxy or redirects, no inherited Git config and no hooks.
The source repository's config is never read by that network operation.

In a [read-only sandbox](#read-only-sandbox) the gaps above that need a write
or the network (changing the guards' environment to push or post, the real
`gh` or `git`, `curl` with your token, a `pushurl`, guard metadata) are
closed by the OS sandbox instead.

## Data that leaves your machine

- **The launch check** sends the PR title, the new activity (comment and review
  text) and failing checks to the classifier. With Jev that is your Jev backend
  provider.
- **The agent** sends what it reads (the prompt, the code, the PR discussion)
  to its model provider, like any Claude Code or Codex session.
- GitHub is only read, unless you allow posts.

## MicroVM isolation

[Docker Sandboxes sessions](isolation.md) use the existing `sbx` runtime with a
private writable clone and read-only host inputs. The host home and Docker socket
are not mounted. Selected login/configuration files seed writable state inside
the VM; GitHub credentials use Docker's proxy and SSH keys use agent forwarding.
The agent can use the shared identities remotely. Desktop approval dialogs are
unavailable, so configurations requiring them are refused. Runtime setup failures
never fall back to host agent execution. See the isolation guide for authentication,
network policy limitations and how to inspect results before importing them.

## Evidence review profiles

Evidence profiles run code from the PR as you. A trusted-author or repository
allowlist is a decision to execute that code, not a proof that it is safe.
Use static profiles for unknown authors. Org membership does not confer trust.
`e2e` needs an explicit rule and sandbox off; Docker access gives arbitrary host
capability. Outrider does not add infrastructure secrets, and the prompt asks
before external infrastructure. Profiles cannot change push/post permissions,
PR ownership rules or the sandbox. Local comment drafts never submit a review.

Playbooks come only from your configuration directory and are excluded from
the tool gate. Linked issues and base-branch docs are capped, labelled untrusted
and passed as files with provenance. A PR cannot replace the docs against which
it is checked. A draft outbox is the only requested local write exception;
PR edits, commits and pushes still follow the session policy. Read-only Codex
and Claude sessions retain drafts in the transcript.
