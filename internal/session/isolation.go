package session

import (
	"bufio"
	"context"
	"crypto/rand"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/guard"
	"github.com/appetizers-io/outrider/internal/proc"
	"github.com/appetizers-io/outrider/internal/shell"
)

// IsolatedSpec identifies a Docker Sandboxes microVM and its read-only inputs.
type IsolatedSpec struct {
	Name          string   `json:"name"`
	Inputs        string   `json:"inputs"`
	Guard         string   `json:"guard"`
	Agent         string   `json:"agent"`
	GitHub        string   `json:"github"`
	GPGSocket     *string  `json:"gpg_socket,omitempty"`
	ReadOnly      []string `json:"read_only"`
	Environment   []string `json:"environment,omitempty"`
	NetworkAccess *bool    `json:"network_access,omitempty"`
}

const isolatedHome = "/tmp/outrider"

// IsolationSupport checks runtime authentication and the portable session guards.
// It never launches an agent or falls back to a host session.
func IsolationSupport(ctx context.Context, cfg *config.Config, self string, lookPath func(string) (string, error), run proc.Runner) error {
	sbx, err := lookPath("sbx")
	if err != nil {
		return errors.New("docker Sandboxes is not installed; install sbx and run sbx login")
	}
	if _, err := run(ctx, proc.Cmd{Args: []string{sbx, "ls", "--json"}}); err != nil {
		return fmt.Errorf("docker Sandboxes unavailable (run sbx login): %w", err)
	}
	if _, err := run(ctx, proc.Cmd{Args: []string{sbx, "policy", "ls", "--json"}}); err != nil {
		return fmt.Errorf("sandbox network policy unavailable (run sbx policy init balanced for a new installation): %w", err)
	}
	return checkLinuxGuard(isolationGuard(cfg, self))
}

func isolationGuard(cfg *config.Config, self string) string {
	if cfg.Isolation.GuardBinary != nil {
		return *cfg.Isolation.GuardBinary
	}
	return filepath.Join(filepath.Dir(self), "outrider-linux-runtime")
}

func checkLinuxGuard(path string) error {
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("isolation needs a Linux Outrider binary at %s (task build/install): %w", path, err)
	}
	defer func() { _ = f.Close() }()
	machine := elf.EM_X86_64
	if runtime.GOARCH == "arm64" {
		machine = elf.EM_AARCH64
	}
	if f.Machine != machine {
		return errors.New("isolation.guard_binary has the wrong architecture")
	}
	return nil
}

