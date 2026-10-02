# Recipes

## Work in a fork

In a fork checkout, `origin` is your fork and `upstream` the project. Watch the
project and branch worktrees off your checkout:

```sh
cd ~/dev/my-fork
outrider --remote upstream
```

Agents on your own PRs rebase onto `upstream/<base>`.

## Ignore bots

Activity only by these logins never starts a session:

```yaml
ignore_authors: ["*[bot]", "renovate", "netlify"]
```

On watched (👀) PRs your own comments don't relaunch either
(`triggers.opt_in.on_change.ignore_own_activity`, on by default). For noise
that isn't from a bot, use a [launch check](classifiers.md).

## Use Codex or Claude Code

```sh
outrider --agent claude
outrider --agent codex       # the default
```

or `agent: claude` in the config. Claude sessions get the deny rules and the
tool gate in supervised mode; Codex sessions get the guards and the prompt
(see [Safety](safety.md#tool-gate)).

## Watch many repos

Run outrider outside a checkout and list the repos:

```yaml
repos:
  include: ["my-org/*", "other-org/tool"]
  exclude: ["my-org/{website,sandbox}"]
max_agents: 3
```

or with flags: `outrider --repo 'my-org/*' --repo other-org/tool`. Without
`repos.include`, outrider outside a checkout watches every repo you get
notifications for. Repos without a local checkout are cloned once into
`~/.cache/outrider/repos/`.

## Run it as a background service

Start one instance per user, in the checkout it should watch (or with
`repos.include`). Logs go to stderr; `--log-format json` makes them easy to
filter.

**macOS (launchd)**: [`examples/service/io.appetizers.outrider.plist`](examples/service/io.appetizers.outrider.plist)
is a user agent. A user agent runs in your desktop session, so sessions open in
your terminal and approval dialogs show up.

```sh
mkdir -p ~/Library/LaunchAgents
cp docs/examples/service/io.appetizers.outrider.plist ~/Library/LaunchAgents/
# edit the paths, then
launchctl load ~/Library/LaunchAgents/io.appetizers.outrider.plist
tail -f ~/Library/Logs/outrider.log
```

**Linux (systemd)**: [`examples/service/outrider.service`](examples/service/outrider.service)
is a user service. It has no display, so sessions open in tmux, and approval
dialogs can't show: pushes and posts in `ask` mode are denied.

```sh
mkdir -p ~/.config/systemd/user
cp docs/examples/service/outrider.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now outrider
journalctl --user -u outrider -f
```

## Try a config without side effects

```sh
outrider --config ./try.yaml --dry-run --once --log-level debug
```

Nothing is launched, written or posted.

## Start over

```sh
outrider --reset-state --once
```

forgets which notifications and comments were handled. The next live run is a
first run again: existing notifications on your own PRs are only recorded.
