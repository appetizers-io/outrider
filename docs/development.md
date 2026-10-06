# Development

Needs Go 1.27, [Task](https://taskfile.dev) and
[golangci-lint](https://golangci-lint.run) v2. CI runs the same tasks.

| Task | Does |
|---|---|
| `task all` | lint + vet + test + build; run it before a PR |
| `task lint` | golangci-lint |
| `task vet` | `go vet` for linux, darwin and windows |
| `task test` | `go test -race ./...` (also runs the `docs/examples` scripts and checks the docs' links and config snippets) |
| `task build` | `bin/outrider` |
| `task install` | build into `~/.local/bin` (`INSTALL_DIR=…` to change) |
| `task schema` | regenerate `config.schema.json` after changing `internal/config` |
| `task schema:check` | fail if `config.schema.json` is out of date |
| `task golden` | rewrite the golden files after an intended change to the prompt, the gate rules or the launch-check request; review the diff |
| `task changelog` | preview the next version and its release notes (needs git-cliff, gh) |
| `task release:snapshot` | local GoReleaser build of all archives into `dist/` |

## Layout

| Path | What |
|---|---|
| `main.go` | multi-call dispatch: run as `gh`/`git` it is a guard, else the CLI |
| `internal/cli` | cobra commands and flags |
| `internal/watch` | startup checks and the poll loop |
| `internal/doctor` | `outrider doctor`: setup checks, using the startup resolution from `watch` |
| `internal/config` | config structs, schema generation, loading, `config.example.yaml` |
| `internal/github` | a thin `gh` wrapper |
| `internal/poll` | triggers and launch decisions |
| `internal/session` | worktrees, prompt, policy, agent settings, launchers, the session runner |
| `internal/guard` | the `gh` and `git` guards |
| `internal/approve` | native approval dialogs |
| `internal/classifier` | launch check and tool-gate classifiers |
| `internal/migrate` | the one-time move from the llm-review-agent directories |
| `prompts/` | the prompt template |
| `internal/*/testdata/` | golden files: prompts and tool-gate rules (`session`), launch-check requests (`classifier`), the activity fingerprint (`poll`) |
| `testdata/config-all-keys.yaml` | a config that sets every key; it must keep loading |

## Config changes

The structs in `internal/config` are the source: their `jsonschema` tags give
the constraints and descriptions. After a change run `task schema`, and update
`internal/config/config.example.yaml`; a test checks it lists every key at its
default.

## Releases

Releases are cut by hand: Actions → **Release** → Run workflow on `main`
(or `gh workflow run release.yml -f bump=auto`). It runs the tests, then
[git-cliff](https://git-cliff.org) (`cliff.toml`) picks the next version from
the Conventional Commit titles since the last tag (`feat:` minor, `fix:`
patch, or the bump you choose: `patch`, `minor`, `major`) and writes the
release notes. GoReleaser creates the tag and the release with the archives.
The run needs a maintainer's approval (the `release` environment).
The same run publishes the Homebrew cask and attests the archives' build
provenance.

### Homebrew tap

GoReleaser writes a cask (`homebrew_casks` in `.goreleaser.yaml`) and pushes
it to [`appetizers-io/homebrew-tap`](https://github.com/appetizers-io/homebrew-tap),
so `brew tap appetizers-io/tap && brew install --cask appetizers-io/tap/outrider` works. `task release:snapshot`
writes it to `dist/homebrew/Casks/outrider.rb` without publishing. The cask
depends on `gh` and `git`; its post-install hook removes the
`com.apple.quarantine` attribute, since the binaries are not signed or
notarized. The cask also lists the Linux archives, but Homebrew's cask
support on Linux is newer and not tested here; on Linux, use the release
archive if `brew install` refuses the cask.

The release skips the upload, and still succeeds, while the secret
`HOMEBREW_TAP_TOKEN` is not set. One-time setup by a maintainer:

1. Create the public repo `appetizers-io/homebrew-tap` (default branch `main`,
   a short README). GoReleaser writes into `Casks/`.
2. Create a token that can write to the tap and nothing else: a fine-grained
   personal access token (resource owner `appetizers-io`, only the
   `homebrew-tap` repository, permission Contents: read and write), or a
   GitHub App installed on the tap only with Contents: write.
3. Add it as a secret named `HOMEBREW_TAP_TOKEN` to this repo, or to the
   `release` environment (`gh secret set HOMEBREW_TAP_TOKEN -R
   appetizers-io/outrider --env release`). Only the GoReleaser step gets it.
4. Run a release, then check `brew tap appetizers-io/tap && brew install --cask appetizers-io/tap/outrider` and
   `outrider --version`.

### Build provenance

The release attests the archives and `checksums.txt` with
[`actions/attest-build-provenance`](https://github.com/actions/attest-build-provenance).
To verify a download:

```sh
gh attestation verify outrider_0.2.0_darwin_arm64.tar.gz -R appetizers-io/outrider
```

Attestations need a public repo, or GitHub Enterprise Cloud for a private
one. While the repo is private on another plan the step fails without
failing the release (`continue-on-error`), and there is nothing to verify.

## Golden files

Golden files pin what agents and classifiers get: the prompt for each kind of
session and the tool-gate rules (`internal/session/testdata/`), the
launch-check requests (`internal/classifier/testdata/`) and the activity
fingerprint (`internal/poll/testdata/`). The tests compare them with
[`gotest.tools/v3/golden`](https://pkg.go.dev/gotest.tools/v3/golden) and fail
when the output changes. After an intended change run `task golden`
(`go test <package> -run Golden -update`) and review the diff. The fingerprint
must not change: it is stored in `state.json`, and a different value
relaunches every watched PR once.

### Homebrew verification

On macOS arm64, a fresh cask install of v0.1.1 and an upgrade from v0.1.0 to
v0.1.1 were verified on 2026-10-06. Each installed binary printed its matching
release version. Use `brew upgrade --cask appetizers-io/tap/outrider` for updates.
The tap README is maintained in the
[public tap repository](https://github.com/appetizers-io/homebrew-tap).
Linux cask support and macOS amd64 installation have not been exercised locally;
use the release archives if your Homebrew version refuses the cask.
