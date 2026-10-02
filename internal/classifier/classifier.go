// Package classifier holds the decision backends for the launch check and the
// tool gate.
//
// A classifier is resolved once at startup into the commands it can run.
// Unavailable ones (disabled, binary missing, Jev without a backend key)
// resolve to nil with a reason, and their roles are simply off.
//
// Launch check protocol for `kind: command`: the Request is written to stdin
// as JSON; the command prints {"launch": bool} or {"probability": 0..1},
// optionally with "reason". Anything else, a timeout or a failure launches.
//
// The tool gate is a Claude Code / Codex PreToolUse hook. It gets the
// session's rules in $OUTRIDER_GATE_TEXT and the full session policy
// as JSON in $OUTRIDER_POLICY_FILE (Jev reads $JEV_GATE_STATE, set to
// the same text).
package classifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kballard/go-shellquote"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/proc"
	"github.com/appetizers-io/outrider/internal/shell"
)

// Question is what the launch check asks about the new activity.
const Question = "Should a coding agent working for %[1]s act on this PR now: a concrete " +
	"change request, a question that needs %[1]s's answer, a reply in their " +
	"review thread that needs a response, or a failing CI check on their own PR?"

// Resolved is a classifier that can run.
type Resolved struct {
	Name                string
	Kind                string // jev | command
	LaunchCmd           []string
	HookCmd             []string
	Timeout             time.Duration
	ConfidenceThreshold *float64 // jev only
}

// LookPath finds a binary, like exec.LookPath.
type LookPath func(file string) (string, error)

func split(cmd string) ([]string, error) {
	parts, err := shellquote.Split(cmd)
	if err != nil {
		return nil, fmt.Errorf("split %q: %w", cmd, err)
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty command %q", cmd)
	}
	if p := parts[0]; p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			parts[0] = filepath.Join(home, p[1:])
		}
	}
	return parts, nil
}

// Resolve returns the runnable classifier, or nil with the reason it is off.
func Resolve(name string, c config.Classifier, lookPath LookPath) (*Resolved, string) {
	available := func(cmd []string) bool { _, err := lookPath(cmd[0]); return err == nil }
	if c.Disabled() {
		return nil, "disabled"
	}
	if c.Kind == config.KindJev {
		j := c.Jev
		var base []string
		switch {
		case j.Command != nil:
			var err error
			if base, err = split(*j.Command); err != nil {
				return nil, err.Error()
			}
		case available([]string{"jev-use"}):
			base = []string{"jev-use"}
		default:
			base = []string{"npx", "-y", "jev-use@0.8.0"}
		}
		if !available(base) {
			return nil, base[0] + " not found"
		}
		if j.Enabled == config.EnabledAuto && !config.JevBackendConfigured() {
			return nil, "no backend key (" + strings.Join(config.JevBackendEnv, ", ") + ")"
		}
		return &Resolved{
			Name:                name,
			Kind:                config.KindJev,
			LaunchCmd:           append(append([]string{}, base...), "judge"),
			HookCmd:             append(append([]string{}, base...), "hook", "gate"),
			Timeout:             time.Duration(j.TimeoutSeconds) * time.Second,
			ConfidenceThreshold: j.ConfidenceThreshold,
		}, shell.Join(base...)
	}
	r := &Resolved{Name: name, Kind: config.KindCommand, Timeout: time.Duration(c.Command.TimeoutSeconds) * time.Second}
	for _, cmd := range []struct {
		src *string
		dst *[]string
	}{{c.LaunchCommand, &r.LaunchCmd}, {c.HookCommand, &r.HookCmd}} {
		if cmd.src == nil {
			continue
		}
		parts, err := split(*cmd.src)
		if err != nil {
			return nil, err.Error()
		}
		if !available(parts) {
			return nil, parts[0] + " not found"
		}
		*cmd.dst = parts
	}
	return r, "command"
}

// ResolveRole resolves the classifier a role names; nil name means the role is off.
func ResolveRole(cfg *config.Config, name *string, lookPath LookPath) (*Resolved, string) {
	if name == nil {
		return nil, "off"
	}
	r, note := Resolve(*name, cfg.Classifiers[*name], lookPath)
	return r, *name + ": " + note
}

func number(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	return f, err == nil
}

