# Outrider for Kiro Crew 0.1.0 — release preparation

The app is distributed from `apps/kiro-crew` in this repository. The App Store
entry is in [registry-entry.json](registry-entry.json), ready to add to
`src/kiro_crew/apps/app-registry.json` in `kirodotdev/KiroCrew` after the release
is available on main. This file is a submission draft, not a published listing.

Official references: [publishing](https://kiro.dev/docs/crew/apps/publishing/)
and [manifest](https://kiro.dev/docs/crew/apps/manifest/).

## Release contents

- Versioned manifest, Apache-2.0 license, store icon, banner and feature highlights.
- Committed build-free UI modules, Python backend, two agents, and a launcher for the Go MCP server.
- `task crew:package` creates `build/crew/outrider-crew-0.1.0.tar.gz` and a SHA-256
  sidecar. Only runtime files are included; user state, credentials, tests and
  caches are excluded. `release-info.json` records the source commit and whether
  the checkout was dirty. Publish only an artifact marked `dirty: false`.
- The existing Release workflow tests the Crew app, packages it, and attaches
  the archive/checksum beside the CLI binaries in the same GitHub release.

## Requirements and release gates

1. Merge PR #77 after all CI checks pass. Produce a normal Outrider CLI release
   from that commit using the existing Release workflow. Published CLI v0.1.1
   predates this integration and is not a compatible prerequisite.
2. Install that CLI release, Git, Python 3, and GitHub CLI; run `gh auth login`.
   Verify `outrider crew scan --help` and `outrider crew session-prompt --help`, and `outrider mcp --help`.
   The archive does not bundle these external commands or install them silently.
3. Use Kiro Crew 0.9.0 or later (the tested baseline). Install the extracted
   `outrider` directory through Crew's local app installer, or select the repo
   and `apps/kiro-crew` subdirectory when installing from source.
4. On a fresh Crew profile, verify first launch, dependency errors, enable-time
   permission disclosure, repository setup, pause/restart persistence and one
   controlled session. Verify both macOS and Linux before advertising both as
   end-to-end tested. CI backend tests alone do not establish live Crew support.
5. Recheck the store listing assets and capture current product screenshots if
   desired. No private repository screenshots are included in the release.
6. Submit the registry entry to KiroCrew only after the compatible CLI release
   and app source are available. The entry follows main; maintainers must keep
   main installable and bump `app.json` when publishing app updates.

## Permission disclosure

The app reads GitHub PR activity via your authenticated `gh`, stores a local queue,
creates chats in selected Crew folders, and can prepare isolated Git worktrees.
Optional automatic sessions consume the selected Crew model. Session trust changes
use Crew's explicit session-approval grant; the plugin cannot silently grant it.
Push/post policies constrain session tasks, while Crew controls tool approvals.
The shipped MCP tools only read state or return configuration drafts.

## Release notes

First Crew integration: persistent polling and PR queue; per-repository triggers
and workflows; CLI YAML import preserving ownership/task policies; isolated local
sessions with selectable Crew members, trust and concurrency; session links,
retry/dismiss controls, activity log, and optional text-only review and AI setup.

Existing development installs retain their data. New prompt behavior applies only
to new sessions. Reimport CLI YAML to recover fields removed by earlier importer
versions; review the global polling values before saving.
