package session

import (
	"fmt"
	"strconv"
	"strings"
	"text/template"

	"github.com/appetizers-io/outrider/internal/github"
	"github.com/appetizers-io/outrider/prompts"
)

var promptTmpl = template.Must(template.ParseFS(prompts.FS, "prompt.tmpl"))

// PromptInput is what the agent prompt is about.
type PromptInput struct {
	Repo, Trigger string
	N             int
	PR            github.PR
	Owner, Login  string   // how prompts call the user; their GitHub login
	Remote        string   // remote to rebase on: origin, or upstream for a fork checkout
	Scope         []string // comment URLs the session is limited to; nil: the whole PR
	ScopeWhy      string
	Extra         string // prompts.extra
	AllowPush     bool   // others_prs.allow_push in effect
	PolicyFile    string
	Push          string   // review-only | review-forks | never | ask | allow
	GHWrites      string   // never | ask | allow
	ContextDir    *string  // read-only sandbox: the prefetched PR context; nil: no sandbox
	ReviewForks   []string // review only, but evidence may be pushed to these owner/repo globs (following Push)
}

// tristate is a yes/no that GitHub may leave out.
func tristate(b *bool) string {
	if b == nil {
		return "unknown"
	}
	return strconv.FormatBool(*b)
}

// Prompt is the text an agent session starts with.
func Prompt(in PromptInput) (string, error) {
	author := in.PR.AuthorLogin()
	own := strings.EqualFold(author, in.Login)
	owner, name := "?", "?"
	if in.PR.HeadRepositoryOwner != nil && in.PR.HeadRepositoryOwner.Login != "" {
		owner = in.PR.HeadRepositoryOwner.Login
	}
	if in.PR.HeadRepository != nil && in.PR.HeadRepository.Name != "" {
		name = in.PR.HeadRepository.Name
	}
	why := in.ScopeWhy
	if why == "" {
		why = "where someone replied to " + in.Owner + "'s review comment"
	}
	remote := in.Remote
	if remote == "" {
		remote = "origin"
	}
	sandbox := in.ContextDir != nil
	data := map[string]any{
		"Trigger": in.Trigger, "Repo": in.Repo, "N": in.N, "URL": in.PR.URL, "Title": in.PR.Title,
		"Author": author, "Head": owner + "/" + name, "HeadRef": in.PR.HeadRefName,
		"MaintainerCanModify": tristate(in.PR.MaintainerCanModify), "Base": in.PR.BaseRefName,
		"Owner": in.Owner, "Remote": remote, "Own": own && !sandbox, "AllowPush": in.AllowPush && !sandbox,
		"MayPush": (own || in.AllowPush) && !sandbox, "Push": in.Push, "PushRule": in.Push == "ask" || in.Push == "never",
		"GHWrites": in.GHWrites, "Writable": in.GHWrites == "ask" || in.GHWrites == "allow",
		"Scope": in.Scope, "ScopeWhy": why, "PolicyFile": in.PolicyFile, "Extra": strings.TrimSpace(in.Extra),
		"Sandbox": sandbox, "ContextFiles": github.ContextFiles,
		"Forks": len(in.ReviewForks) > 0, "ReviewForks": strings.Join(in.ReviewForks, ", "),
	}
	if sandbox {
		data["ContextDir"] = *in.ContextDir
	}
	var b strings.Builder
	if err := promptTmpl.Execute(&b, data); err != nil {
		return "", fmt.Errorf("render prompt: %w", err)
	}
	return b.String(), nil
}
