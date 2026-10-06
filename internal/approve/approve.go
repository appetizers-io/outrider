// Package approve asks the owner in a native dialog: osascript on macOS,
// zenity or kdialog on a Linux desktop, a PowerShell message box on Windows.
// Without a dialog it refuses. A dialog left alone times out after 5 minutes.
//
// Dialog tools are only taken from fixed system locations, never from PATH:
// the guards run with the agent's environment, and a fake `zenity` early on
// PATH must not be able to approve on the owner's behalf.
package approve

import (
	"context"
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"
)

// Timeout is how long a dialog waits for the owner.
const Timeout = 300 * time.Second

// ErrNoDialog means no dialog can be shown here.
var ErrNoDialog = errors.New("no approval dialog available")

// Decision is the response positively established by the native dialog.
type Decision uint8

// Decision values include an unknown result that never permits an operation.
const (
	Unknown Decision = iota
	Approved
	Denied
	TimedOut
)

// dialog is a command whose success means the owner clicked ok.
type dialog struct {
	args   []string
	env    []string // extra environment
	answer func(stdout string, exitCode int) (Decision, error)
}

func namedAnswer(ok string) func(string, int) (Decision, error) {
	return func(stdout string, exitCode int) (Decision, error) {
		if exitCode != 0 {
			return Unknown, fmt.Errorf("unexpected exit code %d", exitCode)
		}
		switch strings.TrimSpace(stdout) {
		case ok:
			return Approved, nil
		case "Deny":
			return Denied, nil
		case "timeout":
			return TimedOut, nil
		default:
			return Unknown, errors.New("unexpected dialog response")
		}
	}
}

// Platform describes where a dialog would run.
type Platform struct {
	GOOS   string
	Getenv func(string) string
	Exists func(path string) bool
}

func exists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// Host is the platform this process runs on.
func Host() Platform { return Platform{GOOS: goos, Getenv: os.Getenv, Exists: exists} }

const osascriptPath = "/usr/bin/osascript"

var linuxDirs = []string{"/usr/bin", "/bin", "/usr/local/bin"}

func (p Platform) find(name string) string {
	for _, d := range linuxDirs {
		// slash paths: these are Linux locations, whatever builds the test
		if candidate := path.Join(d, name); p.Exists(candidate) {
			return candidate
		}
	}
	return ""
}

// dialogFor builds the dialog command, or ErrNoDialog.
func (p Platform) dialogFor(title, body, ok string) (dialog, error) {
	switch p.GOOS {
	case "darwin":
		if !p.Exists(osascriptPath) {
			return dialog{}, ErrNoDialog
		}
		// every text is an argument, never part of the script
		script := "on run argv\ntry\n" +
			"display dialog (item 1 of argv) with title (item 2 of argv)" +
			" buttons {\"Deny\", (item 3 of argv)} default button \"Deny\" cancel button \"Deny\"" +
			" with icon caution giving up after 300\n" +
			"if gave up of result then return \"timeout\"\n" +
			"return button returned of result\n" +
			"on error number -128\nreturn \"Deny\"\nend try\nend run"
		return dialog{
			args:   []string{osascriptPath, "-e", script, "--", body, title, ok}, // "--": a body starting with "-" is no option
			answer: namedAnswer(ok),
		}, nil
	case "windows":
		// a fixed path: SystemRoot comes from the agent's environment
		const ps = `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
		if !p.Exists(ps) {
			return dialog{}, ErrNoDialog
		}
		// the texts travel in the environment: no quoting into the script
		script := "Add-Type -AssemblyName PresentationFramework; " +
			"$r = [System.Windows.MessageBox]::Show($env:OUTRIDER_DIALOG_BODY, $env:OUTRIDER_DIALOG_TITLE, 'YesNo', 'Warning', 'No'); " +
			"if ($r -eq 'Yes') { 'Yes' } else { 'Deny' }"
		return dialog{
			args: []string{ps, "-NoProfile", "-NonInteractive", "-Command", script},
			env: []string{
				"OUTRIDER_DIALOG_TITLE=" + title,
				"OUTRIDER_DIALOG_BODY=" + body + "\n\nYes: " + ok + "    No: Deny",
			},
			answer: namedAnswer("Yes"),
		}, nil
	default:
		if p.Getenv("DISPLAY") == "" && p.Getenv("WAYLAND_DISPLAY") == "" {
			return dialog{}, ErrNoDialog
		}
		if z := p.find("zenity"); z != "" {
			return dialog{
				args: []string{z, "--question", "--title=" + title, "--text=" + body, "--no-markup",
					"--ok-label=" + ok, "--cancel-label=Deny", "--timeout=300", "--width=600"},
				answer: linuxAnswer(true),
			}, nil
		}
		if k := p.find("kdialog"); k != "" {
			return dialog{
				// kdialog renders text that looks like HTML; show it as written
				args:   []string{k, "--title", title, "--warningyesno", html.EscapeString(body), "--yes-label", ok, "--no-label", "Deny"},
				answer: linuxAnswer(false),
			}, nil
		}
		return dialog{}, ErrNoDialog
	}
}

func linuxAnswer(zenity bool) func(string, int) (Decision, error) {
	return func(_ string, exitCode int) (Decision, error) {
		switch exitCode {
		case 0:
			return Approved, nil
		case 1:
			return Denied, nil
		case 5:
			if zenity {
				return TimedOut, nil
			}
		}
		return Unknown, fmt.Errorf("unexpected exit code %d", exitCode)
	}
}

// Backend is the dialog tool that Ask would run here, or ErrNoDialog.
func (p Platform) Backend() (string, error) {
	d, err := p.dialogFor("", "", "")
	if err != nil {
		return "", err
	}
	return d.args[0], nil
}

// Ask shows the dialog. Only Approved permits the guarded operation.
func Ask(ctx context.Context, title, body, ok string) (Decision, error) {
	d, err := Host().dialogFor(title, body, ok)
	if err != nil {
		return Unknown, err
	}
	return run(ctx, d, title, body, ok)
}

func run(ctx context.Context, d dialog, title, body, ok string) (Decision, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout+10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, d.args[0], d.args[1:]...)
	cmd.Env = append(os.Environ(), d.env...)
	out, err := cmd.Output()
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return TimedOut, nil
		}
		return Unknown, ctx.Err()
	}
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return Unknown, fmt.Errorf("%s: %w", path.Base(d.args[0]), err)
		}
		exitCode = exitErr.ExitCode()
		if exitCode == 1 && len(exitErr.Stderr) != 0 {
			return Unknown, fmt.Errorf("%s: exit code %d; stderr: %s", path.Base(d.args[0]), exitCode,
				redactStderr(string(exitErr.Stderr), title, body, ok))
		}
	}
	decision, answerErr := d.answer(string(out), exitCode)
	if answerErr == nil {
		return decision, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) != 0 {
		return Unknown, fmt.Errorf("%s: %w; stderr: %s", path.Base(d.args[0]), answerErr,
			redactStderr(string(exitErr.Stderr), title, body, ok))
	}
	return Unknown, fmt.Errorf("%s: %w", path.Base(d.args[0]), answerErr)
}

func redactStderr(stderr, title, body, ok string) string {
	for _, value := range append([]string{title, ok}, strings.Split(body, "\n")...) {
		if len(value) >= 3 {
			stderr = strings.ReplaceAll(stderr, value, "[redacted]")
		}
	}
	stderr = strings.Join(strings.Fields(stderr), " ")
	if len(stderr) > 400 {
		stderr = stderr[:400] + "..."
	}
	return stderr
}
