package session

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// runAgent warns at the deadline, then escalates Interrupt → TERM → Kill.
// Grace is injectable so timeout behaviour can be exercised without minutes.
func runAgent(cmd *exec.Cmd, budget, grace time.Duration, stdout io.Writer) (bool, error) {
	if budget <= 0 {
		return false, cmd.Run()
	}
	if err := cmd.Start(); err != nil {
		return false, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case err := <-done:
		return false, err
	case <-timer.C:
	}
	_, _ = fmt.Fprintln(stdout, "outrider: review time limit reached; stopping agent")
	_ = cmd.Process.Signal(os.Interrupt)
	timer.Reset(grace)
	select {
	case err := <-done:
		return true, err
	case <-timer.C:
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	timer.Reset(grace)
	select {
	case err := <-done:
		return true, err
	case <-timer.C:
	}
	_ = cmd.Process.Kill()
	return true, <-done
}

func reviewBudget(review *Review) time.Duration {
	if review == nil {
		return 0
	}
	return time.Duration(review.MaxMinutes) * time.Minute
}
