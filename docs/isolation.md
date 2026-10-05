# Docker Sandboxes sessions

Outrider can launch Claude Code or Codex through the existing
[Docker Sandboxes runtime](https://docs.docker.com/ai/sandboxes/).
It uses `sbx`, not a custom Docker image or an Outrider container runtime.
This is opt-in; normal sessions keep their existing behavior.

This integration is currently a draft. Reusing an existing OpenPGP signing key
is not working in the live sandbox verification; do not enable this for workflows
that require GPG-signed commits until the forwarding issue is resolved.

```yaml
mode: autonomous
push: never                  # or allow; desktop dialogs cannot run in the VM
github_writes: never         # or allow
network_access: true         # null inherits Docker's network policy
isolation:
  enabled: true
  guard_binary: null         # task build/install provide the Linux helper
  read_only: []              # optional additional absolute host paths
```

`isolation` also works in per-PR overrides. `mode` is global, so use explicit
push/post settings for sessions where you want tighter controls. It cannot be
combined with `sandbox: read-only`, which forbids even private workspace edits.

## Install

Install [Docker Sandboxes](https://docs.docker.com/ai/sandboxes/install/) and
sign in once with `sbx login`. On Apple silicon macOS:

```sh
brew install docker/tap/sbx
sbx login
sbx policy init balanced     # first installation only
task build
bin/outrider doctor
```

`task build` and `task install` build a Linux Outrider helper for your machine's
architecture alongside the host executable. It runs the same Git/GitHub guards
inside the VM. If using a release archive, extract the matching Linux Outrider
binary from the **same version** and set `isolation.guard_binary` to its absolute
path. The host executable cannot run in a Linux VM on macOS or Windows.
A missing runtime, Docker login, or Linux helper refuses the session; Outrider
never falls back to running that agent on the host.

## What the agent can access

Docker creates a microVM with a private writable Git clone (`sbx create --clone`).
The original repository is mounted read-only by Docker at `/run/sandbox/source`;
ignored and untracked files there are readable too. Outrider does not create a
host worktree for these sessions. The agent's home, tools, Docker engine, and
workspace are writable **inside** the VM. The host home, host Docker socket and
writable host directories are not mounted. Extra `isolation.read_only` paths
always receive `:ro`; there is no arbitrary Docker-argument escape hatch.

A trusted Outrider process snapshots the selected agent's settings, login files,
instructions, skills, commands, agents and plugins into private host inputs.
Only these inputs are mounted read-only; watcher environment files, agent history
and unrelated host configuration are excluded. Inside the VM, they seed a
writable agent home. Subsequent token refreshes, histories and setting changes
stay inside that VM. New launches take fresh snapshots of host settings.
Symlinks in imported skill/plugin trees are preserved without following their
host targets; mount required targets explicitly through `read_only`.

This shares settings, not previous conversation history. Settings that invoke
host-only executables or paths need Linux equivalents available inside the VM.
Local MCP servers and hooks run inside the VM, never through a host MCP gateway.

## Login, GitHub, SSH and signing

* Codex imports `auth.json`, `config.toml` and `AGENTS.md` from `CODEX_HOME`, or
  `~/.codex`. File-backed subscription logins are reused. Keychain-only Codex
  logins need a file-backed login for this import route.
* Claude imports `settings.json`, `.credentials.json`, `CLAUDE.md` and
  `~/.claude.json`. On macOS, the trusted host launcher reads the existing
  `Claude Code-credentials` Keychain item and places only that credential in the
  private read-only input. It never mounts the Keychain itself. A custom
  `CLAUDE_CONFIG_DIR` needs a file-backed `.credentials.json`.
* Existing `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_API_KEY` and `OPENAI_API_KEY`
  environment variables are forwarded by name when set; Outrider does not store
  their values in session metadata. Configure the same authentication source as
  your native agent. A Docker-global provider secret may affect provider
  authentication; remove conflicting secrets when using your subscription login.
* GitHub uses Docker's sandbox-scoped credential proxy with the trusted host
  `gh auth token` command. The command runs on the host; the returned token stays
  in Docker's credential broker, outside agent environment and session metadata.
* Docker forwards `SSH_AUTH_SOCK` for Git authentication and SSH commit signing.
  Private keys remain in the host SSH agent. The VM can request signatures,
  including for remote operations, so this shares your Git identity and account
  capabilities. No `.ssh` directory or private-key files are copied.
* Portable Git identity and signing settings are imported. SSH signing uses the
  configured public key, including public-key files converted to `key::...`.
  OpenPGP signing has an experimental bridge to the host GPG agent’s restricted
  extra socket. Only the public key is imported; private-key files remain on the
  host. Live signing verification currently fails with “No secret key”, so this
  path is not ready for use. Host-only signing programs are not imported.

Outrider keeps the PR ownership prompt and Git/GitHub guards, including review
fork restrictions. Explicit `never` still blocks the guarded commands; `allow`
permits them without a dialog. Host dialogs and Outrider tool classifiers are
not available in this autonomous mode. These command guards are workflow
controls; the microVM and read-only mounts enforce host filesystem isolation.
Sharing a credential grants its remote capabilities; host filesystem isolation
does not prevent a permitted remote Git push or GitHub post.

`network_access: true` adds a sandbox-scoped outbound TCP allow rule; false blocks
agent network access after trusted PR checkout/setup; null keeps Docker's policy
plus the GitHub hosts needed for checkout. Loopback, common host gateway names,
and private address ranges are denied for these sandboxes. Docker's network
rules are separate from its filesystem isolation: an explicitly allowed hostname
can override an IP-range rule for DNS-resolved traffic. Do not treat this setting
as a complete protection for host or LAN services exposed over network names.
See [Docker's policy behavior](https://docs.docker.com/ai/sandboxes/security/defaults/).

## Inspect and retrieve results

The terminal shows the generated sandbox name. Sandboxes are retained after the
agent exits, including their writable state and subscription credential copy:

```sh
sbx ls
sbx exec -it SANDBOX bash
git fetch sandbox-SANDBOX
# Inspect a branch before merging it into your host checkout.
git diff main..sandbox-SANDBOX/review/pr-123
sbx cp SANDBOX:/path/to/result ./result
sbx rm SANDBOX
```

The Git daemon is reachable only while the sandbox is running. Outrider never
copies agent changes back into the host checkout automatically. Remove completed
sandboxes and their private session inputs when you no longer need them.
Concurrent native/sandbox OAuth refreshes may require signing in again; copied
credentials do not provide a shared writable host token store.
