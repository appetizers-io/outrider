package guard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// RunGate keeps a classifier's approval from overriding Claude's permission
// checks. Only ask and deny survive; malformed output or hook failures block.
func RunGate(stdin io.Reader, stdout, stderr io.Writer) int {
	_, p, err := trustedEnv()
	if err != nil || len(p.Guard.Hook) == 0 {
		_, _ = fmt.Fprintln(stderr, "outrider: tool gate policy unavailable")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.Guard.Hook[0], p.Guard.Hook[1:]...)
	cmd.Stdin, cmd.Stderr = stdin, stderr
	cmd.Env = os.Environ()
	for k, v := range p.Guard.HookEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	raw, err := cmd.Output()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "outrider: tool gate refused or failed")
		return 2
	}
	if len(raw) == 0 {
		return 0
	}
	out, err := gateOutput(raw)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "outrider: invalid tool gate response")
		return 2
	}
	_, _ = stdout.Write(out)
	return 0
}

func gateOutput(raw []byte) ([]byte, error) {
	var output map[string]any
	if err := json.Unmarshal(raw, &output); err != nil {
		return nil, err
	}
	if hook, ok := output["hookSpecificOutput"].(map[string]any); ok {
		if hook["permissionDecision"] == "allow" {
			delete(hook, "permissionDecision")
			delete(hook, "permissionDecisionReason")
		}
	}
	if output["decision"] == "approve" {
		delete(output, "decision")
		delete(output, "reason")
	}
	return json.Marshal(output)
}
