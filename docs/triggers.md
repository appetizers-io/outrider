# Triggers

A trigger decides that new activity on a pull request may need you. Each one
can be switched off and can skip the [launch check](classifiers.md). The text
in brackets in a `matched` log line names the trigger.

| Trigger | Fires on | Session covers | Log text |
|---|---|---|---|
| [Own PRs](#own-prs) | any notification on a PR you authored | the whole PR | `my PR notification (<reason>)` |
| [👀 opt-in](#-opt-in) | your reaction on someone else's PR, then changes to its reviews and comments | the whole PR | `👀 opt-in (on review comment)`, then `review/discussion changed` |
| [Review replies](#review-replies) | a reply in a review thread you took part in | the replied threads | `reply to my review comment(s): <urls>` |
| [@mentions](#mentions) | `@you` on someone else's PR | the mentioning comments | `@<login> mentioned: <urls>` |

`ignore_authors` applies to all of them: activity only by those logins (globs
with `*` and `?`, e.g. `"*[bot]"`) never starts a session.

## Own PRs

Any notification on a PR you authored: a review, a comment, a CI result. The
launch check sees the activity since the last session on that PR and skips
the ones with nothing to do.

```yaml
triggers:
  own_prs:
    enabled: true
    check: true        # run the launch check first
```

On the first live run, existing notifications on your own PRs are only
recorded. Pass `--process-existing` to launch for them.

## 👀 opt-in

React with 👀 to someone else's PR (its description, a comment, a review or an
inline review comment) to opt it in. The first session starts without a launch
check. After that outrider watches the PR and relaunches when its reviews or
comments change. Remove the reaction to stop watching.

```yaml
triggers:
  opt_in:
    enabled: true
    reaction: eyes     # +1, -1, laugh, confused, heart, hooray, rocket, eyes
    where: [description, comment, review, review_comment]
    on_change:
      check: true                # launch check before a relaunch
      ignore_own_activity: true  # your own comments never relaunch
candidate_limit: 50   # most recent non-owned PRs checked for the reaction per poll
```

outrider finds candidate PRs in your notifications and with a search for open
PRs that involve you or request your review, so a reaction on an older PR
still counts.

## Review replies

Someone replies in a review thread you took part in, and the last comment in
the thread isn't yours. With `scope: thread` the session handles only those
threads.

```yaml
triggers:
  review_replies:
    enabled: true
    check: true
    scope: thread             # thread | pr
    fresh_within_hours: null  # only replies this recent; null: lookback_hours
```

Old, unanswered threads don't trigger when outrider starts watching a PR:
only replies newer than `fresh_within_hours` (or `lookback_hours`) count.

## @mentions

Someone writes `@<your login>` in the description, a comment or a review of a
PR that is neither yours nor opted in (those relaunch on any activity anyway).
Your own comments and mentions inside words, email addresses and paths
(`me@example.com`, `@meadow`) don't count. A mention that is also a handled
review reply doesn't launch twice.

```yaml
triggers:
  mentions:
    enabled: true
    check: true
    scope: comment            # comment | pr
    fresh_within_hours: null
```

## Which repos

Without `repos.include` (or `--repo`), outrider watches the repo of the
checkout you run it in, or every repo when you run it elsewhere. See
[Configuration](configuration.md#repos-and-authors).
