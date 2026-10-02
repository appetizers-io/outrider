# Classifiers

A classifier is a decision backend. outrider uses one in two roles:

| Role | Config | Question | Default |
|---|---|---|---|
| Launch check | `launch_check` | Is this new activity worth an agent session? | `jev` |
| Tool gate | `tool_gate` | May the agent make this tool call? (supervised Claude sessions) | `jev` |

`classifiers` defines them by name. `jev` is always defined; add your own with
`kind: command`. A role whose classifier can't run (disabled, binary missing,
Jev without a backend key) is off, and the startup log says why:

```text
level=INFO msg="launch check: off (jev: no backend key (TYPESAFE_API_KEY, OPENROUTER_API_KEY, AI_GATEWAY_API_KEY, JEV_BACKEND))"
level=INFO msg="tool gate: on (local: command)"
```

## Jev

[Jev](https://www.npmjs.com/package/jev-use) is the built-in classifier. The
launch check runs `jev-use judge`, the tool gate `jev-use hook gate`.

1. Install `jev-use` (`npm install -g jev-use`), or have `npx` on your `PATH`;
   outrider then runs `npx -y jev-use@0.8.0`.
2. Put a backend key in the environment outrider runs in:
   `TYPESAFE_API_KEY`, `OPENROUTER_API_KEY`, `AI_GATEWAY_API_KEY` or
   `JEV_BACKEND`.

```yaml
classifiers:
  jev:
    enabled: auto              # auto: on when the command and a backend key exist; true; false
    command: null              # e.g. "jev-use"; null: jev-use on PATH, else npx
    confidence_threshold: null # below this confidence Jev escalates, which launches
    timeout_seconds: 60        # a slower check launches anyway
launch_check:
  classifier: jev              # null: always launch
  skip_below: 0.5              # skip when Jev is confident the probability is below this
```

`--no-jev` switches it off for a run, `--jev-cmd` sets `command`.

The launch check sends the PR title, the new activity and the failing checks
to your Jev backend provider.

## Your own launch check

A `kind: command` classifier with a `launch_command` reads a JSON request on
stdin and prints an answer on stdout.

```yaml
classifiers:
  local:
    kind: command
    launch_command: sh ~/outrider/launch-check.sh
    timeout_seconds: 30
launch_check:
  classifier: local
```

The request (abridged; the real one is a single line):

```json
{
  "version": 1,
  "repo": "octo-org/app", "pr": 42, "title": "Add retries", "url": "https://github.com/octo-org/app/pull/42",
  "author": "mona", "own": true, "owner": "Mona", "owner_login": "mona",
  "trigger": "my PR notification (comment)",
  "failing_checks": ["test"],
  "activity": [
    {"kind": "review", "state": "CHANGES_REQUESTED", "user": "hubot", "path": null,
     "at": "2026-01-02T10:00:00Z", "body": "Please handle the error.", "url": "https://github.com/…"}
  ],
  "question": "Should a coding agent working for Mona act on this PR now: …",
  "state_text": "the same, as text for a language model"
}
```

The answer:

| Output | Effect |
|---|---|
| `{"launch": false, "reason": "…"}` | skip |
| `{"launch": true}` | launch |
| `{"probability": 0.2}` | skip when below `launch_check.skip_below` |
| anything else, an error, a timeout | launch |

[`examples/classifiers/launch-check.sh`](examples/classifiers/launch-check.sh)
is a complete one: it skips when all new activity comes from `[bot]` logins
and no check fails.

## Your own tool gate

A `hook_command` is a [Claude Code PreToolUse
hook](https://code.claude.com/docs/en/hooks). In supervised
Claude sessions outrider wires it into `claude-settings.json` for the tools in
`tool_gate.matcher`. It gets the tool call as JSON on stdin; exit code 2 blocks
the call and shows stderr to the agent, or it prints a `permissionDecision`.

```yaml
classifiers:
  local:
    kind: command
    hook_command: sh ~/outrider/gate-hook.sh
tool_gate:
  classifier: local
  matcher: Bash|Write|Edit|NotebookEdit
  threshold: null       # passed on in $OUTRIDER_GATE_THRESHOLD
  rules:                # your own rules, added to the session's rules
    - never modify generated/
  include_prompt_extra: false
```

The hook's environment:

| Variable | Content |
|---|---|
| `OUTRIDER_GATE_TEXT` | what this session may do (review only or not, push and post rules) plus your `tool_gate.rules` |
| `OUTRIDER_POLICY_FILE` | the session's `policy.json` |
| `OUTRIDER_GATE_THRESHOLD` | `tool_gate.threshold`, when set |

[`examples/classifiers/gate-hook.sh`](examples/classifiers/gate-hook.sh)
blocks force pushes, `rm -rf` and edits under `generated/`. A real gate would
judge the call against `OUTRIDER_GATE_TEXT`.

One classifier can have both commands. A `kind: command` classifier used as the
launch check needs a `launch_command`, one used as the tool gate a
`hook_command`; `config check` says when one is missing.

[`custom-classifier.yaml`](examples/configs/custom-classifier.yaml) wires
both scripts up.

## Testing the examples

The examples run in the test suite (`TestDocsExample*` in
[`internal/classifier`](../internal/classifier/examples_test.go)), through the
same code path outrider uses. To try one by hand:

```sh
echo '{"activity":[{"user":"netlify[bot]"}],"failing_checks":[]}' | sh docs/examples/classifiers/launch-check.sh
echo '{"tool_name":"Bash","tool_input":{"command":"git push --force"}}' | sh docs/examples/classifiers/gate-hook.sh; echo "exit $?"
```
