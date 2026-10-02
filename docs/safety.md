# Safety

outrider starts an AI agent on code other people wrote, with your GitHub login.
This page describes the guardrails around that, and their limits.

> **The guardrails catch an agent's mistakes. They are not a sandbox.** The
> agent runs as you, with your files, credentials and network. A determined or
> prompt-injected agent can get around every guard listed here. For PRs from
> people you don't trust, keep the tool gate on (supervised mode) or run the
> agent in a sandbox: the Claude Code sandbox, a container or a VM, without
> write access to `~/.cache/outrider` and without your push credentials.

## Modes

| | `mode: supervised` (default) | `mode: autonomous` |
|---|---|---|
| Someone else's PR | review only | may push fixes (the prompt asks for fast-forward only), unless `others_prs.allow_push: false` |
| `push` default | `ask` | `allow` |
| `github_writes` default | `ask` | `allow` |
| Claude deny rules | yes | no |
| Tool gate (PreToolUse hook) | yes, when its classifier is available | no |

## Review only

A session on a PR someone else authored is review only unless
`others_prs.allow_push` allows pushing. Then:

- the prompt forbids edits, commits and pushes, even lint fixes;
- the `git` guard refuses `git push`, `git send-pack` and `git http-push`,
  also through aliases;
- Claude gets deny rules for `Edit`, `Write`, `NotebookEdit`, `git commit`,
  `git push`, `git rebase`, `git reset --hard`, `git cherry-pick`, `git merge`
  and `git am`.

On your own PR the agent may commit, rebase onto the base branch and push
with `--force-with-lease`. Claude still gets deny rules for plain force pushes,
deleting branches and remote branches.

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
your PR, review only, the push and post modes, the scope, the deny rules and
the tool-gate rules. The prompt points the agent to it, and the tool-gate hook
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

## Data that leaves your machine

- **The launch check** sends the PR title, the new activity (comment and review
  text) and failing checks to the classifier. With Jev that is your Jev backend
  provider.
- **The agent** sends what it reads (the prompt, the code, the PR discussion)
  to its model provider, like any Claude Code or Codex session.
- GitHub is only read, unless you allow posts.
