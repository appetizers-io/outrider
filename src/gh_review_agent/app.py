"""
GitHub PR notifications -> local interactive Codex (or Claude) prototype.

- own PR notifications trigger the agent
- 👀 on a non-owned PR opts it in
- max 1 active agent by default
- GitHub notifications stay untouched (read-only polling, local dedupe)
- agent review output stays local; no PR comments/replies/reactions/reviews
- the agent's `gh` is a read-only guard shim (git push still works)
"""

import argparse
import contextlib
import datetime as dt
import fnmatch
import hashlib
import json
import re
import shlex
import shutil
import subprocess
import sys
import time
from collections.abc import Callable, Sequence
from functools import partial
from pathlib import Path
from typing import Any, TypedDict, cast

import yaml

from gh_review_agent import config

# GitHub API payloads are only partially inspected; keep them loosely typed.
Json = Any
PR = dict[str, Any]


class ActivityItem(TypedDict):
    kind: str
    id: int | None
    state: str | None
    updated_at: str | None
    submitted_at: str | None
    user: str | None
    path: str | None
    at: str
    body: str


class Candidate(TypedDict):
    repo: str
    pr: int
    seen_at: float


class Watched(TypedDict):
    repo: str
    pr: int
    fingerprint: str | None


class State(TypedDict):
    seen: dict[str, str]  # notification id -> updated_at
    candidates: dict[str, Candidate]  # owner/repo#n -> non-owned PR
    watched: dict[str, Watched]  # owner/repo#n -> 👀 opted-in PR
    initialized: bool
    replies: dict[str, list[int]]  # owner/repo#n -> handled reply ids
    handled: dict[str, str]  # owner/repo#n -> when activity was last judged


class Args(argparse.Namespace):
    """Effective settings: the config file, overridden by command-line flags."""

    cfg: config.Config
    config: str | None
    config_source: Path | None  # None: built-in defaults
    repo: list[str]
    exclude_repo: list[str]
    agent: str
    remote: str | None
    launcher: str
    interval: int
    lookback_hours: int
    max_agents: int
    candidate_limit: int
    stale_lock_hours: float
    process_existing: bool
    once: bool
    dry_run: bool
    jev_cmd: str | None
    no_jev: bool
    reset_state: bool


Gate = Callable[[], list[ActivityItem]]

ROOT = Path.home() / ".cache/gh-review-agent"
STATE = Path.home() / ".local/state/gh-review-agent/state.json"
PR_RE = re.compile(r"/pulls/(\d+)$")
LAUNCHER = "terminal"  # set from --launcher in main()
LOGIN = ""  # my GitHub login, set in main()
OWNER = "Matthias"  # how prompts refer to me, set in main()
JEV: list[str] | None = None  # jev-use command for the pre-launch check
DEFAULT_JEV = config.Jev()
LOCAL: dict[str, tuple[Path, str]] = {}  # owner/repo -> (checkout, remote)

# Put first on the agent's PATH. Blocks every GitHub write through gh except
# local checkout; pushing goes through git, not gh.
GH_GUARD = r"""#!/usr/bin/env python3
import os, sys
REAL = os.environ["GH_REVIEW_AGENT_REAL_GH"]
a = sys.argv[1:]
READ = {
    "pr": {"view", "diff", "checks", "list", "status", "checkout"},
    "run": {"view", "list", "watch", "download"},
    "repo": {"view"}, "auth": {"status"}, "issue": {"view", "list"},
    "search": {"prs", "issues", "code", "commits"}, "browse": None,
}
def deny(why):
    sys.exit(f"gh-review-agent guard: blocked `gh {' '.join(a)}` ({why}). "
             "GitHub is read-only here; explain it locally instead.")
if not a:
    deny("no command")
if a[0] == "api":
    method, fields = None, False
    for i, x in enumerate(a):
        if x in ("-X", "--method") and i + 1 < len(a):
            method = a[i + 1].upper()
        elif x.startswith("--method="):
            method = x.split("=", 1)[1].upper()
        elif x.startswith("-X") and len(x) > 2:
            method = x[2:].upper()
        elif x == "--input" or x.startswith("--input="):
            deny("--input")
        elif x in ("-f", "-F", "--field", "--raw-field") or x.startswith(
                ("--field=", "--raw-field=")):
            fields = True
    if "graphql" in a:
        if any("mutation" in x.lower() or "=@" in x for x in a):
            deny("graphql mutation or query from file")
    elif (method or ("POST" if fields else "GET")) != "GET":
        deny("non-GET api call")
elif a[0] not in READ:
    deny("command not allowlisted")
elif READ[a[0]] is not None and (len(a) < 2 or a[1] not in READ[a[0]]):
    deny("subcommand not allowlisted")
os.execv(REAL, [REAL, *a])
"""


def log(s: str) -> None:
    print(time.strftime("%H:%M:%S"), s, flush=True)


def run(
    args: Sequence[str],
    cwd: Path | None = None,
    check: bool = True,
    timeout: float = 120,
    input: str | None = None,
) -> subprocess.CompletedProcess[str]:
    # Timeouts surface as CalledProcessError so every caller that already
    # tolerates a failed command also tolerates a hung one.
    try:
        return subprocess.run(
            args,
            cwd=str(cwd) if cwd else None,
            text=True,
            capture_output=True,
            check=check,
            timeout=timeout,
            input=input,
        )
    except subprocess.TimeoutExpired as e:
        raise subprocess.CalledProcessError(
            -1, args, stderr=f"timed out after {timeout}s"
        ) from e


def gh_json(args: Sequence[str]) -> Json:
    out = run(["gh", *args]).stdout
    try:
        return json.loads(out)
    except ValueError as e:
        raise subprocess.CalledProcessError(
            -1, ["gh", *args], output=out, stderr=f"invalid JSON: {e}"
        ) from e


def api(endpoint: str) -> list[Json]:
    pages = gh_json(["api", endpoint, "--paginate", "--slurp"])
    return [x for page in pages for x in page]


