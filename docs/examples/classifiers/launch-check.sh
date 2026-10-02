#!/bin/sh
# A launch-check classifier for outrider (kind: command, launch_command).
#
# outrider writes the request as JSON on stdin and reads the answer from
# stdout: {"launch": true|false} or {"probability": 0..1}, optionally with
# "reason". Anything else, a failure or a timeout means "launch".
#
# This one skips a session when every piece of new activity comes from a bot
# (a login ending in [bot]) and no CI check fails. It only uses POSIX sh and
# grep, so it runs anywhere outrider does (on Windows: Git Bash).
request=$(cat)

users=$(printf '%s' "$request" | grep -o '"user":"[^"]*"' | wc -l)
bots=$(printf '%s' "$request" | grep -o '"user":"[^"]*\[bot\]"' | wc -l)
failing=$(printf '%s' "$request" | grep -c '"failing_checks":\[\]' || true)

if [ "$users" -gt 0 ] && [ "$users" -eq "$bots" ] && [ "$failing" -eq 1 ]; then
  echo '{"launch": false, "reason": "only bot activity, CI green"}'
else
  echo '{"launch": true}'
fi
