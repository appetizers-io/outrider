package classifier

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/github"
	"github.com/appetizers-io/outrider/internal/proc"
)

// As a local classifier the test binary saves its request and prints $CLS_ANSWER.
func TestMain(m *testing.M) {
	if answer, ok := os.LookupEnv("CLS_ANSWER"); ok && len(os.Args) > 1 && os.Args[1] == "launch" {
		in, _ := io.ReadAll(os.Stdin)
		_ = os.WriteFile(os.Getenv("CLS_REQUEST"), in, 0o600)
		fmt.Print(answer)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func which(present ...string) LookPath {
	return func(f string) (string, error) {
		for _, p := range present {
			if p == f {
				return "/bin/" + f, nil
			}
		}
		return "", errors.New("not found")
	}
}

func noKeys(t *testing.T) {
	for _, k := range config.JevBackendEnv {
		t.Setenv(k, "")
	}
}

func jev(mod func(*config.JevClassifier)) config.Classifier {
	c := config.DefaultJev()
	if mod != nil {
		mod(&c.Jev)
	}
	return c
}

// --- resolving: on only when it can work ----------------------------------

func TestJevAutoOffWithoutBackendKey(t *testing.T) {
	noKeys(t)
	r, why := Resolve("jev", jev(nil), which("npx"))
	require.Nil(t, r)
	require.Contains(t, why, "no backend key")
}

func TestJevPrefersInstalledJevUse(t *testing.T) {
	noKeys(t)
	t.Setenv("TYPESAFE_API_KEY", "x")
	r, note := Resolve("jev", jev(nil), which("npx", "jev-use"))
	require.NotNil(t, r)
	require.Equal(t, []string{"jev-use", "judge"}, r.LaunchCmd)
	require.Equal(t, []string{"jev-use", "hook", "gate"}, r.HookCmd)
	require.Equal(t, "jev-use", note)
	require.Equal(t, 60*time.Second, r.Timeout)
}

func TestJevFallsBackToNpx(t *testing.T) {
	noKeys(t)
	t.Setenv("OPENROUTER_API_KEY", "x")
	r, note := Resolve("jev", jev(nil), which("npx"))
	require.NotNil(t, r)
	require.Equal(t, []string{"npx", "-y", "jev-use@0.8.0", "judge"}, r.LaunchCmd)
	require.Equal(t, "npx -y jev-use@0.8.0", note)
}

func TestJevOffWhenCommandMissing(t *testing.T) {
	noKeys(t)
	t.Setenv("TYPESAFE_API_KEY", "x")
	r, why := Resolve("jev", jev(nil), which())
	require.Nil(t, r)
	require.Equal(t, "npx not found", why)
}

func TestJevExplicitlyEnabledNeedsNoKey(t *testing.T) {
	noKeys(t)
	c := jev(func(j *config.JevClassifier) { j.Enabled = config.EnabledTrue; j.Command = new("my-jev --flag") })
	r, _ := Resolve("jev", c, which("my-jev"))
	require.NotNil(t, r)
	require.Equal(t, []string{"my-jev", "--flag", "hook", "gate"}, r.HookCmd)
}

func TestDisabled(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "x")
	r, why := Resolve("jev", jev(func(j *config.JevClassifier) { j.Enabled = config.EnabledFalse }), which("npx"))
	require.Nil(t, r)
	require.Equal(t, "disabled", why)
	cmd := config.Classifier{Kind: config.KindCommand, Command: config.CommandClassifier{Enabled: false}}
	r, why = Resolve("local", cmd, which())
	require.Nil(t, r)
	require.Equal(t, "disabled", why)
}

func TestCommandClassifierNeedsItsBinaries(t *testing.T) {
	hook := "~/bin/cls hook"
	c := config.Classifier{Kind: config.KindCommand, HookCommand: &hook,
		Command: config.CommandClassifier{Kind: config.KindCommand, Enabled: true, HookCommand: &hook, TimeoutSeconds: 30}}
	r, why := Resolve("local", c, which())
	require.Nil(t, r)
	require.True(t, strings.HasSuffix(why, filepath.Join("bin", "cls")+" not found"), why)
	r, note := Resolve("local", c, func(f string) (string, error) { return f, nil })
	require.NotNil(t, r)
	require.Equal(t, "command", note)
	require.True(t, strings.HasSuffix(r.HookCmd[0], filepath.Join("bin", "cls"))) // ~ expanded
	require.False(t, strings.HasPrefix(r.HookCmd[0], "~"))
	require.Nil(t, r.LaunchCmd)
}

func TestResolveRole(t *testing.T) {
	cfg := config.Default()
	r, note := ResolveRole(&cfg, nil, which())
	require.Nil(t, r)
	require.Equal(t, "off", note)
	noKeys(t)
	_, note = ResolveRole(&cfg, cfg.LaunchCheck.Classifier, which("npx"))
	require.True(t, strings.HasPrefix(note, "jev: no backend key"), note)
}

// --- launch check through Jev ---------------------------------------------

var jevR = &Resolved{Name: "jev", Kind: "jev", LaunchCmd: []string{"jev-use", "judge"}, Timeout: 5 * time.Second}

var pr = github.PR{Title: "T", Author: &github.User{Login: "bob"}}

func item(body string) github.Activity {
	return github.Activity{Kind: "comment", ID: new(int64(1)), User: new("alice"), At: "2026-01-01T00:00:00Z", Body: body, URL: new("https://x/c1")}
}

func request(items ...github.Activity) Request {
	if items == nil {
		items = []github.Activity{item("x")}
	}
	return NewRequest("o/r", 1, pr, "t", items, "Matthias", "me")
}

// fakeRun answers with outputs in turn, exiting 3 like an escalated jev-use.
func fakeRun(t *testing.T, outputs []string, seen *[]map[string]any) proc.Runner {
	return func(_ context.Context, c proc.Cmd) (proc.Result, error) {
		var payload map[string]any
		require.NoError(t, json.Unmarshal([]byte(c.Stdin), &payload))
		*seen = append(*seen, payload)
		out := outputs[0]
		outputs = outputs[1:]
		return proc.Result{Stdout: out, Code: 3}, &proc.Error{Args: c.Args, Code: 3}
	}
}

func TestJevLaunchDecisions(t *testing.T) {
	for _, tc := range []struct {
		answer string
		launch bool
		note   string
	}{
		{`{"verdicts": [{"id": "act", "escalate": false, "answer": 0.04, "confidence": 0.92}]}`, false, "jev p=0.04 conf=0.92"},
		{`{"verdicts": [{"id": "act", "escalate": false, "answer": 0.89, "confidence": 0.78}]}`, true, "jev p=0.89 conf=0.78"},
		{`{"verdicts": [{"id": "act", "escalate": true, "answer": 0.1, "reason": "unsure"}]}`, true, "jev p=0.1 conf=None unsure (unsure), launching anyway"},
		{`{}`, true, "jev: unexpected answer, launching anyway"},
		{`{"verdicts": []}`, true, "jev: unexpected answer, launching anyway"},
		{`{"verdicts": [{"answer": "yes"}]}`, true, "jev p=yes conf=None unexpected answer, launching anyway"},
		{`{"verdicts": [{"answer": true}]}`, true, "jev p=True conf=None unexpected answer, launching anyway"},
		{`[1]`, true, "jev: unexpected answer, launching anyway"},
		{`not json`, true, "jev unavailable, launching anyway"},
	} {
		var seen []map[string]any
		launch, note := CheckLaunch(t.Context(), jevR, request(), 0.5, fakeRun(t, []string{tc.answer}, &seen))
		require.Equal(t, tc.launch, launch, tc.answer)
		require.Contains(t, note, tc.note, tc.answer)
		q := seen[0]["questions"].([]any)[0].(map[string]any)
		require.Equal(t, "noul", q["type"])
		require.Contains(t, q["question"], "working for Matthias")
		require.NotContains(t, seen[0], "confidence_threshold")
	}
}

func TestJevPayloadCarriesConfidenceThreshold(t *testing.T) {
	var seen []map[string]any
	r := *jevR
	r.ConfidenceThreshold = new(0.7)
	CheckLaunch(t.Context(), &r, request(), 0.5, fakeRun(t, []string{`{"verdicts": [{"answer": 1}]}`}, &seen))
	require.InDelta(t, 0.7, seen[0]["confidence_threshold"], 0)
	require.Contains(t, seen[0]["state"], "GitHub pull request o/r#1: T")
}

func TestUnreachableLaunches(t *testing.T) {
	r := *jevR
	r.LaunchCmd = []string{"definitely-not-installed-jev"}
	launch, note := CheckLaunch(t.Context(), &r, request(), 0.5, proc.Exec)
	require.True(t, launch)
	require.Contains(t, note, "unavailable")
}

func TestRequestKeepsNewestWhenTrimming(t *testing.T) {
	var items []github.Activity
	for i := range 60 {
		items = append(items, item(fmt.Sprintf("old %d ", i)+strings.Repeat("x", 1400)))
	}
	items = append(items, item("NEWEST"))
	state := request(items...).StateText
	require.Contains(t, state, "NEWEST")
	require.NotContains(t, state, "old 0 ")
	require.Less(t, len(state), 45000)
}

func TestRequestMentionsOwnershipAndFailingChecks(t *testing.T) {
	own := pr
	own.Author = &github.User{Login: "ME"}
	own.StatusCheckRollup = []github.Check{{Name: "unit", Conclusion: "FAILURE"}}
	req := NewRequest("o/r", 1, own, "t", nil, "Matthias", "me")
	require.Contains(t, req.StateText, "Matthias's own PR")
	require.Equal(t, []string{"unit"}, req.FailingChecks)
	require.True(t, req.Own)
	require.Contains(t, req.StateText, "- (none)")
}

// --- launch check through a local command ---------------------------------

func TestLocalClassifierProtocol(t *testing.T) {
	for _, tc := range []struct {
		answer string
		launch bool
	}{
		{`{"launch": false, "reason": "only a bot"}`, false},
		{`{"launch": true}`, true},
		{`{"probability": 0.1}`, false},
		{`{"probability": 0.9}`, true},
		{`{"probability": true}`, true}, // not a number: fail open
		{"garbage", true},
	} {
		dir := t.TempDir()
		t.Setenv("CLS_ANSWER", tc.answer)
		t.Setenv("CLS_REQUEST", filepath.Join(dir, "request.json"))
		r := &Resolved{Name: "local", Kind: "command", LaunchCmd: []string{os.Args[0], "launch"}, Timeout: 10 * time.Second}
		launch, note := CheckLaunch(t.Context(), r, request(), 0.5, proc.Exec)
		require.Equal(t, tc.launch, launch, tc.answer)
		var sent map[string]any
		raw, err := os.ReadFile(filepath.Join(dir, "request.json"))
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &sent))
		require.InDelta(t, 1, sent["version"], 0)
		require.Equal(t, "o/r", sent["repo"])
		require.Equal(t, "x", sent["activity"].([]any)[0].(map[string]any)["body"])
		require.Contains(t, sent, "question")
		if strings.Contains(tc.answer, "reason") {
			require.Contains(t, note, "only a bot")
			require.Equal(t, "local: launch=False (only a bot)", note)
		}
	}
}