func (l *Launcher) startIsolated(ctx context.Context, r Request, lock string, cfg *config.Config) error {
	if err := IsolationSupport(ctx, cfg, l.Self, l.LookPath, l.Run); err != nil {
		return err
	}
	source := filepath.Join(l.Root, "repos", strings.ReplaceAll(r.Repo, "/", "__"))
	if loc, ok := l.Local[r.Repo]; ok {
		source = loc.Path
		// Clone the primary repository, not a linked host worktree whose .git
		// pointer would refer to a directory outside the runtime mount.
		res, err := l.Run(ctx, proc.Cmd{Args: []string{"git", "rev-parse", "--path-format=absolute", "--git-common-dir"}, Dir: source})
		if err != nil {
			return fmt.Errorf("isolation repository: %w", err)
		}
		if common := strings.TrimSpace(res.Stdout); common != "" {
			source = filepath.Dir(common)
		}
	}
	if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
			return err
		}
		if _, err := l.Run(ctx, proc.Cmd{Args: []string{"gh", "repo", "clone", r.Repo, source, "--", "--filter=blob:none"}, Timeout: 30 * time.Minute}); err != nil {
			return err
		}
	}
	// Generate the same PR policy and guards for Linux, without a host worktree.
	portable := *l
	portable.GOOS = "linux"
	portable.LookPath = func(name string) (string, error) { return name, nil }
	review, err := l.review(r, cfg, sessionDir(l.Root, r.Repo, r.N), false, source)
	if err != nil {
		return err
	}
	if err := l.reviewContext(ctx, r, cfg, review, source, sessionDir(l.Root, r.Repo, r.N)); err != nil {
		return err
	}
	p, err := portable.Prepare(r, source, lock, nil)
	if err != nil {
		return err
	}
	sbx, err := l.LookPath("sbx")
	if err != nil {
		return err
	}
	name := fmt.Sprintf("outrider-pr-%d-%s", r.N, strings.ToLower(rand.Text()))
	input := filepath.Join(p.Dir, "isolated-inputs", name)
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if err := l.isolationInputs(ctx, cfg, source, input, home); err != nil {
		return err
	}
	for _, file := range []string{"policy.json", "prompt.txt"} {
		raw, err := os.ReadFile(filepath.Join(p.Dir, file))
		if err != nil {
			return err
		}
		if file == "prompt.txt" {
			raw = []byte(isolatedPath(string(raw), p.Dir))
		}
		if file == "policy.json" {
			var policy Policy
			if err := json.Unmarshal(raw, &policy); err != nil {
				return err
			}
			policy.Sandbox = "docker-sandbox"
			policy.Guard.Git, policy.Guard.GH, policy.Guard.SSH = "/usr/bin/git", "/usr/bin/gh", "/usr/bin/ssh"
			policy.Guard.Display = map[string]string{}
			if policy.Review != nil {
				policy.Review.Context = isolatedPath(policy.Review.Context, p.Dir)
				policy.Review.Outbox = isolatedPath(policy.Review.Outbox, p.Dir)
			}
			if policy.Config != nil {
				*policy.Config = isolatedPath(*policy.Config, p.Dir)
			}
			if policy.PRContext != nil {
				*policy.PRContext = isolatedPath(*policy.PRContext, p.Dir)
			}
			policy.ToolGate.Rules = isolatedPath(policy.ToolGate.Rules, p.Dir)
			for i := range policy.DenyRules {
				policy.DenyRules[i] = isolatedPath(policy.DenyRules[i], p.Dir)
			}
			raw, err = json.MarshalIndent(policy, "", "  ")
			if err != nil {
				return err
			}
		}
		if err := os.WriteFile(filepath.Join(input, file), raw, 0o600); err != nil {
			return err
		}
	}
	if p.Spec.Review != nil && p.Spec.Review.Outbox != "" {
		if err := os.MkdirAll(filepath.Join(input, "outbox"), 0o700); err != nil {
			return err
		}
	}
	if p.Spec.Review != nil && p.Spec.Review.Context != "" {
		if err := snapshotTree(p.Spec.Review.Context, filepath.Join(input, "review-context")); err != nil {
			return err
		}
	}
	// All session references resolve inside the VM, never to writable host metadata.
	for k, v := range p.Spec.Env {
		p.Spec.Env[k] = isolatedPath(v, p.Dir)
	}
	p.Spec.Env[guard.EnvRealGH], p.Spec.Env[guard.EnvRealGit] = "/usr/bin/gh", "/usr/bin/git"
	p.Spec.Isolation = &IsolatedSpec{Name: name, Inputs: input, Guard: isolationGuard(cfg, l.Self), Agent: cfg.Agent, ReadOnly: cfg.Isolation.ReadOnly, NetworkAccess: cfg.NetworkAccess}
	hostGH, err := l.LookPath("gh")
	if err != nil {
		return err
	}
	p.Spec.Isolation.GitHub = hostGH
	if raw, err := os.ReadFile(filepath.Join(input, "gpg-extra-socket")); err == nil {
		p.Spec.Isolation.GPGSocket = new(strings.TrimSpace(string(raw)))
	}

	for _, key := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "OPENAI_API_KEY"} {
		if os.Getenv(key) != "" {
			p.Spec.Isolation.Environment = append(p.Spec.Isolation.Environment, key)
		}
	}

	// Agent arguments retain native settings and PR rules; only tool permission
	// prompts are bypassed. The runtime, not the agent, enforces host isolation.
	agent := []string{cfg.Agent}
	if cfg.Agent == "claude" {
		agent = append(agent, "--dangerously-skip-permissions", "--settings", isolatedHome+"/claude-settings.json")
	} else {
		agent = append(agent, "--dangerously-bypass-approvals-and-sandbox", "-c", `cli_auth_credentials_store="file"`)
	}
	p.Spec.Agent = append([]string{sbx, "run", "--name", name, "--"}, agent[1:]...)
	p.Spec.PromptFile = filepath.Join(input, "prompt.txt")
	p.Spec.Path = "" // host guard PATH must not wrap sbx's host-side Git operations
	p.Spec.Header = append(p.Spec.Header, "Docker Sandboxes microVM: "+name+"; host inputs read-only; changes remain in the private clone")
	if err := writeJSON(filepath.Join(p.Dir, "session.json"), p.Spec); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		return err
	}
	if err := writeJSON(lock, p.Spec.Meta); err != nil {
		return err
	}
	if err := l.open(ctx, r, p); err != nil {
		_ = os.Remove(lock)
		return err
	}
	return nil
}

