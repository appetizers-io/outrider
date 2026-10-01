package session

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/github"
)

// Prompts and tool-gate rules must stay what the Python version produced.

type parity struct {
	PromptTexts map[string]string `json:"prompt_texts"`
	Prompts     []struct {
		Author     string   `json:"author"`
		AllowPush  bool     `json:"allow_push"`
		Push       string   `json:"push"`
		GH         string   `json:"gh"`
		Scope      []string `json:"scope"`
		ScopeWhy   *string  `json:"scope_why"`
		Extra      string   `json:"extra"`
		PolicyFile *string  `json:"policy_file"`
		Local      bool     `json:"local"`
		Want       string   `json:"want"`
	} `json:"prompts"`
	Rules []struct {
		Args struct {
			Cfg  int    `json:"cfg"`
			Own  bool   `json:"own"`
			Push string `json:"push"`
			GH   string `json:"gh"`
		} `json:"args"`
		Text string `json:"text"`
	} `json:"rules"`
}

func loadParity(t *testing.T) parity {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/python-parity.json")
	require.NoError(t, err)
	var p parity
	require.NoError(t, json.Unmarshal(raw, &p))
	return p
}

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

func TestPromptsMatchThePythonVersion(t *testing.T) {
	p := loadParity(t)
	require.NotEmpty(t, p.Prompts)
	for _, c := range p.Prompts {
		in := PromptInput{
			Repo: "o/r", N: 1, PR: goldenPR(c.Author), Trigger: "trigger x", Owner: "Matthias", Login: "me",
			Scope: c.Scope, Extra: c.Extra, AllowPush: c.AllowPush, Push: c.Push, GHWrites: c.GH,
		}
		if c.ScopeWhy != nil {
			in.ScopeWhy = *c.ScopeWhy
		}
		if c.PolicyFile != nil {
			in.PolicyFile = *c.PolicyFile
		}
		if c.Local {
			in.Remote = "upstream"
		}
		got, err := Prompt(in)
		require.NoError(t, err)
		require.Equal(t, p.PromptTexts[c.Want], got, fmt.Sprintf("%+v", c))
	}
}

func TestGateRulesMatchThePythonVersion(t *testing.T) {
	p := loadParity(t)
	withRules := config.Default()
	withRules.ToolGate.Rules = []string{"never modify generated/", "x"}
	withRules.ToolGate.IncludePromptExtra = true
	withRules.Prompts.Extra = " no make release \n"
	cfgs := []config.Config{config.Default(), withRules}
	require.NotEmpty(t, p.Rules)
	for _, r := range p.Rules {
		got := GateText(&cfgs[r.Args.Cfg], "o/r", 7, "bob", "Matthias", r.Args.Own, r.Args.Push, r.Args.GH)
		require.Equal(t, r.Text, got, fmt.Sprintf("%+v", r.Args))
	}
}
