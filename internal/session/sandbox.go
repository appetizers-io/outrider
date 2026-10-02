package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/appetizers-io/outrider/internal/github"
	"github.com/appetizers-io/outrider/internal/proc"
)

// The oldest agent versions whose sandbox flags and settings were checked
// against their docs and in real sessions. Claude Code 2.1.285 is the first
// that holds allowUnsandboxedCommands: false from --settings against a
// repository's own settings.
var minVersion = map[string]string{"claude": "v2.1.285", "codex": "v0.156.0"}

// SandboxNote describes the read-only sandbox of an agent for the startup log.
func SandboxNote(agent string) string {
	if agent == "claude" {
		return "claude: native sandbox + deny rules, no user or project settings"
	}
	return "codex: --sandbox read-only, approvals never, private CODEX_HOME"
}

var versionRe = regexp.MustCompile(`\d+\.\d+\.\d+`)

// SandboxSupport tells why agent can't run a read-only sandboxed session on
// goos, or nil when it can. codexHome is the user's CODEX_HOME.
func SandboxSupport(ctx context.Context, agent, goos, codexHome string, lookPath func(string) (string, error), run proc.Runner) error {
	switch goos {
	case "darwin": // Seatbelt
	case "linux":
		tools := []string{"bwrap"}
		if agent == "claude" {
			tools = append(tools, "socat")
		}
		for _, tool := range tools {
			if _, err := lookPath(tool); err != nil {
				return fmt.Errorf("the %s sandbox on Linux needs %s", agent, tool)
			}
		}
	default:
		return fmt.Errorf("outrider supports no %s sandbox on %s", agent, goos)
	}
	res, err := run(ctx, proc.Cmd{Args: []string{agent, "--version"}})
	if err != nil {
		return fmt.Errorf("%s --version: %w", agent, err)
	}
	v := "v" + versionRe.FindString(res.Stdout)
	if want := minVersion[agent]; !semver.IsValid(v) || semver.Compare(v, want) < 0 {
		return fmt.Errorf("%s %s is older than %s, the oldest version whose sandbox outrider checked",
			agent, strings.TrimSpace(res.Stdout), strings.TrimPrefix(want, "v"))
	}
	if agent == "codex" {
		if _, err := os.Stat(filepath.Join(codexHome, "auth.json")); err != nil {
			return errors.New("codex sandboxed sessions need file-based login (" +
				filepath.Join(codexHome, "auth.json") + "); log in with `codex login`")
		}
	}
	return nil
}

// Sandbox is what a read-only session gets prepared before it starts.
type Sandbox struct {
	ContextDir string   // the prefetched PR context
	DenyWrite  []string // the worktree and its git dir
	Checkout   string   // the checkout that owns the git dir
	CodexHome  string   // codex only: the session's CODEX_HOME
}

