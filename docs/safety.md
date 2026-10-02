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
| What the checkout can configure | nothing: `--setting-sources user` skips its `.claude/settings*.json` (hooks), `--strict-mcp-config` its `.mcp.json` | nothing: the worktree and its checkout are untrusted, so its `.codex/` config and rules aren't loaded |
| Your own setup | your user settings and hooks still load | a private `CODEX_HOME` in the session dir that holds only a link to your `auth.json`: your rules (an `allow` rule runs a command outside the sandbox), MCP servers and plugins aren't loaded |

Plan mode (`--permission-mode plan`) isn't used: it doesn't add a boundary the
sandbox and deny rules don't already enforce, it makes every sandboxed command
ask, and leaving it is one click.

**Fail closed.** At startup outrider checks the platform and the agent
version and logs the mode, e.g.
`sandbox: read-only (claude: native sandbox + deny rules)`. When the sandbox
isn't available, it logs `UNAVAILABLE` with the reason and refuses every
session that would need it (`refusing session: read-only sandbox
unavailable`); it never runs one unsandboxed. Claude Code's
`failIfUnavailable` is a second check.

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
only read). Your own user-level Claude hooks and settings still run. The
agent's process (Claude Code or Codex itself) runs outside the sandbox and
writes its own state, e.g. `.claude/` in the worktree. A sandboxed session
can't fetch CI logs; it gets the failed checks and their links.

## Review only

A session on a PR someone else authored is review only unless
`others_prs.allow_push` allows pushing. Then:

- the prompt forbids edits, commits and pushes, even lint fixes;
- the `git` guard refuses `git push`, `git send-pack` and `git http-push`,
  also through aliases;
- Claude gets deny rules for `Edit`, `Write`, `NotebookEdit`, `git commit`,
  `git push`, `git rebase`, `git reset --hard`, `git cherry-pick`, `git merge`
  and `git am`.

With [`others_prs.review_forks`](#review-forks) set, a review-only session may
also commit locally and push evidence to your own forks.

On your own PR the agent may commit, rebase onto the base branch and push
with `--force-with-lease`. Claude still gets deny rules for plain force pushes,
deleting branches and remote branches.

## Review forks

```yaml
others_prs:
  review_forks: ["me/*"]   # owner/repo globs of your own forks
```

A review-only session on someone else's PR becomes **review with evidence**:
the agent may edit files and commit locally, and push failing tests, repro
scripts or a CI workflow to a fork matching `review_forks`. Each push follows
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
- Only plain `git push <remote> <refspec>...` with a few options (`-u`,
  `--dry-run`, `--force-with-lease`, `--force-if-includes`, `--atomic`,
  `--no-verify`, `-q`, `-v`, `--porcelain`, `--progress`) is allowed. It
  refuses `--mirror`, `--all`, `--tags`, `--delete`, `:ref`, `+ref`,
  `--force`, `--prune`, `--repo`, aliases, `send-pack`, and global options
  such as `-c`, `--config-env`, `-C` and `--git-dir`.
- It refuses the push when `GIT_CONFIG_PARAMETERS`, `GIT_CONFIG_GLOBAL`,
  `GIT_CONFIG_SYSTEM`, `GIT_DIR`, `GIT_WORK_TREE` or a `GIT_CONFIG_KEY_<n>`
  other than the push trap's is set, since they can redirect it.
- In `ask` mode the destination is resolved again after you click **Push**;
  if it changed, the push is refused.
- The push trap stays in the session; an allowed push runs without it.

Claude's deny rules for `Edit`, `Write` and `git commit` are dropped for these
sessions; plain force pushes, `git push --delete`, `git rebase`,
`git reset --hard` and `git branch -D` stay denied. `policy.json` lists the
forks.

GitHub refuses a push that adds or changes `.github/workflows/` unless the
token has the `workflow` scope; `outrider doctor` warns when `review_forks` is
set and the scope is missing. `review_forks` with a read-only sandbox for
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

The dialog tool is taken from fixed system paths, never from `PATH`. No dialog,
**Deny**, closing it, or 5 minutes without an answer all mean "no". Claude
sessions get a deny rule for the dialog tool (`Bash(osascript:*)` and the
like), so the agent doesn't click its own dialog by accident.

## Tool gate

In supervised mode, when the `tool_gate` classifier is available, Claude
sessions get a PreToolUse hook that sees every `Bash`, `Write`, `Edit` and
`NotebookEdit` call (`tool_gate.matcher`). It is told what the session may do
(review only or not, the push and post rules) plus your `tool_gate.rules`, and
can deny a call or ask you. See [Classifiers](classifiers.md).

Codex sessions get the guards, the prompt and the gate's environment, but the
hook isn't wired: Codex hooks need a one-time trust (`/hooks`).

## policy.json

Every session writes `policy.json` next to its prompt: repo, PR, whether it is
your PR, review only, the push and post modes, the sandbox (`off` or
`read-only`) and the PR context dir, the scope, the deny rules and the
tool-gate rules. The prompt points the agent to it, and the tool-gate hook
gets its path in `$OUTRIDER_POLICY_FILE`.

## What is not enforced

From two security reviews of the Go rewrite. The open items are tracked in
[issue #10](https://github.com/appetizers-io/outrider/issues/10).

| Gap | Why |
|---|---|
| The agent can change the guard's mode | The guards read `OUTRIDER_PUSH`, `OUTRIDER_GH_WRITES` and the real binary paths from the environment, which the agent controls. |
| The agent can call the real `gh` or `git` | By absolute path, or with `curl` and your token. The guards only cover the names on `PATH`. |
| The push trap is incomplete | Git ignores `pushInsteadOf` for a remote with an explicit `pushurl`. |
| The dialog deny rules are prefixes | `/usr/bin/osascript`, `env osascript` or `pwsh` aren't matched. |
| A PR's checkout can configure the agent | Its `.claude/settings.json`, `.mcp.json` and `CLAUDE.md` are loaded in the worktree. |
| The Linux dialog uses the agent's display | The agent could start its own X server and answer the dialog there. |
| The push dialog shows the session's checkout | A push with `-C`, `--git-dir` or `GIT_DIR` can target something else. |
| `gh api -X PATCH …/issues/comments/<id>` | In `ask` or `allow` mode it can edit any comment in the session's repo, not only yours. |
| The guard links are shared | All sessions use `~/.cache/outrider/bin`, which one session could replace. |
| A review-fork push is checked, then run | Config changed by a background process between the check and the push, a remote's `vcs` helper, a hook, or GitHub redirecting a renamed repo can still send it elsewhere. |

In a [read-only sandbox](#read-only-sandbox) the gaps above that need a write
or the network (changing the guards' environment to push or post, the real
`gh` or `git`, `curl` with your token, a `pushurl`, a shared guard link) are
closed by the OS sandbox instead.

## Data that leaves your machine

- **The launch check** sends the PR title, the new activity (comment and review
  text) and failing checks to the classifier. With Jev that is your Jev backend
  provider.
- **The agent** sends what it reads (the prompt, the code, the PR discussion)
  to its model provider, like any Claude Code or Codex session.
- GitHub is only read, unless you allow posts.
