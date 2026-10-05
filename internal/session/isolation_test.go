package session

import (
	"context"
	"debug/elf"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/proc"
)

func linuxGuard(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runtime")
	raw := make([]byte, 64)
	copy(raw, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(raw[16:], uint16(elf.ET_EXEC))
	machine := elf.EM_X86_64
	if runtime.GOARCH == "arm64" {
		machine = elf.EM_AARCH64
	}
	binary.LittleEndian.PutUint16(raw[18:], uint16(machine))
	binary.LittleEndian.PutUint32(raw[20:], 1)
	binary.LittleEndian.PutUint16(raw[52:], 64)
	require.NoError(t, os.WriteFile(path, raw, 0o700))
	return path
}

func TestIsolationRefusesUnavailableRuntime(t *testing.T) {
	l, c := newLauncher(t, "mode: autonomous\nisolation: {enabled: true}\n")
	l.Run = func(ctx context.Context, cmd proc.Cmd) (proc.Result, error) {
		if cmd.Args[0] == "/bin/sbx" {
			return proc.Result{}, errors.New("not authenticated")
		}
		return c.run(ctx, cmd)
	}
	require.False(t, l.Launch(t.Context(), Request{Repo: "o/r", N: 1, PR: pr("me")}))
	require.Empty(t, c.args, "no checkout, host agent or terminal after preflight failure")
}

func TestIsolationUsesPrivateCloneWithReadOnlyInputs(t *testing.T) {
	l, _ := newLauncher(t, "mode: autonomous\npush: never\ngithub_writes: never\nisolation: {enabled: true, read_only: [/reference docs]}\n")
	l.Cfg.Agent = "codex"
	l.CodexHome = t.TempDir()
	l.Cfg.Isolation.GuardBinary = new(linuxGuard(t))
	got := l.launched(t, "me")
	require.NotNil(t, got.spec.Isolation)
	require.Equal(t, "never", got.policy["push"])
	require.Contains(t, got.prompt, "OWN PR")
	require.Contains(t, got.spec.Agent, "--dangerously-bypass-approvals-and-sandbox")
	require.Empty(t, got.spec.Path)
	args := isolatedCreateArgs(got.spec)
	require.Contains(t, args, "--clone")
	require.Contains(t, args, "off")
	require.Contains(t, args, "/reference docs:ro")
	require.Contains(t, args, got.spec.Isolation.Inputs+":ro")
	require.NotContains(t, args, "--privileged")
	require.NotContains(t, strings.Join(args, " "), "docker.sock")
	raw, err := os.ReadFile(filepath.Join(got.spec.Isolation.Inputs, "prompt.txt"))
	require.NoError(t, err)
	require.Contains(t, string(raw), isolatedHome+"/policy.json")
	require.NotContains(t, string(raw), l.Root)
	_, err = os.Stat(filepath.Join(l.Root, "worktrees"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestIsolationInputsExcludeHistoryAndWatcherSecrets(t *testing.T) {
	l, _ := newLauncher(t, "mode: autonomous\n")
	l.Cfg.Agent = "codex"
	home := t.TempDir()
	source := filepath.Join(home, ".codex")
	require.NoError(t, os.MkdirAll(source, 0o700))
	for _, name := range []string{"auth.json", "config.toml", "AGENTS.md", "history.jsonl", "watcher-env.json"} {
		require.NoError(t, os.WriteFile(filepath.Join(source, name), []byte("test fixture"), 0o600))
	}
	input := filepath.Join(t.TempDir(), "inputs")
	require.NoError(t, l.isolationInputs(t.Context(), l.Cfg, t.TempDir(), input, home))
	entries, err := os.ReadDir(filepath.Join(input, "agent"))
	require.NoError(t, err)
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	require.Equal(t, []string{"AGENTS.md", "auth.json", "config.toml"}, names)
	st, err := os.Stat(filepath.Join(input, "agent", "auth.json"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	require.NoError(t, os.Symlink(filepath.Join(source, "history.jsonl"), filepath.Join(source, "settings-link")))
	require.ErrorContains(t, snapshotFile(filepath.Join(source, "settings-link"), filepath.Join(input, "escaped")), "regular file")
}

func TestIsolationKeychainCredentialNeverEntersMetadata(t *testing.T) {
	l, _ := newLauncher(t, "mode: autonomous\n")
	l.GOOS = "darwin"
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude"), 0o700))
	secret := `{"claudeAiOauth":{"accessToken":"test-secret","refreshToken":"test-refresh"}}`
	l.Run = func(_ context.Context, cmd proc.Cmd) (proc.Result, error) {
		if cmd.Args[0] == "/usr/bin/security" {
			return proc.Result{Stdout: secret}, nil
		}
		return proc.Result{}, errors.New("not set")
	}
	input := t.TempDir()
	require.NoError(t, l.isolationInputs(t.Context(), l.Cfg, t.TempDir(), input, home))
	raw, err := os.ReadFile(filepath.Join(input, "agent", ".credentials.json"))
	require.NoError(t, err)
	require.JSONEq(t, secret, string(raw))
	metadata, err := json.Marshal(IsolatedSpec{Name: "test", Inputs: input, Agent: "claude"})
	require.NoError(t, err)
	require.NotContains(t, string(metadata), "test-secret")
}

func TestIsolationGuardRejectsHostExecutable(t *testing.T) {
	require.NoError(t, checkLinuxGuard(linuxGuard(t)))
	host := filepath.Join(t.TempDir(), "runtime")
	require.NoError(t, os.WriteFile(host, []byte("not a Linux executable"), 0o700))
	require.ErrorContains(t, checkLinuxGuard(host), "Linux Outrider binary")
	cfg := config.Default()
	require.Equal(t, "/app/outrider-linux-runtime", isolationGuard(&cfg, "/app/outrider"))
}

// Exercise the complete host runner using a fake sbx executable: failures must
// never launch the native agent; successful setup must carry only selected env.
func TestIsolationRunnerLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake CLI")
	}
	for _, failAt := range []string{"create", "secret", ""} {
		t.Run("failure-"+failAt, func(t *testing.T) {
			dir := t.TempDir()
			cli := filepath.Join(dir, "sbx")
			log := filepath.Join(dir, "calls")
			script := `#!/bin/sh
printf '%s\n' "$1" >> "$OUTRIDER_TEST_SBX_LOG"
if [ "$1" = "$OUTRIDER_TEST_SBX_FAIL" ]; then exit 1; fi
if [ "$1" = exec ]; then
 for arg in "$@"; do
  case "$arg" in 'command -v gh;'*) printf '/usr/bin/gh\n/usr/bin/git\n/usr/bin:/bin\n';; esac
 done
fi
exit 0
`
			require.NoError(t, os.WriteFile(cli, []byte(script), 0o700))
			t.Setenv("OUTRIDER_TEST_SBX_LOG", log)
			t.Setenv("OUTRIDER_TEST_SBX_FAIL", failAt)
			t.Setenv("OUTRIDER_TEST_UNRELATED_SECRET", "must-not-be-forwarded")
			require.NoError(t, os.WriteFile(filepath.Join(dir, "policy.json"), []byte(`{"deny_rules":[]}`), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "prompt.txt"), []byte("fixture prompt"), 0o600))
			spec := Spec{Isolation: &IsolatedSpec{Name: "test-isolation", Inputs: dir, Guard: filepath.Join(dir, "linux"), Agent: "claude", NetworkAccess: new(false)},
				Meta: LockMeta{Repo: "o/r", PR: 1}, Dir: dir, PromptFile: filepath.Join(dir, "prompt.txt"),
				Agent: []string{cli, "run", "--name", "test-isolation", "--", "--dangerously-skip-permissions"}, Env: map[string]string{}}
			var output strings.Builder
			status := runIsolated(spec, nil, strings.NewReader("\n"), &output)
			if failAt == "" {
				require.Zero(t, status)
			} else {
				require.Equal(t, 1, status)
			}
			raw, err := os.ReadFile(log)
			require.NoError(t, err)
			commands := strings.Split(strings.TrimSpace(string(raw)), "\n")
			require.Equal(t, "create", commands[0])
			if failAt != "" {
				require.NotContains(t, commands, "run")
			} else {
				require.Equal(t, "run", commands[len(commands)-1])
				require.Contains(t, output.String(), "sandbox retained")
			}
			require.NotContains(t, output.String(), "must-not-be-forwarded")
		})
	}
}

func TestIsolationTreeDoesNotReadSymlinkTargets(t *testing.T) {
	source := t.TempDir()
	outside := filepath.Join(t.TempDir(), "private")
	require.NoError(t, os.WriteFile(outside, []byte("unselected secret"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(source, "reference")))
	destination := filepath.Join(t.TempDir(), "snapshot")
	require.NoError(t, snapshotTree(source, destination))
	info, err := os.Lstat(filepath.Join(destination, "reference"))
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSymlink)
	target, err := os.Readlink(filepath.Join(destination, "reference"))
	require.NoError(t, err)
	require.Equal(t, outside, target)
}

// This opt-in check signs a disposable message, never a Git commit. It requires
// an existing test sandbox with socat and the host public signing key imported.
func TestIsolationLiveGPG(t *testing.T) {
	name := os.Getenv("OUTRIDER_LIVE_SANDBOX")
	if name == "" {
		t.Skip("set OUTRIDER_LIVE_SANDBOX for the real runtime check")
	}
	sbx, err := exec.LookPath("sbx")
	require.NoError(t, err)
	socket, err := exec.Command("gpgconf", "--list-dirs", "agent-extra-socket").Output()
	require.NoError(t, err)
	key, err := exec.Command("git", "config", "--get", "user.signingkey").Output()
	require.NoError(t, err)
	stop, err := forwardGPG(t.Context(), sbx, name, strings.TrimSpace(string(socket)), os.Environ())
	require.NoError(t, err)
	defer stop()
	for range 2 {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		cmd := exec.CommandContext(ctx, sbx, "exec", "-i", name, "gpg", "--batch", "--armor", "--detach-sign", "--local-user", strings.TrimSpace(string(key)))
		cmd.Stdin = strings.NewReader("Outrider sandbox signing verification\n")
		var stderr strings.Builder
		cmd.Stderr = &stderr
		raw, err := cmd.Output()
		cancel()
		require.NoError(t, err, "restricted GPG agent forwarding: %s", stderr.String())
		require.Contains(t, string(raw), "BEGIN PGP SIGNATURE")
	}
}
