# Workflows

A workflow gives the agent a named sequence of instructions. It replaces the
default action instructions for a matching session. The prompt tells the agent
to complete each step in order, stop and explain blocked or uncertain checks,
and recheck the current PR state before approving. Steps are natural-language
instructions executed in one agent session, not a deterministic command runner
or a persisted checkpoint for each step.

## Dependency reviews

```yaml
workflows:
  - name: dependency-review
    match:
      - prs: others
        authors: ['renovate[bot]', 'dependabot[bot]', renovate, dependabot]
    post: true
    steps:
      - Read the changelog and release notes for every dependency update.
      - Check compatibility with this project's runtime, APIs and other dependencies.
      - Inspect breaking changes, migrations and removed behavior; run relevant existing tests.
      - Check CI for the current PR head; pending or missing checks are not a pass.
      - Approve this PR only if all checks support approval; otherwise summarize the reasons here, or on the PR when posting is allowed.
```

For someone else's PR, a matching activity workflow also processes new PR
notifications without requiring an opt-in reaction. It uses a `notification`
event and takes precedence over replies and mentions for that notification.
It does not search all open dependency PRs: the PR must first be discovered
through the existing notification or opt-in mechanisms.

`post: true` explicitly authorizes only the GitHub actions requested in the
steps. The default `github_writes: ask` still opens a dialog for each post.
Use `github_writes: allow` in a matching override to permit those posts without
the dialog. `github_writes: never` and read-only sandboxes always block them.
With `post: false` (the default), the agent prepares its output in the session.
Workflow instructions do not grant permission to merge or resolve threads.

## Technical review with evidence

```yaml
others_prs:
  allow_push: false
  review_forks: [me/repo]
workflows:
  - name: technical-review
    match:
      - prs: others
        events: [opt_in, review_change]
      - prs: others
        labels: [feature, tech]
    steps:
      - Fetch the latest PR state, head commit, diff and CI results.
      - Read all open review discussions and identify unresolved concerns.
      - Review the full change for correctness, security, compatibility and test coverage.
      - Validate possible findings with focused test files or shell repro scripts on a local review evidence branch; record commands and results.
      - Push validated evidence to a configured review fork under review/ when permitted; keep it local if pushing is blocked.
      - Prepare precise review comments with PR file and line references, evidence links in the fork, and the observed test results. Keep the comments here for review.
```

Use your own fork's exact `owner/repo` name. The existing
[review fork rules](safety.md) permit local evidence edits and commits while
protecting the original PR branch and both its head and base repositories.
Without a review fork, review-only sessions cannot create evidence files;
the workflow reports that step as blocked. Scoped mention/reply sessions
remain limited to their triggering comments. Set their existing `scope: pr`
when a full PR review is wanted.

## Conditions

```yaml
workflows:
  - name: after-discussions
    on: discussions_resolved
    match: [{repo: my-org/*, prs: others}]
    steps:
      - Confirm every review thread is still resolved on the latest PR.
      - Review the final diff and CI results, then prepare an approval recommendation here.
  - name: after-ci
    on: ci_passed
    match: [{repo: my-org/*, prs: own}]
    steps:
      - Inspect the current PR state and report that its checks have passed.
```

Conditions watch discovered PRs independently of notifications. The watcher
retains matching PRs across restarts, checks them each poll and forgets closed
or merged PRs. `discussions_resolved` requires at least one review thread and
all threads resolved; ordinary conversation comments have no resolved state.
The lookup covers every page and fails closed when resolution data is missing.
`ci_passed` requires at least one reported check and every check successful;
pending, failed, skipped, neutral and missing checks do not satisfy it.

On the first live run, satisfied conditions are recorded without launching,
unless `--process-existing` is set. After that, a newly discovered matching
PR can launch immediately when its condition is satisfied. Each conditional
workflow launches once per observed false-to-true transition. It rearms when
the watcher observes the condition false again. Changing the head while CI
stays successful between polls does not count as a new transition.

Failed lookups and refused launches remain pending. Launch acceptance is
recorded, not successful completion of the agent's steps: an agent failure
does not automatically retry the workflow. `--dry-run` logs eligible launches
without consuming a transition. Conditional workflows bypass the launch
classifier, as do activity sessions with a matching workflow.

## Matching and precedence

| Key | Meaning |
|---|---|
| `repo`, `url`, `prs` | Existing repository and ownership match rules |
| `authors` | Login globs, case-insensitive; `*` and `?`, with `[bot]` literal |
| `labels` | At least one PR label matches a glob |
| `title`, `head` | Title or head branch matches a glob |
| `events` | Any of `notification`, `own_pr`, `opt_in`, `review_change`, `review_reply`, `mention`; activity workflows only |

Each `match` entry requires all its supplied attributes. Entries are
alternatives, and values within lists are alternatives. `match: [{}]` matches
any PR. Repo, label, title and branch globs are case-sensitive and use the
existing repository glob syntax.

Names must be unique within the effective configuration. `on` defaults to
`activity`. The first matching activity workflow wins; conditional workflows
are evaluated independently in file order. Normal agent locks and
`max_agents` still limit launches. You can set `workflows` inside existing
`overrides`; the list replaces earlier workflows, and `workflows: []` disables
them for that override. Push, sandbox, scope and GitHub write policies apply
throughout every workflow.