// snapshotFile rejects symlinks: importing a setting must not follow a link to
// unrelated host data. Missing optional files are simply omitted.
func snapshotFile(src, dst string) error {
	st, err := os.Lstat(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("isolation input %s must be a regular file", src)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}

func snapshotTree(src, dst string) error {
	source, err := os.OpenRoot(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	target, err := os.OpenRoot(dst)
	if err != nil {
		return err
	}
	defer func() { _ = target.Close() }()
	return fs.WalkDir(source.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return target.MkdirAll(path, 0o700)
		}
		if d.Type()&os.ModeSymlink != 0 {
			link, err := source.Readlink(path)
			if err != nil {
				return err
			}
			return target.Symlink(link, path) // preserve without reading its host target
		}
		file, err := source.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		st, err := file.Stat()
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("isolation input %s must be a regular file", path)
		}
		data, err := io.ReadAll(file)
		if err != nil {
			return err
		}
		return target.WriteFile(path, data, 0o600)
	})
}

func (l *Launcher) isolationInputs(ctx context.Context, cfg *config.Config, source, input, home string) error {
	if err := os.MkdirAll(filepath.Join(input, "agent"), 0o700); err != nil {
		return err
	}
	agentDir := filepath.Join(home, "."+cfg.Agent)
	if cfg.Agent == "codex" && l.CodexHome != "" {
		agentDir = l.CodexHome
	}
	if cfg.Agent == "claude" && os.Getenv("CLAUDE_CONFIG_DIR") != "" {
		agentDir = os.Getenv("CLAUDE_CONFIG_DIR")
	}
	files := []string{"config.toml", "auth.json", "AGENTS.md"}
	if cfg.Agent == "claude" {
		files = []string{"settings.json", ".credentials.json", "CLAUDE.md"}
	}
	for _, file := range files {
		if err := snapshotFile(filepath.Join(agentDir, file), filepath.Join(input, "agent", file)); err != nil {
			return err
		}
	}
	for _, dir := range []string{"skills", "commands", "agents", "plugins"} {
		if err := snapshotTree(filepath.Join(agentDir, dir), filepath.Join(input, "agent", dir)); err != nil {
			return err
		}
	}
	if cfg.Agent == "claude" {
		if err := snapshotFile(filepath.Join(home, ".claude.json"), filepath.Join(input, "claude.json")); err != nil {
			return err
		}
		if l.GOOS == "darwin" && agentDir == filepath.Join(home, ".claude") {
			// Copy the existing subscription credential, not an API key. The Keychain
			// remains inaccessible to the VM; only this selected credential is shared.
			res, err := l.Run(ctx, proc.Cmd{Args: []string{"/usr/bin/security", "find-generic-password", "-s", "Claude Code-credentials", "-w"}})
			if err == nil {
				raw := []byte(strings.TrimSpace(res.Stdout))
				if !json.Valid(raw) {
					if decoded, e := hex.DecodeString(string(raw)); e == nil {
						raw = decoded
					}
				}
				if !json.Valid(raw) {
					return errors.New("claude Keychain credential is not valid JSON")
				}
				if err := os.MkdirAll(filepath.Join(input, "agent"), 0o700); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(input, "agent", ".credentials.json"), raw, 0o600); err != nil {
					return err
				}
			} else if _, err := os.Stat(filepath.Join(input, "agent", ".credentials.json")); err != nil {
				return errors.New("cannot reuse Claude login from Keychain; no file-backed login is available")
			}
		}
	}
	// Flatten portable Git identity/signing settings. Host credential helpers,
	// hooks and include paths are not executed or imported by the host bridge.
	gitSettings := map[string]string{}
	for _, key := range []string{"user.name", "user.email", "gpg.format", "user.signingkey", "commit.gpgsign", "tag.gpgsign"} {
		res, err := l.Run(ctx, proc.Cmd{Args: []string{"git", "config", "--get", key}, Dir: source})
		if err != nil || strings.TrimSpace(res.Stdout) == "" {
			continue
		}
		value := strings.TrimSpace(res.Stdout)
		gitSettings[key] = value
		if key == "user.signingkey" && filepath.IsAbs(value) {
			public := value
			if !strings.HasSuffix(public, ".pub") {
				public += ".pub"
			}
			if err := snapshotFile(public, filepath.Join(input, "signing.pub")); err != nil {
				return err
			}
			raw, err := os.ReadFile(filepath.Join(input, "signing.pub"))
			if err != nil {
				return fmt.Errorf("cannot import SSH signing public key %s: %w", public, err)
			}
			value = "key::" + strings.TrimSpace(string(raw))
		}
		if _, err := l.Run(ctx, proc.Cmd{Args: []string{"git", "config", "--file", filepath.Join(input, "gitconfig"), key, value}}); err != nil {
			return err
		}
	}
	if gitSettings["commit.gpgsign"] == "true" && (gitSettings["gpg.format"] == "" || gitSettings["gpg.format"] == "openpgp") {
		if err := l.prepareGPG(ctx, gitSettings["user.signingkey"], input); err != nil {
			return err
		}
	}

	return nil
}

