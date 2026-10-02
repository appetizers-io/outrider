# Running as a service

Use these when outrider should run in the background and start at login,
instead of in a terminal you keep open.

| File | For |
|---|---|
| [`io.appetizers.outrider.plist`](io.appetizers.outrider.plist) | macOS launchd user agent; sessions open in your terminal and dialogs show |
| [`outrider.service`](outrider.service) | Linux systemd user service; no display, so sessions open in tmux and `ask` denies |

Edit the paths in the file first (`/Users/you`, `some-repo`), then:

```sh
# macOS
cp docs/examples/service/io.appetizers.outrider.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/io.appetizers.outrider.plist

# Linux
cp docs/examples/service/outrider.service ~/.config/systemd/user/
systemctl --user daemon-reload && systemctl --user enable --now outrider
```

Without a display, pair the service with
[`headless.yaml`](../configs/headless.yaml). More in
[Recipes](../../recipes.md#run-it-as-a-background-service).
