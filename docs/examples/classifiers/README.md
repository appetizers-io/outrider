# Example classifiers

Two POSIX `sh` scripts for a `kind: command` classifier. Use them when you
want your own rules instead of Jev, or as a starting point for one.

| File | Role | What it does |
|---|---|---|
| [`launch-check.sh`](launch-check.sh) | `launch_command` | skips a session when all new activity is from `[bot]` logins and CI is green |
| [`gate-hook.sh`](gate-hook.sh) | `hook_command` | blocks force pushes, `rm -rf` and edits under `generated/` |

Copy them and wire them up with
[`custom-classifier.yaml`](../configs/custom-classifier.yaml):

```sh
mkdir -p ~/.config/outrider && cp docs/examples/classifiers/*.sh ~/.config/outrider/
outrider --config docs/examples/configs/custom-classifier.yaml
```

The request and answer formats are in [Classifiers](../../classifiers.md).
