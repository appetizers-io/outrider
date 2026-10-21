# Outrider for Kiro Crew — release preparation

The app is distributed from `apps/kiro-crew` in this repository. The root
[`app-registry.json`](../../../app-registry.json) supports direct installation
through a custom registry; it is not an official marketplace listing. Keep its
explicit `outrider` identity: directory auto-discovery would infer `kiro-crew`,
which does not match the manifest.

The official catalog is maintainer-curated. Submit an
[App Store listing request](https://github.com/kirodotdev/KiroCrew/issues/new?template=app-store-listing.yml)
with the repository URL, source ref, `apps/kiro-crew` subdirectory, category,
author credit, and review checklist. A maintainer authors the entry in
`kirodotdev/KiroCrewApps`; its publish pipeline pins the source commit and reads
our app version. No Kiro Crew release is required. A registry PR against the
KiroCrew source repository does not submit an app to the official catalog.

Official references: [publishing](https://kiro.dev/docs/crew/apps/publishing/)
and [manifest](https://kiro.dev/docs/crew/apps/manifest/).

## Release contents

- Versioned manifest, Apache-2.0 license, store icon, banner and feature highlights.
- Committed build-free UI modules, Python backend, two agents, and a launcher for the Go MCP server.
- `task crew:package` creates `build/crew/outrider-crew-<version>.tar.gz` and a SHA-256
  sidecar. Only runtime files are included; user state, credentials, tests and
  caches are excluded. `release-info.json` records the source commit and whether
  the checkout was dirty. Publish only an artifact marked `dirty: false`.
- The Release workflow publishes the CLI with GoReleaser first, then calls
  **Publish Crew app**. It downloads all six published runtime archives and
  verifies their SHA-256 checksums, stages the matching app version and runtime
  lock outside the checkout, tests the app, and packages it with `--marketplace`.
  The pinned archive and its checksum are attached to the same release.
- Only after package upload succeeds, automation opens a signed bot PR updating
  `app.json` and `release/runtime.json` on main. Packaging, release staging and
  the source-update workflow validate the canonical registry against the manifest. The marketplace installs Git
  source, so this PR must be reviewed and merged before source installs update.
  The package records the release commit and pinned runtime version; generating
  the lock does not mark the release checkout dirty.

## Requirements and release gates

1. Run **Release** from main. No manual checksum copying or pin commits are
   needed. CLI and Homebrew publication completes before Crew packaging; if the
   Crew job fails, the CLI remains published but no source-update PR is opened.
2. Enable **Allow GitHub Actions to create and approve pull requests** in the
   repository's Actions settings (organization policy must also allow it).
   The workflow requests contents and pull-request write permissions and uses
   GitHub-signed bot commits. It never approves or merges the resulting PR.
   With the default `GITHUB_TOKEN`, the bot PR does not trigger PR CI: a
   maintainer must close and reopen it to run required checks before merging.
   Review the pin/version diff before merging. Existing live-install validation
   is still required; checksum verification is not an end-to-end Crew test.
   To recover a failed Crew step, dispatch **Publish Crew app** on main with the
   same published version. It uses that tag's source, replaces the deterministic
   package assets, and updates the same version-specific PR. It refuses draft,
   prerelease, malformed versions and source-version downgrades. Use only tags
   containing this release automation. Release assets must remain mutable for
   the post-publication attachment; immutable releases need a different flow.
   Test with no host Outrider: the wizard downloads the pinned archive, verifies
   its SHA-256 against the committed lock, and stores only its executable under
   app data. Git and authenticated GitHub CLI remain external prerequisites.
3. Use Kiro Crew 0.9.0 or later (the tested baseline). Install the extracted
   `outrider` directory through Crew's local app installer, or select the repo
   and `apps/kiro-crew` subdirectory when installing from source.
4. On a fresh Crew profile, verify first launch, dependency errors, enable-time
   permission disclosure, repository setup, pause/restart persistence and one
   controlled session. Verify macOS, Linux, and Windows before advertising them as
   end-to-end tested. CI backend tests alone do not establish live Crew support.
5. Recheck the opaque 512×512 store icon, hero image, and committed product
   screenshot. The queue preview uses labelled sample data rendered by the app's
   components, rather than private repository information. Document Crew's
   third-party execution prerequisite in the app README.
6. Request the official listing only after the compatible CLI release and app
   source are available. Supply the commit after the runtime-pin PR and listing
   assets have merged, not the earlier CLI release tag. For subsequent official
   catalog updates, bump `app.json`, publish the runtime and merge its pin PR,
   then request catalog republication with the new source commit. Pushing main
   alone updates custom-registry sources, not the official catalog's pinned copy.

## Permission disclosure

The app reads GitHub PR activity via your authenticated `gh`, stores a local queue,
creates chats in selected Crew folders, and can prepare isolated Git worktrees.
Optional automatic sessions consume the selected Crew model. Session trust changes
use Crew's explicit session-approval grant; the plugin cannot silently grant it.
Push/post policies constrain session tasks, while Crew controls tool approvals.
MCP can read state, draft configuration and register repositories in existing Crew folders on request. Registration inherits the current polling and automatic-session settings; it cannot change trust.

## Release notes

First Crew integration: persistent polling and PR queue; per-repository triggers
and workflows; CLI YAML import preserving ownership/task policies; isolated local
sessions with selectable Crew members, trust and concurrency; session links,
retry/dismiss controls, activity log, and optional text-only review and AI setup.

Existing development installs retain their data. New prompt behavior applies only
to new sessions. Reimport CLI YAML to recover fields removed by earlier importer
versions; review the global polling values before saving.
