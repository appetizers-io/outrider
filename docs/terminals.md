# Terminals

A session opens in a new terminal window or in a detached tmux session.

## Launcher

| `launcher` / `--launcher` | Sessions open in |
|---|---|
| `auto` (default) | macOS: the terminal in a desktop (Aqua) session, else tmux. Linux: the terminal when `$DISPLAY` or `$WAYLAND_DISPLAY` is set and a terminal is found, else tmux. Windows: the terminal. |
| `terminal` | the terminal app from `terminal` (below) |
| `tmux` | a detached tmux session per PR |

## Which terminal

With `terminal: auto` (the default) outrider detects the terminal once at
startup and logs where it found it:

```text
level=INFO msg="terminal: iTerm2 (from $TERM_PROGRAM)"
```

The first match wins:

1. `$TERM_PROGRAM`: `Apple_Terminal`, `iTerm.app`, `ghostty`, `WezTerm`, `kitty` (`tmux` is ignored)
2. variables that survive inside tmux: `$LC_TERMINAL` (iTerm2), `$__CFBundleIdentifier` (the macOS app), `$GHOSTTY_RESOURCES_DIR`, `$WEZTERM_EXECUTABLE`, `$KITTY_WINDOW_ID`, `$WT_SESSION` (Windows Terminal)
3. `$TERMINAL`, run as `$TERMINAL -e <command>`
4. the platform default: Terminal.app on macOS; `x-terminal-emulator` on Linux; Windows Terminal (`wt.exe`), else `cmd`, on Windows

To pick one yourself, name it:

| Name | Opens |
|---|---|
| `terminal-app` | macOS Terminal (`open -g -a Terminal`, no focus steal) |
| `iterm` | iTerm2 (`open -g -a iTerm`, no focus steal) |
| `ghostty` | Ghostty |
| `wezterm` | WezTerm |
| `kitty` | kitty |
| `windows-terminal` | Windows Terminal (`wt.exe`) |
| `x-terminal-emulator` | the Debian/Ubuntu default terminal |
| `cmd` | a `cmd` window (`start`) |

```sh
outrider --terminal iterm
```

Anything else is a command with a `{cmd}` placeholder for the session command.
As its own word, `{cmd}` becomes the command's words; inside a longer word it
becomes the command as one shell-quoted string.

```yaml
terminal: [alacritty, -e, "{cmd}"]
# or: terminal: [sh, -c, "exec {cmd}"]
```

```sh
outrider --terminal 'alacritty -e {cmd}'
```

## tmux

Each PR gets a session named after it, e.g. `pr-octo-org-app-42`. The log says
how to attach:

```text
level=INFO msg="octo-org/app#42: attach with: tmux attach -t pr-octo-org-app-42"
```

A finished session waits for Enter before it closes; a new launch for the same
PR replaces it.

## Platform notes

- **macOS**: only Terminal.app and iTerm2 are verified. Ghostty, WezTerm and
  kitty are opened with `open -n -a <app> --args …`.
- **Linux**: without a display (SSH, a server) `auto` uses tmux. Approval
  dialogs need `zenity` or `kdialog` and a display; without one, pushes and
  posts in `ask` mode are denied.
- **Windows**: tmux isn't available. The tool-gate hook command is quoted for
  a POSIX shell (Claude Code runs hooks through Git Bash). The guards are
  copies of the binary (`gh.exe`, `git.exe`). Launching in Windows Terminal
  isn't verified on a real machine yet.