const isolationBootstrap = `set -eu
umask 077
mkdir -p /tmp/outrider/bin "$HOME/.codex" "$HOME/.claude"
cp -R "$1"/. /tmp/outrider/
if [ -d /tmp/outrider/agent ]; then cp -R /tmp/outrider/agent/. "$HOME/.$2/"; fi
if [ -f /tmp/outrider/claude.json ]; then cp /tmp/outrider/claude.json "$HOME/.claude.json"; fi
if [ -f /tmp/outrider/gitconfig ]; then cp /tmp/outrider/gitconfig "$HOME/.gitconfig"; fi
sudo chmod 755 /tmp/outrider/outrider
cp /tmp/outrider/outrider /tmp/outrider/bin/git
cp /tmp/outrider/outrider /tmp/outrider/bin/gh
cp /tmp/outrider/outrider /tmp/outrider/bin/outrider-draft
command -v git >/dev/null
command -v gh >/dev/null

# Preserve SSH signing through the forwarded agent, using a public key instead
# of a host-only key path. Only the GPG public key is imported; the
# experimental signing bridge uses the restricted host agent socket.
if [ "$(git config --global --get gpg.format || true)" = ssh ]; then
 key=$(git config --global --get user.signingkey || true)
 case "$key" in key::*|ssh-*) ;; *)
  echo 'Configure an SSH public user.signingkey (key::ssh-ed25519 ...) on the host before signing in isolation' >&2
  exit 1
 esac
elif [ "$(git config --global --get commit.gpgsign || true)" = true ]; then
 test -f /tmp/outrider/gpg-public.asc
 gpg --batch --import /tmp/outrider/gpg-public.asc
 gpgconf --kill gpg-agent
 command -v socat >/dev/null || { sudo apt-get update -qq; sudo apt-get install -y -qq socat; }
fi
gh auth setup-git
# This checkout and any hooks/settings execute only inside the microVM.
gh pr checkout "$3" --repo "$4" --branch "review/pr-$3"
`