def load_state() -> State:
    try:
        s = json.loads(STATE.read_text())
    except FileNotFoundError:
        s = {}
    # migrate older prototype state
    if "seen" not in s:
        old = s.get("notifications", s if isinstance(s, dict) else {})
        s = {"seen": old, "candidates": {}, "watched": {}, "initialized": False}
    s.setdefault("seen", {})
    s.setdefault("candidates", {})
    s.setdefault("watched", {})
    s.setdefault("initialized", False)
    s.setdefault("replies", {})
    s.setdefault("handled", {})
    return cast(State, s)


def save_state(s: State) -> None:
    STATE.parent.mkdir(parents=True, exist_ok=True)
    tmp = STATE.with_suffix(".tmp")
    tmp.write_text(json.dumps(s, indent=2, sort_keys=True) + "\n")
    tmp.replace(STATE)


def repo_pattern(x: str) -> str:
    # accept owner/repo globs as well as GitHub URLs
    x = re.sub(r"^(https?://|git@|ssh://git@)github\.com[/:]", "", x.strip())
    return x.rstrip("/").removesuffix(".git")


def repo_ok(repo: str, include: Sequence[str], exclude: Sequence[str]) -> bool:
    if include and not any(fnmatch.fnmatchcase(repo, p) for p in include):
        return False
    return not any(fnmatch.fnmatchcase(repo, p) for p in exclude)


def pr_view(repo: str, n: int) -> PR:
    fields = (
        "number,title,url,state,author,headRefName,baseRefName,"
        "headRepository,headRepositoryOwner,maintainerCanModify,reviewDecision,"
        "statusCheckRollup"
    )
    return cast(PR, gh_json(["pr", "view", str(n), "--repo", repo, "--json", fields]))


REACTION_CONTENT = {
    "+1": "THUMBS_UP",
    "-1": "THUMBS_DOWN",
    "laugh": "LAUGH",
    "confused": "CONFUSED",
    "heart": "HEART",
    "hooray": "HOORAY",
    "rocket": "ROCKET",
    "eyes": "EYES",
}
REACTION_EMOJI = {
    "+1": "👍",
    "-1": "👎",
    "laugh": "😄",
    "confused": "😕",
    "heart": "❤️",
    "hooray": "🎉",
    "rocket": "🚀",
    "eyes": "👀",
}
WHERE_ALL = ("description", "comment", "review", "review_comment")
WHERE_LABEL = {
    "description": "PR description",
    "comment": "comment",
    "review": "review",
    "review_comment": "review comment",
}
EYES = "reactionGroups { content viewerHasReacted }"
PR_EYES = f"""pullRequest(number: %d) {{
  {EYES}
  comments(last: 100) {{ nodes {{ {EYES} }} }}
  reviews(last: 50) {{ nodes {{ {EYES} }} }}
  reviewThreads(last: 50) {{ nodes {{ comments(first: 30) {{ nodes {{ {EYES} }} }} }} }}
}}"""


def _has_my_reaction(node: Json, content: str) -> bool:
    groups = (node or {}).get("reactionGroups") or []
    return any(g["content"] == content and g["viewerHasReacted"] for g in groups)


def _eyes_query(
    prs: Sequence[tuple[str, int]],
    reaction: str = "eyes",
    where: Sequence[str] = WHERE_ALL,
) -> dict[str, str | None]:
    parts = []
    for i, (repo, n) in enumerate(prs):
        owner, name = repo.split("/", 1)
        parts.append(
            f"p{i}: repository(owner: {json.dumps(owner)}, "
            f"name: {json.dumps(name)}) {{ {PR_EYES % n} }}"
        )
    query = "query=query { " + " ".join(parts) + " }"
    data = gh_json(["api", "graphql", "-f", query])["data"]
    out: dict[str, str | None] = {}
    for i, (repo, n) in enumerate(prs):
        pr = (data.get(f"p{i}") or {}).get("pullRequest")
        if pr is None:
            continue
        content = REACTION_CONTENT[reaction]
        nodes = {
            "description": [pr],
            "comment": pr["comments"]["nodes"],
            "review": pr["reviews"]["nodes"],
            "review_comment": [
                c for t in pr["reviewThreads"]["nodes"] for c in t["comments"]["nodes"]
            ],
        }
        out[f"{repo}#{n}"] = next(
            (
                WHERE_LABEL[w]
                for w in WHERE_ALL
                if w in where and any(_has_my_reaction(x, content) for x in nodes[w])
            ),
            None,
        )
    return out


def my_eyes(
    prs: Sequence[tuple[str, int]],
    reaction: str = "eyes",
    where: Sequence[str] = WHERE_ALL,
    batch: int = 10,
) -> dict[str, str | None]:
    """Where I put the opt-in reaction on each PR, or None. PRs whose lookup
    failed are missing from the result."""
    out: dict[str, str | None] = {}
    for i in range(0, len(prs), batch):
        chunk = prs[i : i + batch]
        try:
            out.update(_eyes_query(chunk, reaction, where))
        except subprocess.CalledProcessError:
            # one broken PR must not hide the others
            for pr in chunk if len(chunk) > 1 else []:
                with contextlib.suppress(subprocess.CalledProcessError):
                    out.update(_eyes_query([pr], reaction, where))
    return out


def involved_prs(login: str) -> dict[str, tuple[str, int, str]]:
    """Open PRs I'm involved in or asked to review, notification or not."""
    out: dict[str, tuple[str, int, str]] = {}
    for q in (f"involves:{login}", f"review-requested:{login}"):
        res = gh_json(
            [
                "api",
                "-X",
                "GET",
                "search/issues",
                "-f",
                f"q=is:pr is:open archived:false {q}",
                "-f",
                "per_page=100",
            ]
        )
        for x in res.get("items", []):
            repo = x["repository_url"].split("/repos/", 1)[1]
            author = (x.get("user") or {}).get("login", "")
            out[f"{repo}#{x['number']}"] = (repo, x["number"], author)
    return out


