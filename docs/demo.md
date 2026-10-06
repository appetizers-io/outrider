# README demo

`demo.gif` records a real local Outrider session on public PR #55. The session
uses Codex in read-only mode, with GitHub writes and pushes disabled. Its
smoke-test prompt asks for a README description. A small display adapter prints
the live Codex JSON connection, command and message events; it hides the echoed
prompt and diagnostic metadata. The recorded agent reads the README and exits 0.
This is a session-launch demonstration, not a polling replay or a fixture agent.

To record another prepared local session with the same tools:

```sh
asciinema record --window-size 100x18 --idle-time-limit 2 \
  --output-format asciicast-v2 \
  --command 'outrider session run <prepared-session-dir>' demo.cast
agg --idle-time-limit 2 --last-frame-duration 5 --font-size 15 \
  demo.cast docs/demo.gif
```

Use a public, disposable review and disable pushes/posts. Inspect every frame
before committing: terminal output can reveal local paths, discussion text or
credentials. Do not upload a recording of private work. Keep the GIF small and
verify its last frame shows the successful exit.
