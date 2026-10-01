// Command llm-review-agent polls your GitHub notifications and opens a local
// interactive coding agent (Codex or Claude Code) for pull requests that need
// your attention.
//
// The binary is multi-call: run as `gh` or `git` (the guards on an agent
// session's PATH link to it) it is the gh or git guard.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/appetizers-io/llm-review-agent/internal/cli"
	"github.com/appetizers-io/llm-review-agent/internal/guard"
)

func main() {
	if run := guard.Command(os.Args[0]); run != nil {
		os.Exit(run(os.Args[1:]))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx)
	stop()
	os.Exit(code)
}
