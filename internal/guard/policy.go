package guard

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Policy is the guard's trusted subset of the session policy. It is loaded
// beside the running guard, never from agent-controlled environment variables.
type Policy struct {
	Repo         string   `json:"repo"`
	PR           int      `json:"pr"`
	Push         string   `json:"push"`
	ReviewOnly   bool     `json:"review_only"`
	ReviewForks  []string `json:"review_forks"`
	GitHubWrites string   `json:"github_writes"`
	Guard        Runtime  `json:"guard"`
}

// Runtime is captured by the watcher before any agent runs.
type Runtime struct {
	Git     string            `json:"git"`
	GH      string            `json:"gh"`
	SSH     string            `json:"ssh"`
	Head    string            `json:"head_repo"`
	Display map[string]string `json:"display"`
	Hook    []string          `json:"hook,omitempty"`
	HookEnv map[string]string `json:"hook_env,omitempty"`
}

func loadPolicy(executable string) (Policy, error) {
	var p Policy
	// Every guard is a real copy in <session>/bin. Symlinks are not anchors.
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(executable), "..", "policy.json"))
	if err != nil {
		return p, fmt.Errorf("cannot read session policy: %w", err)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, errors.New("invalid session policy")
	}
	if p.Repo == "" || p.PR < 1 || !filepath.IsAbs(p.Guard.Git) || !filepath.IsAbs(p.Guard.GH) {
		return p, errors.New("incomplete session policy")
	}
	return p, nil
}

func trustedEnv() (func(string) string, Policy, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, Policy{}, err
	}
	p, err := loadPolicy(self)
	if err != nil {
		return nil, p, err
	}
	push := p.Push
	if p.ReviewOnly {
		push = "review-only"
	}
	values := map[string]string{
		EnvPush: push, EnvGHWrites: p.GitHubWrites, EnvRepo: p.Repo,
		EnvPR: strconv.Itoa(p.PR), EnvRealGit: p.Guard.Git, EnvRealGH: p.Guard.GH,
		EnvHeadRepo: p.Guard.Head, EnvReviewForks: strings.Join(p.ReviewForks, "\n"),
		EnvReviewForksPush: p.Push, EnvSession: fmt.Sprintf("PR %s#%d", p.Repo, p.PR),
	}
	return func(k string) string { return values[k] }, p, nil
}