// CheckLaunch asks the classifier whether to launch: (launch, note). Anything
// but a confident "no" launches.
func CheckLaunch(ctx context.Context, r *Resolved, req Request, skipBelow float64, run proc.Runner) (bool, string) {
	var payload any = req
	if r.Kind == config.KindJev {
		p := map[string]any{
			"state": req.StateText,
			"questions": []any{map[string]any{
				"id":       "act",
				"type":     "noul",
				"question": req.Question,
				"criteria": map[string]string{
					"true": "at least one item needs a code change or a response from " + req.Owner,
					"false": "only bot summaries, approvals, LGTMs, thanks, acknowledgements, " +
						req.Owner + "'s own activity, or nothing",
				},
			}},
		}
		if r.ConfidenceThreshold != nil {
			p["confidence_threshold"] = *r.ConfidenceThreshold
		}
		payload = p
	}
	in, err := marshalNoEscape(payload)
	if err != nil {
		return true, fmt.Sprintf("%s unavailable, launching anyway: %v", r.Name, err)
	}
	// jev-use exits 3 when escalated; the verdict is still on stdout
	res, err := run(ctx, proc.Cmd{Args: r.LaunchCmd, Stdin: string(in), Timeout: r.Timeout})
	if err != nil && res.Code < 0 {
		return true, fmt.Sprintf("%s unavailable, launching anyway: %v", r.Name, err)
	}
	var answer any
	dec := json.NewDecoder(strings.NewReader(res.Stdout))
	dec.UseNumber()
	if err := dec.Decode(&answer); err != nil {
		return true, fmt.Sprintf("%s unavailable, launching anyway: invalid JSON: %v", r.Name, err)
	}
	obj, ok := answer.(map[string]any)
	if !ok {
		return true, r.Name + ": unexpected answer, launching anyway"
	}
	if r.Kind == config.KindJev {
		return jevVerdict(r.Name, obj, skipBelow)
	}
	reason := ""
	if v := obj["reason"]; v != nil && v != "" && v != false {
		reason = fmt.Sprintf(" (%v)", v)
	}
	if launch, ok := obj["launch"].(bool); ok {
		return launch, fmt.Sprintf("%s: launch=%t%s", r.Name, launch, reason)
	}
	if p, ok := number(obj["probability"]); ok {
		return p >= skipBelow, fmt.Sprintf("%s: p=%v%s", r.Name, obj["probability"], reason)
	}
	return true, r.Name + ": unexpected answer, launching anyway"
}

func jevVerdict(name string, answer map[string]any, skipBelow float64) (bool, string) {
	verdicts, _ := answer["verdicts"].([]any)
	if len(verdicts) == 0 {
		return true, name + ": unexpected answer, launching anyway"
	}
	v, ok := verdicts[0].(map[string]any)
	if !ok {
		return true, name + ": unexpected answer, launching anyway"
	}
	note := fmt.Sprintf("%s p=%v conf=%v", name, orUnknown(v["answer"]), orUnknown(v["confidence"]))
	if esc, _ := v["escalate"].(bool); esc {
		return true, fmt.Sprintf("%s unsure (%v), launching anyway", note, orUnknown(v["reason"]))
	}
	raw, present := v["answer"]
	if !present {
		return true, note // no answer: launch
	}
	p, ok := number(raw)
	if !ok {
		return true, note + " unexpected answer, launching anyway"
	}
	return p >= skipBelow, note
}

// orUnknown is a JSON value for a log note; a missing one reads "unknown".
func orUnknown(v any) any {
	if v == nil {
		return "unknown"
	}
	return v
}

// HookEnv is the environment the tool-gate hook runs with.
func HookEnv(r *Resolved, gateText string, threshold *float64, policyFile string) map[string]string {
	env := map[string]string{
		"OUTRIDER_POLICY_FILE": policyFile,
		"OUTRIDER_GATE_TEXT":   gateText,
	}
	if threshold != nil {
		env["OUTRIDER_GATE_THRESHOLD"] = strconv.FormatFloat(*threshold, 'f', -1, 64)
	}
	if r.Kind == config.KindJev {
		env["JEV_GATE_STATE"] = gateText
		if threshold != nil {
			env["JEV_GATE_THRESHOLD"] = strconv.FormatFloat(*threshold, 'f', -1, 64)
		}
	}
	return env
}

// marshalNoEscape is json.Marshal without HTML escaping.
func marshalNoEscape(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}
