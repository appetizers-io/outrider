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
| `task changelog` | preview the next version and its release notes (needs git-cliff, gh) |
| `task release:snapshot` | local GoReleaser build of all archives into `dist/` |

## Layout

| Path | What |
|---|---|
| `main.go` | multi-call dispatch: run as `gh`/`git` it is a guard, else the CLI |
| `internal/cli` | cobra commands and flags |
| `internal/watch` | startup checks and the poll loop |
| `internal/config` | config structs, schema generation, loading, `config.example.yaml` |
| `internal/github` | a thin `gh` wrapper |
| `internal/poll` | triggers and launch decisions |
| `internal/session` | worktrees, prompt, policy, agent settings, launchers, the session runner |
| `internal/guard` | the `gh` and `git` guards |
| `internal/approve` | native approval dialogs |
| `internal/classifier` | launch check and tool-gate classifiers |
| `internal/migrate` | the one-time move from the llm-review-agent directories |
| `prompts/` | the prompt template |
| `testdata/python-parity.json` | prompts, gate rules, launch-check requests and fingerprints the former Python version produced |

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
