# Forgejo

Outrider can render a review for a pull request on a Forgejo (or Gitea)
instance. `outrider render` prints the prompt and writes the pull request's
context files; the caller starts the agent itself, offline. The same
`workflows`, `review` profiles and `prompts.extra` as a GitHub setup apply.

Outrider does not watch Forgejo: no notifications, reactions, mentions or
review threads, and it posts nothing there. The caller posts the result.

## Setup

Add the forge to the config file under [`forges`](configuration.md#forges).
Pick one of two ways to reach it:

| `access` | Reads through | Token |
|---|---|---|
| `api` | the REST API (`/api/v1`) | from `token_env` or `token_file`, never from the config file itself |
| `fj` | the [fj](https://codeberg.org/forgejo-contrib/forgejo-cli) CLI | fj's own login; outrider never sees it |

For `access: api`, create an access token in Forgejo (Settings → Applications)
that can read repositories, issues and users, and put it in an environment
variable or a file only you can read:

```yaml
forges:
  - kind: forgejo
    host: forge.example.com
    access: api
    token_env: FORGEJO_TOKEN
```

For `access: fj`, log fj in once, with the same URL outrider uses:
`fj --host https://forge.example.com auth login` (or `auth add-token`).

```yaml
forges:
  - kind: forgejo
    host: forge.lan
    url: http://forge.lan:3000   # when the forge is not at https://<host>
    access: fj
```

`outrider doctor` reports each forge: whether it answers, whether the login
works, and as whom.

The token only goes into the `Authorization` header of API requests. It never
appears in a URL, a log line, an error message, the prompt or a context file.

## Render a review

```sh
outrider render --config pipeline.yaml \
  --repo forge.example.com/team/api --pr 42 \
  --workflow pipeline-review \
  --context-dir /srv/build/pr-context \
  --prompt-context-dir /work/pr-context
```

| Flag | Meaning |
|---|---|
| `--repo` | `host/owner/repo`, the host as configured in `forges` |
| `--pr` | the pull request number |
| `--workflow` | a workflow from the config, by name; without it, the built-in review task |
| `--context-dir` | where to write the context files |
| `--prompt-context-dir` | the path the prompt cites, where the agent will see the files; default `--context-dir` |
| `--trigger` | the prompt's trigger line |
| `--config` | the config file, as for every command |

The command writes `pr.json`, `pr-files.txt`, `pr.diff`, `review-comments.json`
and `failing-checks.json` into `--context-dir`. When a review profile asks for
context (`context: {issues: true, docs: [...]}`), it also writes
`review-context/`: the issues the PR description links on the same forge, and
the matching docs from the PR's base commit, with a `manifest.json`.
[Review profiles](configuration.md#review-profiles) apply only to other
people's PRs, so a PR the logged-in user wrote gets no `review-context/`.

It prints one JSON object:

| Key | Holds |
|---|---|
| `prompt` | the prompt for the agent |
| `own`, `author` | whether the logged-in user wrote the PR, and who did |
| `fork`, `head_mine` | whether the head is in another repository, and whether that is the user's own |
| `base_ref`, `head_ref` | the branches |
| `repo`, `number`, `workflow`, `review` | what was rendered, and the review profile that applied |
| `context_dir`, `prompt_context_dir`, `files` | where the context went |

The prompt is the read-only, offline one: the agent reads the prefetched files
and does not call the forge. A typical pipeline runs `outrider render` on the
host that holds the token, mounts `--context-dir` read-only into a container
without network access to the forge at `--prompt-context-dir`, checks out the
PR head as the agent's working directory (the prompt says it is there), runs
the agent with the prompt, and posts the result itself.

## What fj gives

fj prints text, not JSON, so `access: fj` gives less than `access: api`:

- `pr.json` has no commit ids for the head and base; reviews and comments are
  fj's text in `reviewsText` and `commentsText`. `outriderNotes` lists what is
  missing.
- `review-comments.json` holds fj's text of the reviews and their inline
  comments.
- Docs from the base commit are not available: fj does not report it.
- fj 0.7 fails on a commit without any status; the checks are then unknown and
  `pr.json` says so.

Use `access: api` when the agent needs inline review comments as data or docs
from the base commit.
