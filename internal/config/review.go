package config

import (
	"fmt"
	"slices"
	"strings"
)

// Review configures opt-in profiles for other people's PRs.
type Review struct {
	Default        *string                  `yaml:"default" jsonschema:"minLength=1,nullable" jsonschema_description:"Default profile; null disables it. Built-ins: quick, standard, deep."`
	Profiles       map[string]ReviewProfile `yaml:"profiles,omitempty"`
	Rules          []ReviewRule             `yaml:"rules,omitempty"`
	TrustedAuthors []string                 `yaml:"trusted_authors,omitempty" jsonschema:"minLength=1"`
	TrustedRepos   []string                 `yaml:"trusted_repos,omitempty" jsonschema:"minLength=1" jsonschema_description:"Repository globs whose code may run automatically; org membership is not inferred."`
}

// ReviewProfile adds optional playbooks, execution hints and evidence policy.
type ReviewProfile struct {
	Playbook   string            `yaml:"playbook,omitempty" jsonschema:"minLength=1" jsonschema_description:"Owner-authored Markdown within the configuration directory, never the PR checkout."`
	Model      map[string]string `yaml:"model,omitempty" jsonschema_description:"Per-agent model name: claude or codex."`
	Effort     string            `yaml:"effort,omitempty" jsonschema:"enum=low,enum=medium,enum=high"`
	MaxMinutes int               `yaml:"max_minutes,omitempty" jsonschema:"minimum=1,maximum=1440"`
	Evidence   ReviewEvidence    `yaml:"evidence"`
	Context    ReviewContext     `yaml:"context"`
}

// ReviewEvidence only requests capabilities; session policy still limits them.
type ReviewEvidence struct {
	Tests    string `yaml:"tests,omitempty" jsonschema:"enum=off,enum=local,enum=e2e"`
	Comments string `yaml:"comments,omitempty" jsonschema:"enum=off,enum=draft" jsonschema_description:"Drafts are local files; pending GitHub reviews are deferred."`
}

// ReviewContext is prefetched by the watcher, not browsed by the agent.
type ReviewContext struct {
	Issues bool     `yaml:"issues"`
	Docs   []string `yaml:"docs,omitempty" jsonschema:"minLength=1" jsonschema_description:"Base-branch path globs; the head cannot rewrite its specification."`
}

// ReviewRule selects a profile. The first matching rule wins.
type ReviewRule struct {
	Repos    []string `yaml:"repos" jsonschema:"minLength=1"`
	Triggers []string `yaml:"triggers" jsonschema:"enum=opt_in,enum=on_change,enum=mentions,enum=review_replies,minLength=1"`
	Profile  string   `yaml:"profile" jsonschema:"required,minLength=1"`
}

func builtInReview(name string) bool {
	return slices.Contains([]string{"quick", "standard", "deep"}, name)
}

// Select resolves rules without widening the request's scope.
func (r *Review) Select(repo, event string) (name, rule string) {
	if r == nil {
		return "", "disabled"
	}
	switch event {
	case "mention":
		event = "mentions"
	case "review_reply":
		event = "review_replies"
	case "review_change":
		event = "on_change"
	}
	for i, match := range r.Rules {
		globs, _ := RepoGlobs(match.Repos)
		if (len(match.Repos) == 0 || globs.Match(repo)) && (len(match.Triggers) == 0 || slices.Contains(match.Triggers, event)) {
			return match.Profile, fmt.Sprintf("review.rules[%d]", i)
		}
	}
	if r.Default == nil {
		return "", "review.default: null"
	}
	name = *r.Default
	if name == "deep" && event == "on_change" {
		return "standard", "review.default: deep → standard on delta"
	}
	return name, "review.default"
}

// Trusted is an explicit author or repository allowlist, never org membership.
func (r *Review) Trusted(repo, author string) bool {
	if r == nil {
		return false
	}
	for _, a := range r.TrustedAuthors {
		if strings.EqualFold(a, author) {
			return true
		}
	}
	g, _ := RepoGlobs(r.TrustedRepos)
	return len(r.TrustedRepos) > 0 && g.Match(repo)
}

func (c *Config) checkReview() []string {
	r := c.Review
	if r == nil {
		return nil
	}
	var errs []string
	known := func(name string) bool { _, ok := r.Profiles[name]; return builtInReview(name) || ok }
	if r.Default != nil && !known(*r.Default) {
		errs = append(errs, "unknown review profile: "+*r.Default)
	}
	if _, err := RepoGlobs(r.TrustedRepos); err != nil {
		errs = append(errs, "review.trusted_repos: "+err.Error())
	}
	for _, rule := range r.Rules {
		if !known(rule.Profile) {
			errs = append(errs, "unknown review profile: "+rule.Profile)
		}
		if _, err := RepoGlobs(rule.Repos); err != nil {
			errs = append(errs, "review.rules.repos: "+err.Error())
		}
	}
	for name, p := range r.Profiles {
		if !builtInReview(name) && p.Playbook == "" {
			errs = append(errs, "custom review profile "+name+" requires a playbook")
		}
		for agent, model := range p.Model {
			if (agent != "claude" && agent != "codex") || strings.TrimSpace(model) == "" {
				errs = append(errs, "review.model requires claude/codex and nonempty model names")
			}
		}
		if p.Evidence.Tests == "e2e" {
			explicit := slices.ContainsFunc(r.Rules, func(rule ReviewRule) bool { return rule.Profile == name })
			if c.SandboxFor(false) != "off" || !explicit || len(r.TrustedAuthors)+len(r.TrustedRepos) == 0 {
				errs = append(errs, "review evidence e2e requires sandbox: off, an explicit profile rule and trusted authors or repositories")
			}
		}
	}
	return errs
}
