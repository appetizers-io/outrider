package session

import (
	"strings"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/shell"
)

// Terminal is the resolved terminal app sessions open in.
type Terminal struct {
	Name    string   // a config.TerminalNames entry; "" for a custom command
	Command []string // custom command with {cmd}
	Source  string   // where it came from, for the startup log
}

var terminalDisplay = map[string]string{
	"terminal-app":        "Terminal.app",
	"iterm":               "iTerm2",
	"ghostty":             "Ghostty",
	"wezterm":             "WezTerm",
	"kitty":               "kitty",
	"windows-terminal":    "Windows Terminal",
	"x-terminal-emulator": "x-terminal-emulator",
	"cmd":                 "cmd",
}

// String is what the startup log shows, e.g. "iTerm2 (from $TERM_PROGRAM)".
func (t Terminal) String() string {
	name := terminalDisplay[t.Name]
	switch {
	case t.Command != nil:
		name = strings.Join(t.Command, " ")
	case t.Name == "":
		name = "none"
	}
	return name + " (" + t.Source + ")"
}

// Found tells whether a terminal was resolved.
func (t Terminal) Found() bool { return t.Name != "" || t.Command != nil }

var termProgram = map[string]string{
	"Apple_Terminal": "terminal-app",
	"iTerm.app":      "iterm",
	"ghostty":        "ghostty",
	"WezTerm":        "wezterm",
	"kitty":          "kitty",
}

var bundleIDs = map[string]string{
	"com.apple.Terminal":     "terminal-app",
	"com.googlecode.iterm2":  "iterm",
	"com.mitchellh.ghostty":  "ghostty",
	"com.github.wez.wezterm": "wezterm",
	"net.kovidgoyal.kitty":   "kitty",
}

// DetectTerminal finds the user's terminal; the first signal that matches wins:
// $TERM_PROGRAM (tmux ignored), then variables that survive inside tmux
// ($LC_TERMINAL, $__CFBundleIdentifier, $GHOSTTY_RESOURCES_DIR,
// $WEZTERM_EXECUTABLE, $KITTY_WINDOW_ID, $WT_SESSION), then $TERMINAL, then
// the platform's default. On Linux the default needs x-terminal-emulator;
// without it nothing is found.
func DetectTerminal(goos string, getenv func(string) string, lookPath func(string) (string, error)) Terminal {
	if name, ok := termProgram[getenv("TERM_PROGRAM")]; ok {
		return Terminal{Name: name, Source: "from $TERM_PROGRAM"}
	}
	if getenv("LC_TERMINAL") == "iTerm2" {
		return Terminal{Name: "iterm", Source: "from $LC_TERMINAL"}
	}
	if name, ok := bundleIDs[getenv("__CFBundleIdentifier")]; ok {
		return Terminal{Name: name, Source: "from $__CFBundleIdentifier"}
	}
	for _, v := range []struct{ env, name string }{
		{"GHOSTTY_RESOURCES_DIR", "ghostty"},
		{"WEZTERM_EXECUTABLE", "wezterm"},
		{"KITTY_WINDOW_ID", "kitty"},
		{"WT_SESSION", "windows-terminal"},
	} {
		if getenv(v.env) != "" {
			return Terminal{Name: v.name, Source: "from $" + v.env}
		}
	}
	if t := getenv("TERMINAL"); t != "" {
		return Terminal{Command: []string{t, "-e", config.Placeholder}, Source: "from $TERMINAL"}
	}
	switch goos {
	case "darwin":
		return Terminal{Name: "terminal-app", Source: "fallback"}
	case "windows":
		if _, err := lookPath("wt.exe"); err == nil {
			return Terminal{Name: "windows-terminal", Source: "fallback"}
		}
		return Terminal{Name: "cmd", Source: "fallback"}
	}
	if _, err := lookPath("x-terminal-emulator"); err == nil {
		return Terminal{Name: "x-terminal-emulator", Source: "fallback"}
	}
	return Terminal{Source: "none found"}
}

// ResolveTerminal applies the terminal key: auto detects, anything else is taken as is.
func ResolveTerminal(t config.Terminal, goos string, getenv func(string) string, lookPath func(string) (string, error)) Terminal {
	switch {
	case t.Command != nil:
		return Terminal{Command: t.Command, Source: "config"}
	case t.Name == "auto" || t.Name == "":
		return DetectTerminal(goos, getenv, lookPath)
	}
	return Terminal{Name: t.Name, Source: "config"}
}

// expand puts cmd where {cmd} is: as separate words for a {cmd} element,
// shell-quoted inside a longer one.
func expand(template, cmd []string) []string {
	var out []string
	for _, w := range template {
		switch {
		case w == config.Placeholder:
			out = append(out, cmd...)
		case strings.Contains(w, config.Placeholder):
			out = append(out, strings.ReplaceAll(w, config.Placeholder, shell.Join(cmd...)))
		default:
			out = append(out, w)
		}
	}
	return out
}

// OpenCommand is the command that opens a new terminal window running cmd.
// script is an executable file that runs cmd, for apps that open files
// (Terminal.app, iTerm2); title names the window where the app allows it.
func (t Terminal) OpenCommand(goos, script, title string, cmd []string) []string {
	if t.Command != nil {
		return expand(t.Command, cmd)
	}
	mac := goos == "darwin"
	switch t.Name {
	case "terminal-app":
		// -g: no focus steal, so keystrokes meant for another app can't leak
		// into the new window
		return []string{"open", "-g", "-a", "Terminal", script}
	case "iterm":
		return []string{"open", "-g", "-a", "iTerm", script}
	case "ghostty":
		if mac {
			return append([]string{"open", "-g", "-n", "-a", "Ghostty", "--args", "-e"}, cmd...)
		}
		return append([]string{"ghostty", "-e"}, cmd...)
	case "wezterm":
		if mac {
			return append([]string{"open", "-g", "-n", "-a", "WezTerm", "--args", "start", "--"}, cmd...)
		}
		return append([]string{"wezterm", "start", "--"}, cmd...)
	case "kitty":
		if mac {
			return append([]string{"open", "-g", "-n", "-a", "kitty", "--args"}, cmd...)
		}
		return append([]string{"kitty", "--title", title}, cmd...)
	case "windows-terminal":
		return append([]string{"wt.exe", "-w", "new", "new-tab", "--title", title}, cmd...)
	case "cmd":
		return append([]string{"cmd", "/c", "start", title}, cmd...)
	case "x-terminal-emulator":
		return append([]string{"x-terminal-emulator", "-e"}, cmd...)
	}
	return nil
}
