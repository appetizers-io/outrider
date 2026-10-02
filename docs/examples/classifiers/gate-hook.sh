#!/bin/sh
# A tool-gate hook for outrider (kind: command, hook_command).
#
# Claude Code runs it before every tool call that tool_gate.matcher selects,
# with the call as JSON on stdin. Exit 0 allows the call; exit 2 blocks it and
# shows stderr to the agent. outrider also sets:
#   OUTRIDER_GATE_TEXT       what this session may do, plus your tool_gate.rules
#   OUTRIDER_POLICY_FILE     the session's policy.json
#   OUTRIDER_GATE_THRESHOLD  tool_gate.threshold, when set
#
# This one blocks force pushes, `rm -rf` and edits under generated/. A real
# gate would judge the call against OUTRIDER_GATE_TEXT, e.g. with a model.
call=$(cat)

case "$call" in
  *'git push --force'* | *'git push -f'* | *'rm -rf'*)
    echo "blocked by the tool gate: not allowed in this session" >&2
    exit 2
    ;;
  *'"file_path"'*'/generated/'*)
    echo "blocked by the tool gate: never modify generated/" >&2
    exit 2
    ;;
esac
exit 0
