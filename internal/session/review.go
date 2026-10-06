package session

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/github"
	"github.com/appetizers-io/outrider/prompts"
)

// Review records the resolved profile and effective, never enlarged, policy.
type Review struct {
	Name           string   `json:"name"`
	Rule           string   `json:"rule"`
	Body           string   `json:"-"`
	PlaybookSHA256 string   `json:"playbook_sha256,omitempty"`
	Model          string   `json:"model,omitempty"`
	Effort         string   `json:"effort,omitempty"`
	MaxMinutes     int      `json:"max_minutes,omitempty"`
	Tests          string   `json:"tests"`
	Comments       string   `json:"comments"`
	Outbox         string   `json:"outbox,omitempty"`
	Context        string   `json:"context,omitempty"`
	Trusted        bool     `json:"trusted"`
	Notes          []string `json:"notes,omitempty"`
}

func (l *Launcher) review(r Request, cfg *config.Config, dir string, readonly bool, worktrees ...string) (*Review, error) {
	if cfg.Review == nil || strings.EqualFold(r.PR.AuthorLogin(), l.Login) {
		return nil, nil
	}
	name, rule := cfg.Review.Select(r.Repo, r.Event)
	if name == "" {
		return nil, nil
	}
	p := cfg.Review.Profiles[name]
	result := &Review{Name: name, Rule: rule, Model: p.Model[cfg.Agent], Effort: p.Effort, MaxMinutes: p.MaxMinutes,
		Tests: "off", Comments: "off", Trusted: cfg.Review.Trusted(r.Repo, r.PR.AuthorLogin())}
	var body []byte
	var err error
	if p.Playbook != "" {
		source := l.ConfigSource
		if source == "" {
			source = config.DefaultPath()
		}
		if local, ok := l.Local[r.Repo]; ok {
			worktrees = append(worktrees, local.Path)
		}
		body, err = readPlaybook(filepath.Dir(source), p.Playbook, worktrees...)
		if err == nil {
			result.PlaybookSHA256 = fmt.Sprintf("%x", sha256.Sum256(body))
		}
	} else {
		body, err = prompts.FS.ReadFile("profiles/" + name + ".md")
	}
	if err != nil {
		return nil, fmt.Errorf("review profile %s: %w", name, err)
	}
	result.Body = string(body)
	if name != "quick" && result.Trusted && p.Evidence.Tests != "" {
		result.Tests = p.Evidence.Tests
	}
	if p.Evidence.Tests != "" && p.Evidence.Tests != "off" && !result.Trusted {
		result.Notes = append(result.Notes, "untrusted author/repository: static review only")
	}
	if readonly && result.Tests == "e2e" {
		result.Tests = "off"
	}
	if readonly && result.Tests == "local" {
		result.Notes = append(result.Notes, "offline tests require a warm cache; sandbox writes remain forbidden")
	}
	if p.Evidence.Comments == "draft" {
		result.Comments = "draft"
		if readonly {
			result.Notes = append(result.Notes, "read-only sandbox: drafts stay in the transcript")
		} else {
			result.Outbox = filepath.Join(dir, "outbox")
			if err := os.MkdirAll(result.Outbox, 0o700); err != nil {
				return nil, err
			}
		}
	}
	if p.Evidence.Tests != "off" && len(cfg.OthersPRs.Forks()) == 0 {
		result.Notes = append(result.Notes, "no review fork: evidence stays local; PR edits remain governed by session policy")
	}
	if p.Context.Issues || len(p.Context.Docs) > 0 {
		result.Context = filepath.Join(dir, "review-context")
	}
	return result, nil
}

const maxPlaybook = 64 << 10

func readPlaybook(configDir, path string, forbidden ...string) ([]byte, error) {
	dir, err := filepath.Abs(configDir)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, path[2:])
	}
	if filepath.IsAbs(path) {
		path, err = filepath.Rel(dir, path)
		if err != nil {
			return nil, err
		}
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(dir, path))
	if err != nil {
		return nil, err
	}
	for _, checkout := range forbidden {
		if checkout == "" {
			continue
		}
		base, err := filepath.EvalSymlinks(checkout)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(base, resolved)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return nil, fmt.Errorf("playbooks must not be read from the PR repository")
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(path)
	if err != nil {
		return nil, fmt.Errorf("playbook must be a readable file within the owner configuration directory: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("playbook must be a regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxPlaybook+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxPlaybook {
		return nil, fmt.Errorf("playbook exceeds %d bytes", maxPlaybook)
	}
	return raw, nil
}

func (l *Launcher) reviewContext(ctx context.Context, r Request, cfg *config.Config, review *Review, worktree, dir string) error {
	if review == nil {
		return nil
	}
	requested := cfg.Review.Profiles[review.Name].Context
	if !requested.Issues && len(requested.Docs) == 0 {
		return nil
	}
	review.Context = filepath.Join(dir, "review-context")
	client := &github.Client{Run: l.Run}
	return client.SaveReviewContext(ctx, r.Repo, r.N, worktree, requested.Issues, requested.Docs, review.Context)
}