// --- tool gate environment -------------------------------------------------

func TestHookEnvGenericAndJev(t *testing.T) {
	local := &Resolved{Name: "l", Kind: "command", HookCmd: []string{"h"}}
	require.Equal(t, map[string]string{
		"OUTRIDER_POLICY_FILE":    "/p.json",
		"OUTRIDER_GATE_TEXT":      "rules",
		"OUTRIDER_GATE_THRESHOLD": "0.7",
	}, HookEnv(local, "rules", new(0.7), "/p.json"))
	env := HookEnv(jevR, "rules", nil, "/p.json")
	require.Equal(t, "rules", env["JEV_GATE_STATE"])
	require.NotContains(t, env, "JEV_GATE_THRESHOLD")
	require.Equal(t, "1.0", HookEnv(jevR, "r", new(1.0), "/p")["JEV_GATE_THRESHOLD"])
}

// --- parity with the Python version ----------------------------------------

func TestRequestsMatchThePythonVersion(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/python-parity.json")
	require.NoError(t, err)
	var golden struct {
		Requests []struct {
			Args map[string]any `json:"args"`
			Req  map[string]any `json:"req"`
		} `json:"requests"`
	}
	require.NoError(t, json.Unmarshal(raw, &golden))
	items := []github.Activity{
		{Kind: "comment", ID: new(int64(1)), User: new("alice"), At: "2026-01-01T00:00:00Z", Body: "  hello\nworld  "},
		{Kind: "review", ID: new(int64(2)), State: new("APPROVED"), User: new("bob"), At: "2026-01-02T00:00:00Z"},
		{Kind: "inline comment", ID: new(int64(3)), User: new("carol"), Path: new("a/b.go"), At: "2026-01-03T00:00:00Z",
			Body: "fix this ünïcode 🚀 <&>", URL: new("https://x/c3")},
	}
	checks := []github.Check{{Name: "unit", Conclusion: "FAILURE"}, {Context: "ci/x", State: "ERROR"}, {Name: "ok", Conclusion: "SUCCESS"}}
	for _, g := range golden.Requests {
		var req Request
		if g.Args["big"] == true {
			var big []github.Activity
			for i := range 40 {
				big = append(big, github.Activity{Kind: "comment", ID: new(int64(i)), User: new("u"),
					At: fmt.Sprintf("2026-01-01T00:00:%02dZ", i), Body: fmt.Sprintf("old %d ", i) + strings.Repeat("x", 1400)})
			}
			big = append(big, github.Activity{Kind: "comment", ID: new(int64(99)), User: new("u"), At: "2026-02-01T00:00:00Z", Body: "NEWEST"})
			bob := github.PR{Title: "T", URL: "https://github.com/o/r/pull/1", Author: &github.User{Login: "bob"}}
			req = NewRequest("o/r", 1, bob, "t", big, "Matthias", "me")
			sum := sha256.Sum256([]byte(req.StateText))
			require.Equal(t, g.Req["head"], req.StateText[:300])
			require.Equal(t, g.Req["state_text_sha256"], hex.EncodeToString(sum[:]))
			continue
		}
		p := github.PR{Title: "T", URL: "https://github.com/o/r/pull/1", Author: &github.User{Login: g.Args["author"].(string)}, StatusCheckRollup: checks}
		var use []github.Activity
		if g.Args["n"].(float64) > 0 {
			use = items
		}
		req = NewRequest("o/r", 1, p, "my PR notification (comment)", use, "Matthias", "me")
		b, err := json.Marshal(req)
		require.NoError(t, err)
		var got map[string]any
		require.NoError(t, json.Unmarshal(b, &got))
		require.Equal(t, g.Req, got, g.Args)
	}
}
