package guard

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/appetizers-io/outrider/internal/shell"
)

// isolatedPush resolves immutable object IDs and pushes from a disposable bare
// repository. The source checkout's config, hooks, proxies and rewrites are
// never re-read by the network operation, closing the resolve/push race.
func isolatedPush(d GitDecision, runtime Runtime, env []string, stderr io.Writer) int {
	fail := func(err error) int { _, _ = fmt.Fprintln(stderr, "outrider guard:", err); return 1 }
	source := func(args ...string) (string, error) {
		cmd := exec.Command(runtime.Git, args...)
		cmd.Env = env
		raw, err := cmd.Output()
		return strings.TrimSpace(string(raw)), err
	}
	objects, err := source("rev-parse", "--path-format=absolute", "--git-path", "objects")
	if err != nil {
		return fail(err)
	}
	var refs []string
	for _, ref := range d.Refs {
		src, dst, _ := strings.Cut(ref, ":")
		oid, err := source("rev-parse", "--verify", "--end-of-options", src+"^{commit}")
		if err != nil {
			return fail(fmt.Errorf("cannot resolve push source %s: %w", src, err))
		}
		refs = append(refs, oid+":"+dst)
	}
	dir, err := os.MkdirTemp("", "outrider-push-")
	if err != nil {
		return fail(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	clean := []string{}
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "GIT_") || strings.EqualFold(key, "HTTP_PROXY") || strings.EqualFold(key, "HTTPS_PROXY") || strings.EqualFold(key, "ALL_PROXY") {
			continue
		}
		clean = append(clean, kv)
	}
	clean = append(clean, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	init := exec.Command(runtime.Git, "init", "--bare", "--template=", dir)
	init.Env, init.Stdout, init.Stderr = clean, io.Discard, stderr
	if err := init.Run(); err != nil {
		return fail(err)
	}
	if strings.ContainsAny(objects, "\r\n") {
		return fail(fmt.Errorf("invalid object directory"))
	}
	if err := os.WriteFile(filepath.Join(dir, "objects", "info", "alternates"), []byte(filepath.ToSlash(objects)+"\n"), 0o600); err != nil {
		return fail(err)
	}
	args := []string{"--git-dir=" + dir, "-c", "core.hooksPath=" + os.DevNull, "-c", "http.proxy=", "-c", "http.sslVerify=true", "-c", "http.followRedirects=false",
		"-c", "credential.helper=!" + shell.Join(runtime.GH, "auth", "git-credential")}
	if runtime.SSH != "" {
		args = append(args, "-c", "core.sshCommand="+shell.Join(runtime.SSH, "-F", os.DevNull))
	} else if strings.HasPrefix(d.URL, "git@") || strings.HasPrefix(d.URL, "ssh://") {
		return fail(fmt.Errorf("trusted ssh is unavailable"))
	}
	args = append(args, "push", "--no-verify")
	args = append(args, d.Flags...)
	args = append(args, "--", d.URL)
	args = append(args, refs...)
	cmd := exec.Command(runtime.Git, args...)
	cmd.Env, cmd.Stdout, cmd.Stderr = clean, os.Stdout, stderr
	if err := cmd.Run(); err != nil {
		return fail(err)
	}
	return 0
}
