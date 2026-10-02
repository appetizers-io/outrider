# Example configs

Each file is short and only sets the keys that matter for its use case; the
rest stay at their defaults. Replace `my-org/*` with your repos.

| File | Use it when you want to |
|---|---|
| [`minimal.yaml`](minimal.yaml) | start with the smallest useful config |
| [`reviewer.yaml`](reviewer.yaml) | mostly review others' PRs (👀 and @mentions, review only, posts ask) |
| [`own-prs.yaml`](own-prs.yaml) | fix CI and review feedback on your own PRs (push and posts ask) |
| [`autonomous.yaml`](autonomous.yaml) | let the agent push and post without asking, on a trusted repo |
| [`read-only.yaml`](read-only.yaml) | keep everything local: no pushes, no posts |
| [`codex.yaml`](codex.yaml) | use Codex instead of Claude Code |
| [`custom-classifier.yaml`](custom-classifier.yaml) | use your own launch check and tool gate instead of Jev |
| [`many-repos.yaml`](many-repos.yaml) | watch many repos with include/exclude globs and ignore bots |
| [`headless.yaml`](headless.yaml) | run as a background service (tmux, no dialogs) |

Try one without side effects, then make it yours:

```sh
outrider --config docs/examples/configs/reviewer.yaml --dry-run --once
cp docs/examples/configs/reviewer.yaml ~/.config/outrider/config.yaml
```

Every key is explained in [Configuration](../../configuration.md).