def pending_replies(repo: str, n: int, login: str) -> list[list[Json]]:
    """Review threads I took part in whose last comment isn't mine."""
    threads: dict[int, list[Json]] = {}
    for c in api(f"repos/{repo}/pulls/{n}/comments?per_page=100"):
        threads.setdefault(c.get("in_reply_to_id") or c["id"], []).append(c)
    out = []
    for cs in threads.values():
        cs.sort(key=lambda c: c["id"])
        who = [(c.get("user") or {}).get("login", "").lower() for c in cs]
        if login.lower() in who and who[-1] != login.lower():
            out.append(cs)
    return out


def handle_replies(
    s: State,
    a: Args,
    login: str,
    repo: str,
    n: int,
    pr: PR | None,
    baseline: bool,
    fresh_after: str,
) -> bool:
    """Launch for unhandled replies to my review comments. False = retry.

    Only replies newer than fresh_after count, so an old, unanswered thread
    never triggers a session once the tool (re)starts watching the PR."""
    rules = a.cfg.triggers.review_replies
    if not rules.enabled:
        return True
    key = f"{repo}#{n}"
    done = set(s["replies"].get(key, []))
    try:
        new = [
            cs
            for cs in pending_replies(repo, n, login)
            if cs[-1]["id"] not in done
            and cs[-1].get("created_at", "") >= fresh_after
            and not ignored_author((cs[-1].get("user") or {}).get("login"), a.cfg)
        ]
        if new and not baseline:
            pr = pr or pr_view(repo, n)
            if pr.get("state") != "OPEN":
                return True
            urls = [cs[-1].get("html_url", "") for cs in new]
            thread = [activity_item("inline comment", c) for cs in new for c in cs]
            if not launch(
                repo,
                n,
                pr,
                f"reply to my review comment(s): {' '.join(urls)}",
                a,
                gate=(lambda: thread) if rules.gate == "jev" else None,
                scope=urls if rules.scope == "thread" else None,
            ):
                return False
    except subprocess.CalledProcessError:
        return False
    if not a.dry_run:
        s["replies"][key] = sorted(done | {cs[-1]["id"] for cs in new})
    return True


def activity_item(kind: str, x: Json) -> ActivityItem:
    return {
        "kind": kind,
        "id": x.get("id"),
        "state": x.get("state"),
        "updated_at": x.get("updated_at"),
        "submitted_at": x.get("submitted_at"),
        "user": (x.get("user") or {}).get("login"),
        "path": x.get("path"),
        "at": x.get("updated_at") or x.get("submitted_at") or "",
        "body": x.get("body") or "",
    }


def activity(repo: str, n: int) -> list[ActivityItem]:
    """Reviews, inline comments and conversation comments of a PR."""
    out = []
    for kind, endpoint in (
        ("review", f"repos/{repo}/pulls/{n}/reviews?per_page=100"),
        ("inline comment", f"repos/{repo}/pulls/{n}/comments?per_page=100"),
        ("comment", f"repos/{repo}/issues/{n}/comments?per_page=100"),
    ):
        out += [activity_item(kind, x) for x in api(endpoint)]
    return out