// prepareSandbox fetches the PR context and finds what the sandbox must keep
// read-only: the worktree and its git dir, which Claude Code's sandbox would
// otherwise let a linked worktree write.
func (l *Launcher) prepareSandbox(ctx context.Context, r Request, worktree string) (*Sandbox, error) {
	dir := sessionDir(l.Root, r.Repo, r.N)
	sb := &Sandbox{ContextDir: filepath.Join(dir, "pr-context")}
	if err := os.RemoveAll(sb.ContextDir); err != nil {
		return nil, fmt.Errorf("pr context: %w", err)
	}
	if err := (&github.Client{Run: l.Run}).SaveContext(ctx, r.Repo, r.N, sb.ContextDir); err != nil {
		return nil, err //nolint:wrapcheck // names the gh command already
	}
	res, err := l.Run(ctx, proc.Cmd{Args: []string{"git", "rev-parse", "--path-format=absolute", "--git-common-dir"}, Dir: worktree})
	if err != nil {
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	gitDir := strings.TrimSpace(res.Stdout)
	if gitDir == "" {
		return nil, errors.New("sandbox: no git dir for " + worktree)
	}
	sb.DenyWrite = []string{worktree, gitDir}
	sb.Checkout = gitDir
	if filepath.Base(gitDir) == ".git" {
		sb.Checkout = filepath.Dir(gitDir)
	}
	if l.Agent == "codex" {
		// a private CODEX_HOME holds only the login: the user's rules can
		// allow commands outside the sandbox, and their MCP servers and
		// plugins run outside it
		sb.CodexHome = filepath.Join(dir, "codex-home")
		if err := os.MkdirAll(sb.CodexHome, 0o700); err != nil {
			return nil, fmt.Errorf("sandbox: %w", err)
		}
		auth := filepath.Join(sb.CodexHome, "auth.json")
		if err := os.Remove(auth); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("sandbox: %w", err)
		}
		if err := os.Symlink(filepath.Join(l.CodexHome, "auth.json"), auth); err != nil {
			return nil, fmt.Errorf("sandbox: %w", err)
		}
		// saved as untrusted, Codex opens the folder restricted instead of
		// offering to trust it (a -c override alone still offers that)
		var cfg strings.Builder
		for _, dir := range []string{worktree, sb.Checkout} {
			fmt.Fprintf(&cfg, "[projects.%s]\ntrust_level = \"untrusted\"\n\n", tomlString(dir))
		}
		if err := os.WriteFile(filepath.Join(sb.CodexHome, "config.toml"), []byte(cfg.String()), 0o600); err != nil {
			return nil, fmt.Errorf("sandbox: %w", err)
		}
	}
	return sb, nil
}

// tomlString quotes s as a TOML basic string.
func tomlString(s string) string {
	raw, _ := json.Marshal(s) // a JSON string is a TOML basic string
	return string(raw)
}

// readOnlyTools are the only Claude Code tools of a read-only session.
const readOnlyTools = "Bash,Read,Glob,Grep"

// ClaudeSandboxArgs are the Claude Code flags of a read-only session, after
// --settings: no settings files at all (`--setting-sources ""`), so neither a
// PR's checkout nor the user's own settings can add hooks or loosen the
// sandbox (Claude Code adds their sandbox lists and WebFetch allow rules to
// outrider's); no MCP servers, no bypass mode, read-only tools.
func ClaudeSandboxArgs() []string {
	return []string{"--setting-sources", "", "--strict-mcp-config", "--permission-mode", "manual", "--tools", readOnlyTools}
}

// ClaudeSandbox is the sandbox key of a read-only session's --settings: Bash
// runs sandboxed or not at all, writes nothing in the worktree or its git
// dir, and reaches no host.
func ClaudeSandbox(denyWrite []string) map[string]any {
	return map[string]any{
		"enabled":                  true,
		"failIfUnavailable":        true,
		"allowUnsandboxedCommands": false,
		"autoAllowBashIfSandboxed": true,
		"filesystem":               map[string]any{"denyWrite": denyWrite},
		"network":                  map[string]any{"allowedDomains": []string{}, "strictAllowlist": true},
	}
}

// readOnlyDenyRules are added to the review-only deny rules: no web access.
var readOnlyDenyRules = []string{"WebFetch", "WebSearch"}

// CodexSandboxArgs are the Codex flags of a read-only session in worktree,
// a worktree of checkout: the read-only sandbox (no writes, no network), no
// approvals that could leave it, no tools that run outside it, and both
// untrusted, so Codex neither asks to trust them nor loads their .codex
// config and rules (Codex keys a worktree's trust by its main checkout).
func CodexSandboxArgs(worktree, checkout string) []string {
	args := []string{"--sandbox", "read-only", "--ask-for-approval", "never", "-c", `web_search="disabled"`}
	for _, dir := range []string{worktree, checkout} {
		args = append(args, "-c", fmt.Sprintf(`projects.%s.trust_level="untrusted"`, tomlString(dir)))
	}
	return append(args, "--disable", "apps", "--disable", "plugins", "--disable", "browser_use",
		"--disable", "computer_use", "--disable", "in_app_browser")
}