func isolatedCreateArgs(spec Spec) []string {
	cs := spec.Isolation
	args := []string{spec.Agent[0], "create", "--clone", "--skills", "off", "--name", cs.Name}

	args = append(args, cs.Agent, spec.Dir, cs.Inputs+":ro")
	for _, path := range cs.ReadOnly {
		args = append(args, path+":ro")
	}
	return args
}

func runIsolated(spec Spec, watcher []string, stdin io.Reader, stdout io.Writer) int {
	cs := spec.Isolation
	env := append(os.Environ(), watcher...)
	// Host environment stays on the host: only explicit session policy variables
	// are forwarded later. sbx itself receives SSH_AUTH_SOCK for its broker.
	run := func(args []string, input io.Reader) error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, input, stdout, stdout
		return cmd.Run()
	}
	sbx := spec.Agent[0]
	if err := run(isolatedCreateArgs(spec), nil); err != nil {
		_, _ = fmt.Fprintln(stdout, "outrider: sandbox creation failed; host agent will not be started")
		return 1
	}
	fail := func(err error) int {
		_, _ = fmt.Fprintf(stdout, "outrider: sandbox setup failed: %v\nretained sandbox: %s\n", err, cs.Name)
		return 1
	}
	// Use Docker's host credential proxy for GitHub. The token never enters an
	// environment variable, command argument, or Outrider session metadata.
	gh := cs.GitHub
	if gh == "" {
		var err error
		gh, err = exec.LookPath("gh")
		if err != nil {
			return fail(err)
		}
	}
	if err := run([]string{sbx, "secret", "set", "github", "--sandbox", cs.Name, "--command", shell.Join(gh, "auth", "token")}, nil); err != nil {
		return fail(err)
	}
	if err := run([]string{sbx, "policy", "allow", "network", "--sandbox", cs.Name, "github.com,api.github.com,ssh.github.com"}, nil); err != nil {
		return fail(err)
	}
	if cs.NetworkAccess != nil && *cs.NetworkAccess {
		if err := run([]string{sbx, "policy", "allow", "network", "--sandbox", cs.Name, "**"}, nil); err != nil {
			return fail(err)
		}
	}
	if err := run([]string{sbx, "policy", "deny", "network", "--sandbox", cs.Name, "localhost,**.localhost,host.docker.internal,gateway.docker.internal,127.0.0.0/8,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,::1/128,fc00::/7,fe80::/10"}, nil); err != nil {
		return fail(err)
	}
	if err := run([]string{sbx, "exec", cs.Name, "mkdir", "-p", isolatedHome}, nil); err != nil {
		return fail(err)
	}
	if err := run([]string{sbx, "cp", cs.Guard, cs.Name + ":" + isolatedHome + "/outrider"}, nil); err != nil {
		return fail(err)
	}
	if err := run([]string{sbx, "exec", cs.Name, "sh", "-c", isolationBootstrap, "outrider-init", cs.Inputs, cs.Agent, itoa(spec.Meta.PR), spec.Meta.Repo}, nil); err != nil {
		return fail(err)
	}
	if cs.GPGSocket != nil {
		gpgCtx, gpgCancel := context.WithCancel(context.Background())
		defer gpgCancel()
		stop, err := forwardGPG(gpgCtx, sbx, cs.Name, *cs.GPGSocket, env)
		if err != nil {
			return fail(err)
		}
		defer stop()
	}
	if cs.Agent == "claude" {
		// Keep Outrider's original deny rules, without a host-only tool gate.
		var policy Policy
		rawPolicy, err := os.ReadFile(filepath.Join(cs.Inputs, "policy.json"))
		if err != nil {
			return fail(err)
		}
		if err := json.Unmarshal(rawPolicy, &policy); err != nil {
			return fail(err)
		}
		settings := map[string]any{"skipDangerousModePermissionPrompt": true, "permissions": map[string]any{"deny": policy.DenyRules}}
		raw, _ := json.Marshal(settings)
		if err := run([]string{sbx, "exec", "-i", cs.Name, "sh", "-c", "cat > /tmp/outrider/claude-settings.json"}, strings.NewReader(string(raw))); err != nil {
			return fail(err)
		}
	}
	if cs.NetworkAccess != nil && !*cs.NetworkAccess {
		if err := run([]string{sbx, "policy", "deny", "network", "--sandbox", cs.Name, "**"}, nil); err != nil {
			return fail(err)
		}
	}
	pathsCtx, pathsCancel := context.WithTimeout(context.Background(), proc.DefaultTimeout)
	defer pathsCancel()
	pathsCmd := exec.CommandContext(pathsCtx, sbx, "exec", cs.Name, "sh", "-c", `command -v gh; command -v git; printf '%s\n' "$PATH"`)
	pathsCmd.Env = env
	pathsRaw, err := pathsCmd.Output()
	if err != nil {
		return fail(errors.New("cannot locate sandbox Git/GitHub tools"))
	}
	paths := strings.Split(strings.TrimSpace(string(pathsRaw)), "\n")
	if len(paths) != 3 || !strings.HasPrefix(paths[0], "/") || !strings.HasPrefix(paths[1], "/") {
		return fail(errors.New("invalid sandbox tool paths"))
	}
	spec.Env[guard.EnvRealGH], spec.Env[guard.EnvRealGit] = paths[0], paths[1]
	// A fresh VM home provides writable runtime state beside the copied config.
	args := []string{sbx, "run", "--name", cs.Name}
	for _, key := range cs.Environment {
		args = append(args, "--env", key)
	}
	for k, v := range spec.Env {
		args = append(args, "--env", k+"="+v)
	}
	args = append(args, "--env", "PATH="+isolatedHome+"/bin:"+paths[2], "--")
	args = append(args, spec.Agent[5:]...)
	prompt, err := os.ReadFile(spec.PromptFile)
	if err != nil {
		return fail(err)
	}
	args = append(args, string(prompt)+"\n\nThis session runs in a Docker Sandboxes microVM. Host inputs are read-only. Changes stay in the private clone. Keep the PR ownership and GitHub write rules above; never try to execute commands on the host.")
	for _, line := range spec.Header {
		_, _ = fmt.Fprintln(stdout, line)
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, os.Stdin, os.Stdout, os.Stderr
	status := 0
	timedOut, err := runAgent(cmd, reviewBudget(spec.Review), 15*time.Second, stdout)
	if err != nil {
		status = 1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			status = ee.ExitCode()
		}
	}
	if timedOut {
		_ = run([]string{sbx, "stop", cs.Name}, nil)
		status = 124
		_ = writeJSON(filepath.Join(filepath.Dir(spec.PromptFile), "ended.json"), map[string]any{"ended": "timeout", "status": status})
	}
	_ = os.Remove(spec.Lock)
	_, _ = fmt.Fprintf(stdout, "\nagent exited: %d\nsandbox retained: %s\ninspect with: sbx exec -it %s bash\npress Enter to close\n", status, cs.Name, cs.Name)
	_, _ = bufio.NewReader(stdin).ReadString('\n')
	return status
}

