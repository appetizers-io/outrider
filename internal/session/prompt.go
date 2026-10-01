package session

import (
	"fmt"
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
	Push          string // review-only | never | ask | allow
	GHWrites      string // never | ask | allow
}

func pyBool(b *bool) string {
	switch {
	case b == nil:
		return "None"
	case *b:
		return "True"
	}
	return "False"
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
	data := map[string]any{
		"Trigger": in.Trigger, "Repo": in.Repo, "N": in.N, "URL": in.PR.URL, "Title": in.PR.Title,
		"Author": author, "Head": owner + "/" + name, "HeadRef": in.PR.HeadRefName,
		"MaintainerCanModify": pyBool(in.PR.MaintainerCanModify), "Base": in.PR.BaseRefName,
		"Owner": in.Owner, "Remote": remote, "Own": own, "AllowPush": in.AllowPush,
		"MayPush": own || in.AllowPush, "Push": in.Push, "PushRule": in.Push == "ask" || in.Push == "never",
		"GHWrites": in.GHWrites, "Writable": in.GHWrites == "ask" || in.GHWrites == "allow",
		"Scope": in.Scope, "ScopeWhy": why, "PolicyFile": in.PolicyFile, "Extra": strings.TrimSpace(in.Extra),
	}
	var b strings.Builder
	if err := promptTmpl.Execute(&b, data); err != nil {
		return "", fmt.Errorf("render prompt: %w", err)
	}
	return b.String(), nil
}
