package approve

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func platform(goos string, env map[string]string, files ...string) Platform {
	return Platform{
		GOOS:   goos,
		Getenv: func(k string) string { return env[k] },
		Exists: func(p string) bool {
			for _, f := range files {
				if f == p {
					return true
				}
			}
			return false
		},
	}
}

func TestMacOSUsesOsascript(t *testing.T) {
	r := require.New(t)
	d, err := platform("darwin", nil, "/usr/bin/osascript").dialogFor(`t "x"`, "body", "Push")
	r.NoError(err)
	r.Equal("/usr/bin/osascript", d.args[0])
	r.Contains(d.args[2], `with title (item 2 of argv)`)
	r.Contains(d.args[2], `buttons {"Deny", (item 3 of argv)} default button "Deny" cancel button "Deny"`)
	r.Equal([]string{"--", "body", `t "x"`, "Push"}, d.args[3:]) // texts are arguments only
	// security review F2: without "--" osascript reads a body starting with
	// "-" as its own option (-e adds script text)
	d, err = platform("darwin", nil, "/usr/bin/osascript").dialogFor("t", "-e do shell script \"x\"", "Push")
	r.NoError(err)
	r.Equal([]string{"--", "-e do shell script \"x\"", "t", "Push"}, d.args[3:])
	r.Contains(d.args[2], "giving up after 300")
	r.Contains(d.args[2], "on error number -128")
	for _, tc := range []struct {
		stdout string
		want   Decision
	}{
		{stdout: "Push\n", want: Approved},
		{stdout: "Deny\n", want: Denied},
		{stdout: "timeout\n", want: TimedOut},
	} {
		got, answerErr := d.answer(tc.stdout, 0)
		r.NoError(answerErr)
		r.Equal(tc.want, got)
	}
	_, err = d.answer("unexpected\n", 0)
	r.ErrorContains(err, "unexpected dialog response")
	_, err = platform("darwin", nil).dialogFor("t", "b", "Push")
	r.ErrorIs(err, ErrNoDialog)
}

func TestLinuxPrefersZenityThenKdialog(t *testing.T) {
	r := require.New(t)
	display := map[string]string{"WAYLAND_DISPLAY": "wayland-0"}
	d, err := platform("linux", display, "/usr/bin/zenity", "/usr/bin/kdialog").dialogFor("t", "b", "Post")
	r.NoError(err)
	r.Equal([]string{"/usr/bin/zenity", "--question", "--title=t", "--text=b", "--no-markup",
		"--ok-label=Post", "--cancel-label=Deny", "--timeout=300", "--width=600"}, d.args)
	d, err = platform("linux", map[string]string{"DISPLAY": ":0"}, "/usr/local/bin/kdialog").dialogFor("t", "b", "Post")
	r.NoError(err)
	r.Equal("/usr/local/bin/kdialog", d.args[0])
	r.Contains(d.args, "--yes-label")
	// security review: kdialog renders text that looks like HTML
	d, err = platform("linux", map[string]string{"DISPLAY": ":0"}, "/usr/bin/kdialog").dialogFor("t", "<b>gh pr view</b> & <!-- gh pr merge -->", "Post")
	r.NoError(err)
	r.Contains(d.args, "&lt;b&gt;gh pr view&lt;/b&gt; &amp; &lt;!-- gh pr merge --&gt;")
}

func TestLinuxWithoutDisplayOrToolDenies(t *testing.T) {
	_, err := platform("linux", nil, "/usr/bin/zenity").dialogFor("t", "b", "Post")
	require.ErrorIs(t, err, ErrNoDialog)
	_, err = platform("linux", map[string]string{"DISPLAY": ":0"}).dialogFor("t", "b", "Post")
	require.ErrorIs(t, err, ErrNoDialog)
	_, err = platform("freebsd", map[string]string{"DISPLAY": ":0"}).dialogFor("t", "b", "Post")
	require.ErrorIs(t, err, ErrNoDialog)
}