// prepareGPG exports only the selected public key. The restricted extra socket
// is a cryptographic capability; no GnuPG home or private-key file is shared.
func (l *Launcher) prepareGPG(ctx context.Context, key, input string) error {
	if key == "" || strings.HasPrefix(key, "-") {
		return errors.New("GPG isolation requires an explicit user.signingkey")
	}
	gpg, err := l.LookPath("gpg")
	if err != nil {
		return errors.New("gpg is required for the configured commit signing key")
	}
	gpgconf, err := l.LookPath("gpgconf")
	if err != nil {
		return errors.New("gpgconf is required for agent forwarding")
	}
	if _, err := l.Run(ctx, proc.Cmd{Args: []string{gpgconf, "--launch", "gpg-agent"}}); err != nil {
		return err
	}
	socket, err := l.Run(ctx, proc.Cmd{Args: []string{gpgconf, "--list-dirs", "agent-extra-socket"}})
	if err != nil {
		return err
	}
	path := strings.TrimSpace(socket.Stdout)
	if !filepath.IsAbs(path) {
		return errors.New("GPG restricted agent socket must be an absolute path")
	}
	public, err := l.Run(ctx, proc.Cmd{Args: []string{gpg, "--batch", "--armor", "--export", key}})
	if err != nil {
		return err
	}
	if !strings.Contains(public.Stdout, "BEGIN PGP PUBLIC KEY BLOCK") {
		return errors.New("configured GPG public signing key could not be exported")
	}
	if err := os.WriteFile(filepath.Join(input, "gpg-public.asc"), []byte(public.Stdout), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(input, "gpg-extra-socket"), []byte(path), 0o600)
}

