package session

import (
	"fmt"
	"strings"
	"testing"

	"gotest.tools/v3/golden"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/github"
)

// The prompts and tool-gate rules are pinned in testdata/: a change to
// what agents are told shows up as a reviewed diff.

func goldenPR(author string) github.PR {
	return github.PR{
		Author: &github.User{Login: author}, BaseRefName: "main", HeadRefName: "feat/x",
		Title: "T", URL: "https://github.com/o/r/pull/1",
		HeadRepositoryOwner: &github.User{Login: author},
		HeadRepository: &struct {
			Name string `json:"name"`
		}{Name: "r"},
		MaintainerCanModify: new(author == "me"),
	}
}

type promptCase struct {
	author    string
	allowPush bool
	push, gh  string
	scope     string // "", "replies" or "mention"
	extra     bool   // prompts.extra and a policy file
	local     bool   // a fork checkout: rebase onto upstream
}

func (c promptCase) name() string {
	scope := c.scope
	if scope == "" {
		scope = "pr"
	}
	return fmt.Sprintf("prompts/%s-allowpush_%t-push_%s-gh_%s-scope_%s-extra_%t-local_%t.txt",
		c.author, c.allowPush, c.push, c.gh, scope, c.extra, c.local)
}

func promptCases() []promptCase {
	var out []promptCase
	for _, author := range []string{"me", "bob"} {
		for _, allowPush := range []bool{false, true} {
			for _, push := range []string{"ask", "never", "allow", "review-only"} {
				for _, gh := range []string{"ask", "never", "allow"} {
					out = append(out, promptCase{author: author, allowPush: allowPush, push: push, gh: gh})
				}
			}
		}
	}
	// scope, prompts.extra and fork checkouts for a review-only and an own PR
	for _, base := range []promptCase{{author: "bob", push: "review-only", gh: "never"}, {author: "me", push: "ask", gh: "ask"}} {
		for _, scope := range []string{"", "replies", "mention"} {
			for _, extra := range []bool{false, true} {
				for _, local := range []bool{false, true} {
					if scope == "" && !extra && !local {
						continue // in the matrix above
					}
					c := base
					c.scope, c.extra, c.local = scope, extra, local
					out = append(out, c)
				}
			}
		}
	}
	return out
}

func TestPromptsGolden(t *testing.T) {
	for _, c := range promptCases() {
		in := PromptInput{
			Repo: "o/r", N: 1, PR: goldenPR(c.author), Trigger: "trigger x", Owner: "Matthias", Login: "me",
			AllowPush: c.allowPush, Push: c.push, GHWrites: c.gh,
		}
		switch c.scope {
		case "replies":
			in.Scope = []string{"https://x/c1", "https://x/c2"}
		case "mention":
			in.Scope, in.ScopeWhy = []string{"https://x/c3"}, "where someone mentioned @me"
		}
		if c.extra {
			in.Extra, in.PolicyFile = "Run make test.\n", "/s/policy.json"
		}
		if c.local {
			in.Remote = "upstream"
		}
		got, err := Prompt(in)
		if err != nil {
			t.Fatal(err)
		}
		golden.Assert(t, got, c.name())
	}
}

func TestGateRulesGolden(t *testing.T) {
	withRules := config.Default()
	withRules.ToolGate.Rules = []string{"never modify generated/", "x"}
	withRules.ToolGate.IncludePromptExtra = true
	withRules.Prompts.Extra = " no make release \n"
	cfgs := map[string]config.Config{"defaults": config.Default(), "user-rules": withRules}
	for _, cfgName := range []string{"defaults", "user-rules"} {
		cfg := cfgs[cfgName]
		var b strings.Builder
		for _, own := range []bool{true, false} {
			for _, push := range []string{"ask", "never", "allow", "review-only"} {
				for _, gh := range []string{"ask", "never", "allow"} {
					fmt.Fprintf(&b, "== own_%t push_%s gh_%s\n%s\n\n", own, push, gh,
						GateText(&cfg, "o/r", 7, "bob", "Matthias", own, push, gh))
				}
			}
		}
		golden.Assert(t, b.String(), "gate-rules/"+cfgName+".txt")
	}
}

// The read-only GitHub guardrail must stay in every session where posting is
// off, whatever the wording around it.
func TestReadOnlyPromptKeepsTheGuardrail(t *testing.T) {
	for _, c := range []PromptInput{
		{PR: goldenPR("bob"), Push: "review-only", GHWrites: "never"},
		{PR: goldenPR("me"), Push: "ask", GHWrites: "never"},
	} {
		c.Repo, c.N, c.Trigger, c.Owner, c.Login = "o/r", 1, "t", "Matthias", "me"
		got, err := Prompt(c)
		if err != nil {
			t.Fatal(err)
		}
		flat := strings.Join(strings.Fields(got), " ")
		for _, rule := range []string{
			"GITHUB: read-only in this session.",
			"keep ALL review summaries/questions in this local session",
			"do NOT post comments or review replies",
			"do NOT add/remove reactions",
			"do NOT submit/approve/reject reviews",
			"The `gh` on PATH is read-only and will refuse GitHub writes; do not try to work around it (no curl/API tokens).",
			"do NOT merge/close the PR",
		} {
			if !strings.Contains(flat, rule) {
				t.Errorf("%s/%s prompt lacks %q", c.PR.AuthorLogin(), c.Push, rule)
			}
		}
		if strings.Contains(got, "STRICT") {
			t.Errorf("prompt still has the old STRICT header")
		}
	}
}

func TestSandboxPromptsGolden(t *testing.T) {
	ctx := "/s/pr-context"
	for _, author := range []string{"me", "bob"} {
		for _, scope := range []string{"pr", "mention"} {
			in := PromptInput{
				Repo: "o/r", N: 1, PR: goldenPR(author), Trigger: "trigger x", Owner: "Matthias", Login: "me",
				Push: "review-only", GHWrites: "never", PolicyFile: "/s/policy.json", ContextDir: &ctx,
			}
			if scope == "mention" {
				in.Scope, in.ScopeWhy = []string{"https://x/c3"}, "where someone mentioned @me"
			}
			got, err := Prompt(in)
			if err != nil {
				t.Fatal(err)
			}
			golden.Assert(t, got, fmt.Sprintf("prompts/sandbox-%s-scope_%s.txt", author, scope))
		}
	}
}
