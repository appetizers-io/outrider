# Contributing

Thanks for helping. Bug reports, docs fixes and pull requests are welcome.

## Before you start

- For anything bigger than a small fix, open an issue first so we can agree on
  the approach.
- Security problems: please contact a maintainer
  ([MAINTAINERS.md](MAINTAINERS.md)) privately instead of opening an issue.

## Making a change

1. Fork and branch from `main`.
2. Make the change with tests. See [docs/development.md](docs/development.md)
   for the tasks and the code layout.
3. Run `task all` (lint, vet, tests with the race detector, build). If you
   changed `internal/config`, also run `task schema` and update
   `internal/config/config.example.yaml`.
4. Open a pull request.

## Pull requests

- **The title is a [Conventional Commit](https://www.conventionalcommits.org):**
  `feat: …`, `fix: …`, `docs: …`, `chore: …`, `refactor: …`, `test: …`,
  `ci: …`. PRs are squash-merged with that title, and the release notes and the
  next version are built from it.
- Link the issue (`Closes #123`).
- Keep it focused; one change per PR.
- CI must pass (lint, typecheck, tests on Linux, macOS and Windows).
- Every PR needs a maintainer's approving review before it can merge.

## Docs

User docs live in [`docs/`](docs/README.md), plain Markdown. Check every flag,
key and command against the real binary, keep sentences short, and use tables
for options. `task test` checks the docs' relative links and runs the example
scripts.