func TestWindowsUsesPowerShellMessageBox(t *testing.T) {
	r := require.New(t)
	ps := `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
	// security review: SystemRoot comes from the agent's environment
	_, err := platform("windows", map[string]string{"SystemRoot": `C:\Users\me\evil`}, `C:\Users\me\evil\System32\WindowsPowerShell\v1.0\powershell.exe`).dialogFor("t", "b", "Push")
	r.ErrorIs(err, ErrNoDialog)
	d, err := platform("windows", map[string]string{"SystemRoot": `C:\Users\me\evil`}, ps).dialogFor("t", "b'; evil", "Push")
	r.NoError(err)
	r.Equal(ps, d.args[0])
	r.NotContains(d.args[len(d.args)-1], "evil") // texts only through the environment
	r.Contains(d.env, "OUTRIDER_DIALOG_TITLE=t")
	decision, answerErr := d.answer("Yes\r\n", 0)
	r.NoError(answerErr)
	r.Equal(Approved, decision)
	decision, answerErr = d.answer("Deny\r\n", 0)
	r.NoError(answerErr)
	r.Equal(Denied, decision)
	_, answerErr = d.answer("", 0)
	r.ErrorContains(answerErr, "unexpected dialog response")
}

func TestBackend(t *testing.T) {
	r := require.New(t)
	got, err := platform("darwin", nil, "/usr/bin/osascript").Backend()
	r.NoError(err)
	r.Equal("/usr/bin/osascript", got)
	got, err = platform("linux", map[string]string{"DISPLAY": ":0"}, "/usr/bin/kdialog").Backend()
	r.NoError(err)
	r.Equal("/usr/bin/kdialog", got)
	_, err = platform("linux", nil, "/usr/bin/zenity").Backend()
	r.ErrorIs(err, ErrNoDialog)
}

func TestLinuxDialogExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		zenity   bool
		exitCode int
		want     Decision
		wantErr  bool
	}{
		{name: "approve", zenity: true, want: Approved},
		{name: "deny", zenity: true, exitCode: 1, want: Denied},
		{name: "timeout", zenity: true, exitCode: 5, want: TimedOut},
		{name: "backend failure", zenity: true, exitCode: 3, wantErr: true},
		{name: "kdialog deny", exitCode: 1, want: Denied},
		{name: "kdialog failure", exitCode: 5, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := linuxAnswer(tc.zenity)("", tc.exitCode)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
			}
		})
	}
}

func TestDialogProcess(t *testing.T) {
	if os.Getenv("OUTRIDER_TEST_DIALOG") == "" {
		return
	}
	switch os.Getenv("OUTRIDER_TEST_DIALOG") {
	case "approve":
		fmt.Println("Push")
	case "deny":
		fmt.Println("Deny")
	case "unexpected":
		fmt.Println("surprise")
	case "failure":
		fmt.Fprintln(os.Stderr, "backend failed: secret body")
		os.Exit(42)
	case "linux deny":
		os.Exit(1)
	case "linux failure":
		fmt.Fprintln(os.Stderr, "backend failed: secret body")
		os.Exit(1)
	case "timeout":
		time.Sleep(time.Second)
	}
	os.Exit(0)
}

func TestRunDialogOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		want      Decision
		wantError string
	}{
		{name: "approve", want: Approved},
		{name: "deny", want: Denied},
		{name: "unexpected", wantError: "unexpected dialog response"},
		{name: "failure", wantError: "backend failed: [redacted]"},
		{name: "timeout", want: TimedOut},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OUTRIDER_TEST_DIALOG", tc.name)
			ctx := context.Background()
			if tc.name == "timeout" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 10*time.Millisecond)
				defer cancel()
			}
			d := dialog{args: []string{os.Args[0], "-test.run=TestDialogProcess"}, answer: namedAnswer("Push")}
			got, err := run(ctx, d, "title", "secret body", "Push")
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				require.NotContains(t, err.Error(), "secret body")
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
			}
		})
	}
	d := dialog{args: []string{"/outrider/missing/dialog"}, answer: namedAnswer("Push")}
	_, err := run(context.Background(), d, "title", "body", "Push")
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "missing/dialog") || strings.Contains(err.Error(), "no such file"))
	for _, tc := range []struct {
		name      string
		want      Decision
		wantError string
	}{
		{name: "linux deny", want: Denied},
		{name: "linux failure", wantError: "backend failed: [redacted]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OUTRIDER_TEST_DIALOG", tc.name)
			d := dialog{args: []string{os.Args[0], "-test.run=TestDialogProcess"}, answer: linuxAnswer(true)}
			got, err := run(context.Background(), d, "title", "secret body", "Push")
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
			}
		})
	}
}