// forwardGPG relays GnuPG's existing restricted protocol over sbx exec STDIO.
// The guest listener is local to the VM; no host port or general host command
// endpoint is exposed. Only the host's preselected extra socket is reachable.
func forwardGPG(parent context.Context, sbx, name, hostSocket string, env []string) (func(), error) {
	ctx, cancel := context.WithCancel(parent)
	query := exec.CommandContext(ctx, sbx, "exec", name, "gpgconf", "--list-dirs", "agent-socket")
	query.Env = env
	raw, err := query.Output()
	if err != nil {
		cancel()
		return nil, errors.New("cannot locate sandbox GPG agent socket")
	}
	remote := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(remote, "/") || strings.ContainsAny(remote, ",\r\n") {
		cancel()
		return nil, errors.New("invalid sandbox GPG socket")
	}
	done := make(chan struct{})
	failures := make(chan error, 1)
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			connection, err := net.Dial("unix", hostSocket)
			if err != nil {
				select {
				case failures <- err:
				default:
				}
				return
			}
			cmd := exec.CommandContext(ctx, sbx, "exec", "-i", name, "socat", "STDIO", "UNIX-LISTEN:"+remote+",unlink-early,mode=0600")
			cmd.Env = env
			in, err := cmd.StdinPipe()
			if err != nil {
				_ = connection.Close()
				failures <- err
				return
			}
			out, err := cmd.StdoutPipe()
			if err != nil {
				_ = in.Close()
				_ = connection.Close()
				failures <- err
				return
			}
			if err := cmd.Start(); err != nil {
				_ = in.Close()
				_ = out.Close()
				_ = connection.Close()
				failures <- err
				return
			}
			inputDone := make(chan struct{})
			outputDone := make(chan struct{})
			go func() { _, _ = io.Copy(in, connection); _ = in.Close(); close(inputDone) }()
			go func() { _, _ = io.Copy(connection, out); close(outputDone) }()
			err = cmd.Wait()
			_ = connection.Close()
			<-inputDone
			<-outputDone
			if err != nil && ctx.Err() == nil {
				select {
				case failures <- err:
				default:
				}
				return
			}
		}
	}()
	stop := func() { cancel(); <-done }
	readyCtx, readyCancel := context.WithTimeout(ctx, 20*time.Second)
	defer readyCancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-failures:
			stop()
			return nil, fmt.Errorf("GPG forwarding: %w", err)
		case <-readyCtx.Done():
			stop()
			return nil, errors.New("GPG forwarding did not become ready")
		case <-ticker.C:
			check := exec.CommandContext(readyCtx, sbx, "exec", name, "test", "-S", remote)
			check.Env = env
			if err := check.Run(); err == nil {
				return stop, nil
			}
		}
	}
}

// Guest paths are always POSIX paths, even when the host runs Windows.
func isolatedPath(text, hostDir string) string {
	text = strings.ReplaceAll(text, hostDir+string(filepath.Separator), isolatedHome+"/")
	return strings.ReplaceAll(text, hostDir, isolatedHome)
}