def fingerprint(items: Sequence[ActivityItem]) -> str:
    # field order must stay stable: stored fingerprints are compared across
    # versions, and a change would relaunch every watched PR
    data = [
        [x["id"], x["state"], x["updated_at"], x["submitted_at"], x["user"]]
        for x in items
    ]
    raw = json.dumps(data, sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(raw).hexdigest()


def newer_than(items: Sequence[ActivityItem], t: str | None) -> list[ActivityItem]:
    """Activity newer than t (all of it, capped, when never judged)."""
    ordered = sorted(items, key=lambda x: x["at"])
    return [x for x in ordered if x["at"] > t] if t else ordered[-10:]


def new_activity(repo: str, n: int, t: str | None) -> list[ActivityItem]:
    return newer_than(activity(repo, n), t)


def login_glob(pattern: str) -> re.Pattern[str]:
    """* and ? wildcards only: logins like "netlify[bot]" contain brackets,
    which fnmatch would read as a character class."""
    rx = re.escape(pattern).replace(r"\*", ".*").replace(r"\?", ".")
    return re.compile(rx, re.IGNORECASE)


def ignored_author(user: str | None, cfg: config.Config) -> bool:
    return any(login_glob(p).fullmatch(user or "") for p in cfg.ignore_authors)


def only_noise(
    items: Sequence[ActivityItem], login: str, cfg: config.Config, mine_too: bool
) -> bool:
    """Nothing in items needs me: all by ignored authors (or by me)."""
    return all(
        ignored_author(x["user"], cfg)
        or (mine_too and (x["user"] or "").lower() == login.lower())
        for x in items
    )


def iso_hours_ago(hours: float) -> str:
    t = dt.datetime.now(dt.UTC) - dt.timedelta(hours=hours)
    return t.strftime("%Y-%m-%dT%H:%M:%SZ")


def now_iso() -> str:
    return dt.datetime.now(dt.UTC).strftime("%Y-%m-%dT%H:%M:%SZ")


def failing_checks(pr: PR) -> list[str]:
    bad = {
        "FAILURE",
        "TIMED_OUT",
        "CANCELLED",
        "ACTION_REQUIRED",
        "ERROR",
        "STARTUP_FAILURE",
    }
    return [
        c.get("name") or c.get("context") or "?"
        for c in pr.get("statusCheckRollup") or []
        if (c.get("conclusion") or c.get("state")) in bad
    ]


def jev_request(
    repo: str,
    n: int,
    pr: PR,
    trigger: str,
    new: Sequence[ActivityItem],
    settings: config.Jev = DEFAULT_JEV,
) -> dict[str, Any]:
    author = (pr.get("author") or {}).get("login", "")
    own = author.lower() == LOGIN.lower()
    whose = f"{OWNER}'s own PR" if own else f"someone else's PR; {OWNER} is a reviewer"
    lines = [
        f"GitHub pull request {repo}#{n}: {pr.get('title')}",
        f"PR author: {author} ({whose})",
        f"{OWNER}'s GitHub login: {LOGIN}",
        f"Why this check runs: {trigger}",
        "Failing CI checks: " + (", ".join(failing_checks(pr)) or "none"),
        "",
        "New activity since the last check (oldest first):",
    ]
    budget, entries = 40000, []
    for x in reversed(new):  # keep the newest when trimming
        head = f"- {x['kind']} by {x['user']} at {x['at']}"
        if x["state"]:
            head += f" [{x['state']}]"
        if x["path"]:
            head += f" on {x['path']}"
        body = x["body"].strip()[:1500].replace("\n", "\n  ")
        entry = head + (":\n  " + body if body else "")
        budget -= len(entry)
        if budget < 0:
            break
        entries.append(entry)
    lines += reversed(entries) if entries else ["- (none)"]
    req: dict[str, Any] = {
        "state": "\n".join(lines),
        "questions": [
            {
                "id": "act",
                "type": "noul",
                "question": f"Should a coding agent working for {OWNER} act on "
                f"this PR now: a concrete change request, a question that needs "
                f"{OWNER}'s answer, a reply in their review thread that needs a "
                "response, or a failing CI check on their own PR?",
                "criteria": {
                    "true": "at least one item needs a code change or a "
                    f"response from {OWNER}",
                    "false": "only bot summaries, approvals, LGTMs, thanks, "
                    f"acknowledgements, {OWNER}'s own activity, or nothing",
                },
            }
        ],
    }
    if settings.confidence_threshold is not None:
        req["confidence_threshold"] = settings.confidence_threshold
    return req


def jev_worth_it(
    repo: str,
    n: int,
    pr: PR,
    trigger: str,
    new: Sequence[ActivityItem],
    settings: config.Jev = DEFAULT_JEV,
) -> tuple[bool, str]:
    """Ask Jev whether the new activity needs the agent at all.

    Returns (launch, note). Anything but a confident "no" launches, so an
    unsure or unreachable Jev never swallows real feedback."""
    req = jev_request(repo, n, pr, trigger, new, settings)
    try:
        # exit 3 = escalated; the verdict is still on stdout
        out = run(
            [*(JEV or []), "judge"],
            input=json.dumps(req),
            timeout=settings.timeout_seconds,
            check=False,
        ).stdout
        v = json.loads(out)["verdicts"][0]
    except (
        subprocess.CalledProcessError,
        OSError,
        ValueError,
        KeyError,
        IndexError,
    ) as e:
        return True, f"jev unavailable, launching anyway: {e!r}"
    note = f"jev p={v.get('answer')} conf={v.get('confidence')}"
    if v.get("escalate"):
        return True, f"{note} unsure ({v.get('reason')}), launching anyway"
    answer = v.get("answer", 1)
    if not isinstance(answer, int | float):
        return True, f"{note} unexpected answer, launching anyway"
    return answer >= settings.skip_below, note


def worktree(repo: str, n: int) -> Path:
    slug = repo.replace("/", "__")
    clone, remote = LOCAL.get(repo) or (ROOT / "repos" / slug, "origin")
    wt = ROOT / "worktrees" / slug / f"pr-{n}"
    branch = f"review/pr-{n}"

    if not clone.exists():
        clone.parent.mkdir(parents=True, exist_ok=True)
        log(f"cloning {repo}")
        run(
            ["gh", "repo", "clone", repo, str(clone), "--", "--filter=blob:none"],
            timeout=1800,
        )

    checkout = ["gh", "pr", "checkout", str(n), "--repo", repo, "--branch", branch]
    if not wt.exists():
        wt.parent.mkdir(parents=True, exist_ok=True)
        log(f"creating worktree for {repo}#{n} from {clone}")
        run(["git", "fetch", "--quiet", remote], cwd=clone, timeout=900)
        run(["git", "worktree", "prune"], cwd=clone)
        run(["git", "worktree", "add", "--detach", str(wt)], cwd=clone)
        # gh sets up the head branch + push remote (also for forks)
        run(checkout, cwd=wt)
    elif not run(["git", "status", "--porcelain"], cwd=wt).stdout.strip():
        run(checkout, cwd=wt, check=False)
    else:
        log(f"{repo}#{n}: preserving existing local changes")
    return wt


def prompt(
    repo: str,
    n: int,
    pr: PR,
    trigger: str,
    scope: Sequence[str] | None = None,
    extra: str = "",
) -> str:
    author = (pr.get("author") or {}).get("login", "")
    head = "{}/{}".format(
        (pr.get("headRepositoryOwner") or {}).get("login", "?"),
        (pr.get("headRepository") or {}).get("name", "?"),
    )
    base = pr.get("baseRefName")
    remote = LOCAL[repo][1] if repo in LOCAL else "origin"  # upstream for forks
    if author.lower() == LOGIN.lower():
        # own work: keep it current with the base branch
        branch_rule = f"""This is {OWNER}'s OWN PR (own work):
- before changing anything: `git fetch {remote} && git rebase {remote}/{base}`
- after committing: fetch and rebase onto {remote}/{base} again
- if a rebase conflicts, stop and explain it here; do not force-resolve
- push the rebased branch with `git push --force-with-lease` (only to this
  PR's branch)"""
        force = "do NOT force-push, except `--force-with-lease` after the rebase"
    else:
        # someone else's PR: never rewrite their history
        branch_rule = f"""This PR belongs to {author} (review work, not {OWNER}'s):
- do NOT rebase, rewrite history or force-push
- add commits on top of the current PR head; push fast-forward only with
  plain `git push`
- if the branch is behind or conflicts with {base}, report it here instead"""
        force = "do NOT force-push"
    if scope:
        # a reply in my review thread: that thread only, not the whole PR
        threads = "\n".join(f"- {u}" for u in scope)
        work = f"""SCOPE: this session is ONLY about the review thread(s) below,
where someone replied to {OWNER}'s review comment:
{threads}
Read those threads (`gh api repos/{repo}/pulls/{n}/comments`, match the
comment ids in the URLs) and handle only them. Do NOT review, summarize or
act on anything else in this PR."""
    else:
        work = """Use gh/git to inspect recent reviews, inline review comments,
PR discussion, commits and CI/checks."""
    return f"""Babysit this GitHub PR locally.

Trigger: {trigger}
Repository: {repo}
PR: #{n}
URL: {pr.get("url")}
Title: {pr.get("title")}
Author: {author}
Head: {head}:{pr.get("headRefName")} (local branch review/pr-{n})
Maintainer can modify: {pr.get("maintainerCanModify")}
Base branch: {base}

{branch_rule}

{work}

If there is new, actionable, unambiguous feedback:
- make the smallest appropriate change
- run focused tests/lint
- commit with a concise human-style message
- VERIFY push access to the existing PR head repository/branch
- only then push to that EXISTING branch

If you cannot push, keep the patch local and explain it to {OWNER}.
If nothing is actionable, change nothing.
If a human/architectural decision is needed, explain it to {OWNER} here.

STRICT TEST PHASE:
- keep ALL review summaries/questions in this local session
- do NOT post comments or review replies
- do NOT resolve threads
- do NOT add/remove reactions
- do NOT submit/approve/reject reviews
- do NOT change GitHub notification state
- do NOT merge/close the PR
- {force}
The `gh` on PATH is read-only and will refuse GitHub writes; do not try to
work around it (no curl/API tokens). Push only with `git push`.
""" + (f"\n{extra.strip()}\n" if extra.strip() else "")


def locks(stale_hours: float) -> list[Path]:
    d = ROOT / "locks"
    d.mkdir(parents=True, exist_ok=True)
    cutoff = time.time() - stale_hours * 3600
    out = []
    for p in d.glob("*.lock"):
        try:
            tmux = json.loads(p.read_text()).get("tmux")
        except OSError, ValueError:
            tmux = None
        gone = (
            tmux
            and run(["tmux", "has-session", "-t", f"={tmux}"], check=False).returncode
            != 0
        )
        if gone or p.stat().st_mtime < cutoff:
            log(f"removing stale lock {p.name}")
            p.unlink(missing_ok=True)
        else:
            out.append(p)
    return out


def launch(
    repo: str,
    n: int,
    pr: PR,
    trigger: str,
    a: Args,
    gate: Gate | None = None,
    scope: Sequence[str] | None = None,
) -> bool:
    """True once the event is handled: agent started, or Jev said skip.

    gate: callable returning the new activity to judge first; None starts
    the agent unconditionally (explicit 👀 opt-in).
    scope: review thread URLs the session is limited to; None = whole PR."""
    try:
        return _launch(repo, n, pr, trigger, a, gate, scope)
    except (subprocess.CalledProcessError, OSError, RuntimeError) as e:
        err = getattr(e, "stderr", "") or e
        log(f"{repo}#{n}: launch failed; keeping event pending: {err}")
        return False


def _launch(
    repo: str,
    n: int,
    pr: PR,
    trigger: str,
    a: Args,
    gate: Gate | None,
    scope: Sequence[str] | None,
) -> bool:
    lock = ROOT / "locks" / f"{repo.replace('/', '__')}__{n}.lock"
    if lock.exists():
        log(f"{repo}#{n}: already running; keeping event pending")
        return False
    if not a.dry_run and len(locks(a.stale_lock_hours)) >= a.max_agents:
        log(f"{repo}#{n}: agent limit reached ({a.max_agents}); keeping event pending")
        return False

    if gate and JEV:
        go, note = jev_worth_it(repo, n, pr, trigger, gate(), a.cfg.jev)
        log(f"{repo}#{n}: {note}")
        if not go:
            log(f"{repo}#{n}: nothing actionable [{trigger}]; not launching")
            return True

    log(f"matched {repo}#{n}: {pr['title']} [{trigger}]")
    if a.dry_run:
        return True

    wt = worktree(repo, n)
    session = ROOT / "sessions" / repo.replace("/", "__") / f"pr-{n}"
    session.mkdir(parents=True, exist_ok=True)
    pf = session / "prompt.txt"
    runner = session / "run-agent.command"
    pf.write_text(prompt(repo, n, pr, trigger, scope, a.cfg.prompts.extra))

    agent_path = shutil.which(a.agent)
    if not agent_path:
        raise RuntimeError(f"{a.agent} is not installed")
    gh_path = shutil.which("gh")
    if not gh_path:
        raise RuntimeError("gh is not installed")
    guard_bin = ROOT / "bin"
    guard_bin.mkdir(parents=True, exist_ok=True)
    (guard_bin / "gh").write_text(GH_GUARD)
    (guard_bin / "gh").chmod(0o755)

    agent_args = ""
    if a.agent == "claude":
        # Remote Control lists the session on claude.ai and in Claude Desktop
        name = shlex.quote(f"PR {repo}#{n}")
        agent_args = f"--name {name} --remote-control {name}"

    tmux = None
    if LAUNCHER == "tmux":
        tmux = re.sub(r"[^A-Za-z0-9_-]", "-", f"pr-{repo}-{n}")
    lock.write_text(
        json.dumps({"repo": repo, "pr": n, "started": time.time(), "tmux": tmux})
    )
    runner.write_text(f"""#!/usr/bin/env zsh
set -u
LOCK={shlex.quote(str(lock))}
trap 'rm -f "$LOCK"' EXIT INT TERM HUP
cd {shlex.quote(str(wt))} || exit 1
export GH_REVIEW_AGENT_REAL_GH={shlex.quote(gh_path)}
export PATH={shlex.quote(str(guard_bin))}:"$PATH"
clear
echo "GitHub PR review agent: {repo}#{n}"
echo "agent: {a.agent} (gh is read-only)"
echo
{shlex.quote(agent_path)} {agent_args} "$(cat {shlex.quote(str(pf))})"
status=$?
rm -f "$LOCK"
trap - EXIT INT TERM HUP
echo
echo "agent exited: $status"
echo "press any key to close"
read -k 1
exit $status
""")
    runner.chmod(0o700)
    try:
        if tmux:
            # a finished session may still wait for a keypress
            run(["tmux", "kill-session", "-t", f"={tmux}"], check=False)
            run(
                [
                    "tmux",
                    "new-session",
                    "-d",
                    "-s",
                    tmux,
                    "-c",
                    str(wt),
                    "zsh",
                    str(runner),
                ]
            )
            log(f"{repo}#{n}: attach with: tmux attach -t {tmux}")
        else:
            subprocess.run(["open", "-a", "Terminal", str(runner)], check=True)
    except subprocess.CalledProcessError, OSError:
        lock.unlink(missing_ok=True)
        raise
    return True


def poll(s: State, a: Args, login: str) -> None:
    """One pass over notifications, candidates and watched PRs."""
    cfg, trig = a.cfg, a.cfg.triggers
    window_start = iso_hours_ago(a.lookback_hours)
    fresh = trig.review_replies.fresh_within_hours
    replies_after = iso_hours_ago(fresh) if fresh else window_start
    ns = api(f"notifications?all=true&since={window_start}&per_page=50")
    log(f"fetched {len(ns)} notifications from last {a.lookback_hours}h")

    first_live = not s["initialized"] and not a.dry_run
    cutoff = time.time() - a.lookback_hours * 3600
    for key, cand in list(s["candidates"].items()):
        if cand.get("seen_at", 0) < cutoff:
            s["candidates"].pop(key)
    stats = {"mine": 0, "candidate": 0, "ignored": 0}
    baseline = first_live and not a.process_existing

    # 1) Notification feed: own PRs + discover non-owned candidates.
    for x in ns:
        subject = x.get("subject") or {}
        repo = (x.get("repository") or {}).get("full_name", "")
        if subject.get("type") != "PullRequest" or not repo_ok(
            repo, a.repo, a.exclude_repo
        ):
            stats["ignored"] += 1
            continue

        m = PR_RE.search(subject.get("url") or "")
        if not m:
            continue
        n = int(m.group(1))
        key = f"{repo}#{n}"
        nid, updated = str(x.get("id", "")), str(x.get("updated_at", ""))

        if key in s["watched"]:
            s["seen"][nid] = updated
            continue
        if key in s["candidates"]:
            # known non-owned PR: refresh only, 👀 is checked below
            s["candidates"][key]["seen_at"] = time.time()
            stats["candidate"] += 1
            if s["seen"].get(nid) != updated and not handle_replies(
                s, a, login, repo, n, None, baseline, replies_after
            ):
                continue  # keep the notification pending
            s["seen"][nid] = updated
            continue
        if s["seen"].get(nid) == updated and not a.dry_run:
            continue  # already handled; skip the pr_view call

        try:
            pr = pr_view(repo, n)
        except subprocess.CalledProcessError:
            continue
        if pr.get("state") != "OPEN":
            s["candidates"].pop(key, None)
            s["seen"][nid] = updated
            continue

        mine = (pr.get("author") or {}).get("login", "").lower() == login.lower()

        if not mine:
            # Important: keep this PR around even if we already saw the
            # notification, so a 👀 added later can still opt it in.
            s["candidates"][key] = {"repo": repo, "pr": n, "seen_at": time.time()}
            stats["candidate"] += 1
            if handle_replies(s, a, login, repo, n, pr, baseline, replies_after):
                s["seen"][nid] = updated
            continue

        if s["seen"].get(nid) == updated:
            continue

        stats["mine"] += 1
        if (first_live and not a.process_existing) or not trig.own_prs.enabled:
            s["seen"][nid] = updated
            continue

        t = s["handled"].get(key)
        if cfg.ignore_authors:
            try:
                new = new_activity(repo, n, t)
            except subprocess.CalledProcessError:
                continue
            if new and only_noise(new, login, cfg, mine_too=False):
                log(f"{repo}#{n}: only ignored authors; not launching")
                if not a.dry_run:
                    s["seen"][nid] = updated
                    s["handled"][key] = now_iso()
                continue
        if (
            launch(
                repo,
                n,
                pr,
                f"my PR notification ({x.get('reason')})",
                a,
                gate=partial(new_activity, repo, n, t)
                if trig.own_prs.gate == "jev"
                else None,
            )
            and not a.dry_run
        ):
            s["seen"][nid] = updated
            s["handled"][key] = now_iso()

    if not trig.opt_in.enabled:
        finish(s, a, stats, first_live)
        return
    opt_in = trig.opt_in
    emoji = REACTION_EMOJI[opt_in.reaction]

    # 2) Non-owned PRs I'm involved in, even without a recent notification,
    #    so the opt-in reaction on an older PR still counts.
    try:
        involved = involved_prs(login)
    except subprocess.CalledProcessError as e:
        log(
            "PR search failed; using notification candidates only: "
            f"{(e.stderr or '').strip()}"
        )
        involved = {}
    for key, (repo, n, author) in involved.items():
        if (
            author.lower() == login.lower()
            or key in s["watched"]
            or not repo_ok(repo, a.repo, a.exclude_repo)
        ):
            continue
        s["candidates"][key] = {"repo": repo, "pr": n, "seen_at": time.time()}

    # 3) Candidates: detect the opt-in reaction added AFTER we first saw them.
    candidates = sorted(
        s["candidates"].items(), key=lambda kv: kv[1].get("seen_at", 0), reverse=True
    )[: a.candidate_limit]
    eyes = my_eyes(
        [(c["repo"], int(c["pr"])) for _, c in candidates],
        opt_in.reaction,
        opt_in.where,
    )

    for key, cand in candidates:
        repo, n = cand["repo"], int(cand["pr"])
        where = eyes.get(key)
        if not where:
            continue  # no reaction, or lookup failed

        try:
            pr = pr_view(repo, n)
        except subprocess.CalledProcessError:
            continue
        if pr.get("state") != "OPEN":
            s["candidates"].pop(key, None)
            continue

        if a.dry_run:
            log(f"{emoji} detected {repo}#{n} (on {where})")

        if launch(repo, n, pr, f"{emoji} opt-in (on {where})", a) and not a.dry_run:
            fp: str | None
            try:
                fp = fingerprint(activity(repo, n))
            except subprocess.CalledProcessError:
                fp = None  # next watch pass relaunches on change
            s["handled"][key] = now_iso()
            s["watched"][key] = {"repo": repo, "pr": n, "fingerprint": fp}
            s["candidates"].pop(key, None)

    # 4) Already opted-in PRs: trigger only when review/discussion changes.
    watched = list(s["watched"].items())
    eyes = my_eyes(
        [(w["repo"], int(w["pr"])) for _, w in watched], opt_in.reaction, opt_in.where
    )
    for key, w in watched:
        repo, n = w["repo"], int(w["pr"])
        if key not in eyes:
            continue  # lookup failed; keep watching
        if not eyes[key]:
            log(f"{repo}#{n}: {emoji} removed; stopping watch")
            if not a.dry_run:
                s["watched"].pop(key, None)
            continue

        try:
            pr = pr_view(repo, n)
            if pr.get("state") != "OPEN":
                if not a.dry_run:
                    s["watched"].pop(key, None)
                continue
            items = activity(repo, n)
        except subprocess.CalledProcessError:
            continue
        fp = fingerprint(items)
        if fp == w.get("fingerprint"):
            continue

        t = s["handled"].get(key)
        if only_noise(
            newer_than(items, t),
            login,
            cfg,
            mine_too=opt_in.on_change.ignore_own_activity,
        ):
            # my own or ignored authors' activity, or edits/deletions only
            log(f"{repo}#{n}: nothing new from others; not launching")
            if not a.dry_run:
                w["fingerprint"] = fp
                s["handled"][key] = now_iso()
            continue

        if (
            launch(
                repo,
                n,
                pr,
                "review/discussion changed",
                a,
                gate=partial(newer_than, items, t)
                if opt_in.on_change.gate == "jev"
                else None,
            )
            and not a.dry_run
        ):
            w["fingerprint"] = fp
            s["handled"][key] = now_iso()

    finish(s, a, stats, first_live)


def finish(s: State, a: Args, stats: dict[str, int], first_live: bool) -> None:
    log(
        f"{'dry-run ' if a.dry_run else ''}summary: "
        f"own_new={stats['mine']} non_owned={stats['candidate']} "
        f"watched={len(s['watched'])} ignored={stats['ignored']}"
        + (
            " (first run: existing own notifications recorded only)"
            if first_live and not a.process_existing
            else ""
        )
    )
    if not a.dry_run:
        s["initialized"] = True
        save_state(s)


def parse_args(argv: Sequence[str] | None = None) -> Args:
    """Command-line flags over the config file; unset flags take its values."""
    p = argparse.ArgumentParser(
        prog="gh-review-agent",
        epilog="Settings come from the config file (see `gh-review-agent config "
        "--help`); these flags override it.",
    )
    p.add_argument(
        "--config",
        help=f"YAML config (default: ${config.ENV_VAR}, else "
        f"{config.default_path()} when present)",
    )
    p.add_argument("--repo", action="append", help="owner/repo glob or URL")
    p.add_argument("--exclude-repo", action="append", help="owner/repo glob or URL")
    p.add_argument("--agent", choices=["codex", "claude"])
    p.add_argument(
        "--remote",
        help="remote of the local checkout to watch "
        "(default: origin; e.g. upstream for a fork)",
    )
    p.add_argument(
        "--launcher",
        choices=["auto", "terminal", "tmux"],
        help="auto: Terminal on a macOS desktop session, else tmux",
    )
    p.add_argument("--interval", type=int, help="seconds between polls")
    p.add_argument("--lookback-hours", type=int)
    p.add_argument("--max-agents", type=int)
    p.add_argument("--candidate-limit", type=int)
    p.add_argument("--stale-lock-hours", type=float)
    p.add_argument("--process-existing", action="store_true")
    p.add_argument("--once", action="store_true")
    p.add_argument("--dry-run", action="store_true")
    p.add_argument("--jev-cmd", help="jev-use command for the pre-launch check")
    p.add_argument(
        "--no-jev",
        action="store_true",
        help="launch on every trigger without asking Jev first",
    )
    p.add_argument("--reset-state", action="store_true")
    a = p.parse_args(argv, namespace=Args())

    a.config_source = config.find(a.config)
    try:
        cfg = config.load(a.config_source)
    except config.ConfigError as e:
        raise SystemExit(str(e)) from e
    jev = cfg.jev.model_copy(
        update={
            k: v
            for k, v in (
                ("command", a.jev_cmd),
                ("enabled", False if a.no_jev else None),
            )
            if v is not None
        }
    )
    cfg = cfg.model_copy(
        update={
            k: v
            for k, v in (
                ("agent", a.agent),
                ("launcher", a.launcher),
                ("interval_seconds", a.interval),
                ("lookback_hours", a.lookback_hours),
                ("max_agents", a.max_agents),
                ("candidate_limit", a.candidate_limit),
                ("stale_lock_hours", a.stale_lock_hours),
                ("jev", jev),
            )
            if v is not None
        }
    )
    a.cfg = cfg
    a.agent, a.launcher = cfg.agent, cfg.launcher
    a.interval, a.lookback_hours = cfg.interval_seconds, cfg.lookback_hours
    a.max_agents, a.candidate_limit = cfg.max_agents, cfg.candidate_limit
    a.stale_lock_hours = cfg.stale_lock_hours
    a.repo = [repo_pattern(x) for x in a.repo or cfg.repos.include]
    a.exclude_repo = [repo_pattern(x) for x in a.exclude_repo or cfg.repos.exclude]
    return a


def resolve_jev(settings: config.Jev) -> tuple[list[str] | None, str]:
    """The jev-use command to run, or None with the reason it is off."""
    if settings.enabled is False:
        return None, "disabled"
    if settings.command:
        cmd = shlex.split(settings.command)
    elif shutil.which("jev-use"):
        cmd = ["jev-use"]
    else:
        cmd = ["npx", "-y", "jev-use@0.8.0"]
    if not shutil.which(cmd[0]):
        return None, f"{cmd[0]} not found"
    if settings.enabled == "auto" and not config.jev_backend_configured():
        return None, "no backend key (" + ", ".join(config.JEV_BACKEND_ENV) + ")"
    return cmd, " ".join(cmd)


def github_user(once: bool) -> tuple[str, str]:
    """(login, display name) of the gh user, retried until GitHub answers."""
    while True:
        err: object
        try:
            user = gh_json(["api", "user"])
            if user.get("login"):
                return user["login"], user.get("name") or ""
            err = "empty login"
        except subprocess.CalledProcessError as e:
            err = (e.stderr or "").strip() or e
        if once:
            raise SystemExit(f"cannot determine GitHub user: {err}")
        log(f"cannot determine GitHub user, retrying in 30s: {err}")
        time.sleep(30)


def config_cli(argv: Sequence[str]) -> int:
    p = argparse.ArgumentParser(
        prog="gh-review-agent config", description="Inspect the configuration."
    )
    sub = p.add_subparsers(dest="cmd", required=True)
    sub.add_parser("schema", help="print the JSON Schema of the config file")
    for name, what in (
        ("check", "validate a config file"),
        ("show", "print the effective config, defaults filled in"),
    ):
        sp = sub.add_parser(name, help=what)
        sp.add_argument("path", nargs="?", help="default: the config in use")
    a = p.parse_args(argv)
    if a.cmd == "schema":
        print(config.schema_text(), end="")
        return 0
    path = config.find(a.path)
    try:
        cfg = config.load(path)
    except config.ConfigError as e:
        print(e, file=sys.stderr)
        return 1
    if a.cmd == "check":
        print(f"ok: {path or 'no config file, built-in defaults'}")
    else:
        print(yaml.safe_dump(cfg.model_dump(mode="json"), sort_keys=False), end="")
    return 0


def main(argv: Sequence[str] | None = None) -> None:
    global LAUNCHER, LOGIN, OWNER, JEV
    argv = list(sys.argv[1:] if argv is None else argv)
    if argv[:1] == ["config"]:
        raise SystemExit(config_cli(argv[1:]))
    a = parse_args(argv)

    # Inside a local checkout: watch its GitHub repo and branch worktrees
    # off it instead of a cached clone.
    top = run(["git", "rev-parse", "--show-toplevel"], check=False)
    if top.returncode == 0:
        remote = a.remote or "origin"
        checkout = Path(top.stdout.strip())
        url = run(
            ["git", "remote", "get-url", remote], cwd=checkout, check=False
        ).stdout.strip()
        repo = repo_pattern(url)
        if re.fullmatch(r"[\w.-]+/[\w.-]+", repo):
            LOCAL[repo] = (checkout, remote)
            if not a.repo:
                a.repo = [repo]
        elif a.remote:
            raise SystemExit(f"remote {a.remote!r} is not a GitHub repo here")
    elif a.remote:
        raise SystemExit("--remote needs to run inside a git checkout")

    JEV, jev_note = resolve_jev(a.cfg.jev)
    LAUNCHER = a.launcher
    if LAUNCHER == "auto":
        gui = (
            sys.platform == "darwin"
            and run(["launchctl", "managername"], check=False).stdout.strip() == "Aqua"
        )
        LAUNCHER = "terminal" if gui else "tmux"

    launcher_tool = "open" if LAUNCHER == "terminal" else "tmux"
    for tool in ("gh", "git", "zsh", launcher_tool, a.agent):
        if not shutil.which(tool):
            raise SystemExit(f"missing required command: {tool}")
    if a.max_agents < 1:
        raise SystemExit("--max-agents must be >= 1")
    if a.reset_state:
        STATE.unlink(missing_ok=True)

    ROOT.mkdir(parents=True, exist_ok=True)
    s = load_state()
    login, name = github_user(a.once)
    LOGIN = login
    OWNER = a.cfg.owner_name or (name.split() or [login])[0]

    log(f"config: {a.config_source or 'no config file, built-in defaults'}")
    log(f"GitHub user: {login} (prompts call you {OWNER})")
    log(f"agent: {a.agent} (interactive)")
    log(f"launcher: {LAUNCHER}")
    log(f"max active agents: {a.max_agents}")
    log(f"jev check before launch: {'on, ' if JEV else 'off, '}{jev_note}")
    log("GitHub notifications: READ ONLY")
    log("review output: LOCAL SESSION ONLY")
    if a.repo:
        log(f"repos: {', '.join(a.repo)}")
    for repo, (path, remote) in LOCAL.items():
        log(f"local checkout for {repo}: {path} (remote {remote})")

    failures = 0
    while True:
        try:
            poll(s, a, login)
            failures = 0
        except Exception as e:
            failures += 1
            err = (getattr(e, "stderr", "") or "").strip() or repr(e)
            log(f"poll failed ({failures}x in a row), will retry: {err}")
            if not a.dry_run:
                try:
                    save_state(s)  # keep launches recorded so far
                except OSError as e2:
                    log(f"could not save state: {e2}")

        if a.once:
            return
        # back off on repeated failures (network down etc.), max 15 min
        time.sleep(min(max(10, a.interval) * 2 ** min(failures, 4), 900))
