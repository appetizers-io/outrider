package approve

import (
	"testing"

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
	r.Contains(d.args[2], `with title "t \"x\""`)
	r.Contains(d.args[2], `buttons {"Deny", "Push"} default button "Deny" cancel button "Deny"`)
	r.Contains(d.args[2], "giving up after 300")
	r.Equal("body", d.args[3]) // the text is an argument, never part of the script
	r.True(d.ok("Push\n"))
	r.False(d.ok("timeout\n"))
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
	d, err := platform("windows", map[string]string{"SystemRoot": `C:\Windows`}, ps).dialogFor("t", "b'; evil", "Push")
	r.NoError(err)
	r.Equal(ps, d.args[0])
	r.NotContains(d.args[len(d.args)-1], "evil") // texts only through the environment
	r.Contains(d.env, "LLM_REVIEW_AGENT_DIALOG_TITLE=t")
	r.True(d.ok("Yes\r\n"))
	r.False(d.ok(""))
}
