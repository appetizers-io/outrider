#!/usr/bin/env python3
"""
GitHub PR notifications -> local interactive Codex (or Claude) prototype.

- own PR notifications trigger the agent
- 👀 on a non-owned PR opts it in
- max 1 active agent by default
- GitHub notifications stay untouched (read-only polling, local dedupe)
- agent review output stays local; no PR comments/replies/reactions/reviews
- the agent's `gh` is a read-only guard shim (git push still works)
"""

import argparse, datetime as dt, fnmatch, hashlib, json, re, shlex, shutil
import subprocess, sys, time
from pathlib import Path

ROOT = Path.home() / ".cache/gh-review-agent"
STATE = Path.home() / ".local/state/gh-review-agent/state.json"
PR_RE = re.compile(r"/pulls/(\d+)$")
LAUNCHER = "terminal"  # set from --launcher in main()
LOGIN = ""  # my GitHub login, set in main()
JEV = None  # jev-use command for the pre-launch check; None = off
LOCAL = {}  # owner/repo -> (local checkout, remote), when run inside a repo

# Put first on the agent's PATH. Blocks every GitHub write through gh except
# local checkout; pushing goes through git, not gh.
GH_GUARD = r'''#!/usr/bin/env python3
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
'''


def log(s):
    print(time.strftime("%H:%M:%S"), s, flush=True)


def run(args, cwd=None, check=True, timeout=120, input=None):
    # Timeouts surface as CalledProcessError so every caller that already
    # tolerates a failed command also tolerates a hung one.
    try:
        return subprocess.run(
            args, cwd=str(cwd) if cwd else None, text=True,
            capture_output=True, check=check, timeout=timeout, input=input
        )
    except subprocess.TimeoutExpired as e:
        raise subprocess.CalledProcessError(
            -1, args, stderr=f"timed out after {timeout}s") from e


def gh_json(args):
    out = run(["gh", *args]).stdout
    try:
        return json.loads(out)
    except ValueError as e:
        raise subprocess.CalledProcessError(
            -1, ["gh", *args], output=out, stderr=f"invalid JSON: {e}") from e


def api(endpoint):
    pages = gh_json(["api", endpoint, "--paginate", "--slurp"])
    return [x for page in pages for x in page]


def load_state():
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
    s.setdefault("handled", {})  # PR -> when its activity was last judged
    return s


def save_state(s):
    STATE.parent.mkdir(parents=True, exist_ok=True)
    tmp = STATE.with_suffix(".tmp")
    tmp.write_text(json.dumps(s, indent=2, sort_keys=True) + "\n")
    tmp.replace(STATE)


def repo_pattern(x):
    # accept owner/repo globs as well as GitHub URLs
    x = re.sub(r"^(https?://|git@|ssh://git@)github\.com[/:]", "", x.strip())
    return x.rstrip("/").removesuffix(".git")


def repo_ok(repo, include, exclude):
    if include and not any(fnmatch.fnmatchcase(repo, p) for p in include):
        return False
    return not any(fnmatch.fnmatchcase(repo, p) for p in exclude)


def pr_view(repo, n):
    fields = (
        "number,title,url,state,author,headRefName,baseRefName,"
        "headRepository,headRepositoryOwner,maintainerCanModify,reviewDecision,"
        "statusCheckRollup"
    )
    return gh_json(["pr", "view", str(n), "--repo", repo, "--json", fields])


EYES = "reactionGroups { content viewerHasReacted }"
PR_EYES = f"""pullRequest(number: %d) {{
  {EYES}
  comments(last: 100) {{ nodes {{ {EYES} }} }}
  reviews(last: 50) {{ nodes {{ {EYES} }} }}
  reviewThreads(last: 50) {{ nodes {{ comments(first: 30) {{ nodes {{ {EYES} }} }} }} }}
}}"""


def _eyes_query(prs):
    parts = []
    for i, (repo, n) in enumerate(prs):
        owner, name = repo.split("/", 1)
        parts.append(f"p{i}: repository(owner: {json.dumps(owner)}, "
                     f"name: {json.dumps(name)}) {{ {PR_EYES % n} }}")
    data = gh_json(["api", "graphql", "-f",
                    "query=query { " + " ".join(parts) + " }"])["data"]
    out = {}
    for i, (repo, n) in enumerate(prs):
        pr = (data.get(f"p{i}") or {}).get("pullRequest")
        if pr is None:
            continue
        mine = lambda x: any(g["content"] == "EYES" and g["viewerHasReacted"]
                             for g in (x or {}).get("reactionGroups") or [])
        where = None
        if mine(pr):
            where = "PR description"
        elif any(mine(c) for c in pr["comments"]["nodes"]):
            where = "comment"
        elif any(mine(r) for r in pr["reviews"]["nodes"]):
            where = "review"
        elif any(mine(c) for t in pr["reviewThreads"]["nodes"]
                 for c in t["comments"]["nodes"]):
            where = "review comment"
        out[f"{repo}#{n}"] = where
    return out


def my_eyes(prs, batch=10):
    """Where I put 👀 on each PR (description, any comment or review), or
    None. PRs whose lookup failed are missing from the result."""
    out = {}
    for i in range(0, len(prs), batch):
        chunk = prs[i:i + batch]
        try:
            out.update(_eyes_query(chunk))
        except subprocess.CalledProcessError:
            # one broken PR must not hide the others
            for pr in chunk if len(chunk) > 1 else []:
                try:
                    out.update(_eyes_query([pr]))
                except subprocess.CalledProcessError:
                    pass
    return out


def involved_prs(login):
    """Open PRs I'm involved in or asked to review, notification or not."""
    out = {}
    for q in (f"involves:{login}", f"review-requested:{login}"):
        res = gh_json(["api", "-X", "GET", "search/issues", "-f",
                       f"q=is:pr is:open archived:false {q}",
                       "-f", "per_page=100"])
        for x in res.get("items", []):
            repo = x["repository_url"].split("/repos/", 1)[1]
            out[f"{repo}#{x['number']}"] = (
                repo, x["number"], (x.get("user") or {}).get("login", ""))
    return out

def pending_replies(repo, n, login):
    """Review threads I took part in whose last comment isn't mine."""
    threads = {}
    for c in api(f"repos/{repo}/pulls/{n}/comments?per_page=100"):
        threads.setdefault(c.get("in_reply_to_id") or c["id"], []).append(c)
    out = []
    for cs in threads.values():
        cs.sort(key=lambda c: c["id"])
        who = [(c.get("user") or {}).get("login", "").lower() for c in cs]
        if login.lower() in who and who[-1] != login.lower():
            out.append(cs)
    return out


def handle_replies(s, a, login, repo, n, pr, baseline):
    """Launch for unhandled replies to my review comments. False = retry."""
    key = f"{repo}#{n}"
    done = set(s["replies"].get(key, []))
    try:
        new = [cs for cs in pending_replies(repo, n, login)
               if cs[-1]["id"] not in done]
        if new and not baseline:
            pr = pr or pr_view(repo, n)
            if pr.get("state") != "OPEN":
                return True
            links = " ".join(cs[-1].get("html_url", "") for cs in new)
            thread = [activity_item("inline comment", c)
                      for cs in new for c in cs]
            if not launch(repo, n, pr, f"reply to my review comment(s): {links}",
                          a.agent, a.max_agents, a.stale_lock_hours, a.dry_run,
                          gate=lambda: thread):
                return False
    except subprocess.CalledProcessError:
        return False
    if not a.dry_run:
        s["replies"][key] = sorted(done | {cs[-1]["id"] for cs in new})
    return True


def activity_item(kind, x):
    return {
        "kind": kind, "id": x.get("id"), "state": x.get("state"),
        "updated_at": x.get("updated_at"), "submitted_at": x.get("submitted_at"),
        "user": (x.get("user") or {}).get("login"), "path": x.get("path"),
        "at": x.get("updated_at") or x.get("submitted_at") or "",
        "body": x.get("body") or "",
    }


def activity(repo, n):
    """Reviews, inline comments and conversation comments of a PR."""
    out = []
    for kind, endpoint in (
        ("review", f"repos/{repo}/pulls/{n}/reviews?per_page=100"),
        ("inline comment", f"repos/{repo}/pulls/{n}/comments?per_page=100"),
        ("comment", f"repos/{repo}/issues/{n}/comments?per_page=100"),
    ):
        out += [activity_item(kind, x) for x in api(endpoint)]
    return out


def fingerprint(items):
    data = [[x["id"], x["state"], x["updated_at"], x["submitted_at"],
             x["user"]] for x in items]
    raw = json.dumps(data, sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(raw).hexdigest()


def since(items, t):
    """Activity newer than t (all of it, capped, when never judged)."""
    items = sorted(items, key=lambda x: x["at"])
    return [x for x in items if x["at"] > t] if t else items[-10:]


def now_iso():
    return dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def failing_checks(pr):
    bad = {"FAILURE", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "ERROR",
           "STARTUP_FAILURE"}
    return [c.get("name") or c.get("context") or "?"
            for c in pr.get("statusCheckRollup") or []
            if (c.get("conclusion") or c.get("state")) in bad]


def jev_worth_it(repo, n, pr, trigger, new):
    """Ask Jev whether the new activity needs the agent at all.

    Returns (launch, note). Anything but a confident "no" launches, so an
    unsure or unreachable Jev never swallows real feedback."""
    author = (pr.get("author") or {}).get("login", "")
    own = author.lower() == LOGIN.lower()
    lines = [
        f"GitHub pull request {repo}#{n}: {pr.get('title')}",
        f"PR author: {author} ("
        + ("Matthias's own PR" if own else
           "someone else's PR; Matthias is a reviewer") + ")",
        f"Matthias's GitHub login: {LOGIN}",
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
    req = {"state": "\n".join(lines), "questions": [{
        "id": "act", "type": "noul",
        "question": "Should a coding agent working for Matthias act on this "
                    "PR now: a concrete change request, a question that needs "
                    "Matthias's answer, a reply in his review thread that "
                    "needs a response, or a failing CI check on his own PR?",
        "criteria": {
            "true": "at least one item needs a code change or a response "
                    "from Matthias",
            "false": "only bot summaries, approvals, LGTMs, thanks, "
                     "acknowledgements, Matthias's own activity, or nothing",
        },
    }]}
    try:
        # exit 3 = escalated; the verdict is still on stdout
        out = run(JEV + ["judge"], input=json.dumps(req), timeout=60,
                  check=False).stdout
        v = json.loads(out)["verdicts"][0]
    except (subprocess.CalledProcessError, OSError, ValueError, KeyError,
            IndexError) as e:
        return True, f"jev unavailable, launching anyway: {e!r}"
    note = f"jev p={v.get('answer')} conf={v.get('confidence')}"
    if v.get("escalate"):
        return True, f"{note} unsure ({v.get('reason')}), launching anyway"
    return v.get("answer", 1) >= 0.5, note

def worktree(repo, n):
    slug = repo.replace("/", "__")
    clone, remote = LOCAL.get(repo) or (ROOT / "repos" / slug, "origin")
    wt = ROOT / "worktrees" / slug / f"pr-{n}"
    branch = f"review/pr-{n}"

    if not clone.exists():
        clone.parent.mkdir(parents=True, exist_ok=True)
        log(f"cloning {repo}")
        run(["gh", "repo", "clone", repo, str(clone), "--", "--filter=blob:none"],
            timeout=1800)

    if not wt.exists():
        wt.parent.mkdir(parents=True, exist_ok=True)
        log(f"creating worktree for {repo}#{n} from {clone}")
        run(["git", "fetch", "--quiet", remote], cwd=clone, timeout=900)
        run(["git", "worktree", "prune"], cwd=clone)
        run(["git", "worktree", "add", "--detach", str(wt)], cwd=clone)
        # gh sets up the head branch + push remote (also for forks)
        run(["gh", "pr", "checkout", str(n), "--repo", repo,
             "--branch", branch], cwd=wt)
    elif not run(["git", "status", "--porcelain"], cwd=wt).stdout.strip():
        run(["gh", "pr", "checkout", str(n), "--repo", repo,
             "--branch", branch], cwd=wt, check=False)
    else:
        log(f"{repo}#{n}: preserving existing local changes")
    return wt


def prompt(repo, n, pr, trigger):
    author = (pr.get("author") or {}).get("login", "")
    head = "{}/{}".format(
        (pr.get("headRepositoryOwner") or {}).get("login", "?"),
        (pr.get("headRepository") or {}).get("name", "?"),
    )
    base = pr.get("baseRefName")
    remote = (LOCAL.get(repo) or (None, "origin"))[1]  # upstream for forks
    if author.lower() == LOGIN.lower():
        # own work: keep it current with the base branch
        branch_rule = f"""This is Matthias's OWN PR (own work):
- before changing anything: `git fetch {remote} && git rebase {remote}/{base}`
- after committing: fetch and rebase onto {remote}/{base} again
- if a rebase conflicts, stop and explain it here; do not force-resolve
- push the rebased branch with `git push --force-with-lease` (only to this
  PR's branch)"""
        force = "do NOT force-push, except `--force-with-lease` after the rebase"
    else:
        # someone else's PR: never rewrite their history
        branch_rule = f"""This PR belongs to {author} (review work, not Matthias's own):
- do NOT rebase, rewrite history or force-push
- add commits on top of the current PR head; push fast-forward only with
  plain `git push`
- if the branch is behind or conflicts with {base}, report it here instead"""
        force = "do NOT force-push"
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

Use gh/git to inspect recent reviews, inline review comments, PR discussion,
commits and CI/checks.

If there is new, actionable, unambiguous feedback:
- make the smallest appropriate change
- run focused tests/lint
- commit with a concise human-style message
- VERIFY push access to the existing PR head repository/branch
- only then push to that EXISTING branch

If you cannot push, keep the patch local and explain it to Matthias.
If nothing is actionable, change nothing.
If a human/architectural decision is needed, explain it to Matthias here.

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
"""


def locks(stale_hours):
    d = ROOT / "locks"
    d.mkdir(parents=True, exist_ok=True)
    cutoff = time.time() - stale_hours * 3600
    out = []
    for p in d.glob("*.lock"):
        try:
            tmux = json.loads(p.read_text()).get("tmux")
        except (OSError, ValueError):
            tmux = None
        gone = tmux and run(["tmux", "has-session", "-t", f"={tmux}"],
                            check=False).returncode != 0
        if gone or p.stat().st_mtime < cutoff:
            log(f"removing stale lock {p.name}")
            p.unlink(missing_ok=True)
        else:
            out.append(p)
    return out


def launch(repo, n, pr, trigger, agent, max_agents, stale_hours, dry,
           gate=None):
    """True once the event is handled: agent started, or Jev said skip.

    gate: callable returning the new activity to judge first; None starts
    the agent unconditionally (explicit 👀 opt-in)."""
    try:
        return _launch(repo, n, pr, trigger, agent, max_agents, stale_hours,
                       dry, gate)
    except (subprocess.CalledProcessError, OSError, RuntimeError) as e:
        err = getattr(e, "stderr", "") or e
        log(f"{repo}#{n}: launch failed; keeping event pending: {err}")
        return False


def _launch(repo, n, pr, trigger, agent, max_agents, stale_hours, dry,
            gate):
    lock = ROOT / "locks" / f"{repo.replace('/', '__')}__{n}.lock"
    if lock.exists():
        log(f"{repo}#{n}: already running; keeping event pending")
        return False
    if not dry and len(locks(stale_hours)) >= max_agents:
        log(f"{repo}#{n}: agent limit reached ({max_agents}); keeping event pending")
        return False

    if gate and JEV:
        go, note = jev_worth_it(repo, n, pr, trigger, gate())
        log(f"{repo}#{n}: {note}")
        if not go:
            log(f"{repo}#{n}: nothing actionable [{trigger}]; not launching")
            return True

    log(f"matched {repo}#{n}: {pr['title']} [{trigger}]")
    if dry:
        return True

    wt = worktree(repo, n)
    session = ROOT / "sessions" / repo.replace("/", "__") / f"pr-{n}"
    session.mkdir(parents=True, exist_ok=True)
    pf = session / "prompt.txt"
    runner = session / "run-agent.command"
    pf.write_text(prompt(repo, n, pr, trigger))

    agent_path = shutil.which(agent)
    if not agent_path:
        raise RuntimeError(f"{agent} is not installed")
    guard_bin = ROOT / "bin"
    guard_bin.mkdir(parents=True, exist_ok=True)
    (guard_bin / "gh").write_text(GH_GUARD)
    (guard_bin / "gh").chmod(0o755)

    agent_args = ""
    if agent == "claude":
        # Remote Control lists the session on claude.ai and in Claude Desktop
        name = shlex.quote(f"PR {repo}#{n}")
        agent_args = f"--name {name} --remote-control {name}"

    tmux = None
    if LAUNCHER == "tmux":
        tmux = re.sub(r"[^A-Za-z0-9_-]", "-", f"pr-{repo}-{n}")
    lock.write_text(json.dumps({"repo": repo, "pr": n, "started": time.time(),
                                "tmux": tmux}))
    runner.write_text(f"""#!/usr/bin/env zsh
set -u
LOCK={shlex.quote(str(lock))}
trap 'rm -f "$LOCK"' EXIT INT TERM HUP
cd {shlex.quote(str(wt))} || exit 1
export GH_REVIEW_AGENT_REAL_GH={shlex.quote(shutil.which("gh"))}
export PATH={shlex.quote(str(guard_bin))}:"$PATH"
clear
echo "GitHub PR review agent: {repo}#{n}"
echo "agent: {agent} (gh is read-only)"
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
            run(["tmux", "new-session", "-d", "-s", tmux, "-c", str(wt),
                 "zsh", str(runner)])
            log(f"{repo}#{n}: attach with: tmux attach -t {tmux}")
        else:
            subprocess.run(["open", "-a", "Terminal", str(runner)], check=True)
    except (subprocess.CalledProcessError, OSError):
        lock.unlink(missing_ok=True)
        raise
    return True


def poll(s, a, login):
    """One pass over notifications, candidates and watched PRs."""
    since = (
        dt.datetime.now(dt.timezone.utc) - dt.timedelta(hours=a.lookback_hours)
    ).replace(microsecond=0).isoformat().replace("+00:00", "Z")
    ns = api(f"notifications?all=true&since={since}&per_page=50")
    log(f"fetched {len(ns)} notifications from last {a.lookback_hours}h")

    first_live = not s["initialized"] and not a.dry_run
    cutoff = time.time() - a.lookback_hours * 3600
    for key, item in list(s["candidates"].items()):
        if item.get("seen_at", 0) < cutoff:
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
                    s, a, login, repo, n, None, baseline):
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
            s["candidates"][key] = {
                "repo": repo, "pr": n, "seen_at": time.time()
            }
            stats["candidate"] += 1
            if handle_replies(s, a, login, repo, n, pr, baseline):
                s["seen"][nid] = updated
            continue

        if s["seen"].get(nid) == updated:
            continue

        stats["mine"] += 1
        if first_live and not a.process_existing:
            s["seen"][nid] = updated
            continue

        t = s["handled"].get(key)
        if launch(repo, n, pr, f"my PR notification ({x.get('reason')})",
                  a.agent, a.max_agents, a.stale_lock_hours, a.dry_run,
                  gate=lambda: since(activity(repo, n), t)):
            if not a.dry_run:
                s["seen"][nid] = updated
                s["handled"][key] = now_iso()

    # 2) Non-owned PRs I'm involved in, even without a recent notification,
    #    so a 👀 on an older PR still opts it in.
    try:
        involved = involved_prs(login)
    except subprocess.CalledProcessError as e:
        log(f"PR search failed; using notification candidates only: "
            f"{(e.stderr or '').strip()}")
        involved = {}
    for key, (repo, n, author) in involved.items():
        if (author.lower() == login.lower() or key in s["watched"]
                or not repo_ok(repo, a.repo, a.exclude_repo)):
            continue
        s["candidates"][key] = {"repo": repo, "pr": n, "seen_at": time.time()}

    # 3) Candidates: detect a 👀 added AFTER we first saw them.
    candidates = sorted(
        s["candidates"].items(),
        key=lambda kv: kv[1].get("seen_at", 0),
        reverse=True
    )[:a.candidate_limit]
    eyes = my_eyes([(i["repo"], int(i["pr"])) for _, i in candidates])

    for key, item in candidates:
        repo, n = item["repo"], int(item["pr"])
        where = eyes.get(key)
        if not where:
            continue  # no 👀, or lookup failed

        try:
            pr = pr_view(repo, n)
        except subprocess.CalledProcessError:
            continue
        if pr.get("state") != "OPEN":
            s["candidates"].pop(key, None)
            continue

        if a.dry_run:
            log(f"👀 detected {repo}#{n} (on {where})")

        if launch(repo, n, pr, f"👀 opt-in (on {where})", a.agent,
                  a.max_agents, a.stale_lock_hours, a.dry_run):
            if not a.dry_run:
                try:
                    fp = fingerprint(activity(repo, n))
                except subprocess.CalledProcessError:
                    fp = None  # next watch pass relaunches on change
                s["handled"][key] = now_iso()
                s["watched"][key] = {
                    "repo": repo, "pr": n, "fingerprint": fp
                }
                s["candidates"].pop(key, None)

    # 4) Already opted-in PRs: trigger only when review/discussion changes.
    watched = list(s["watched"].items())
    eyes = my_eyes([(i["repo"], int(i["pr"])) for _, i in watched])
    for key, item in watched:
        repo, n = item["repo"], int(item["pr"])
        if key not in eyes:
            continue  # lookup failed; keep watching
        if not eyes[key]:
            log(f"{repo}#{n}: 👀 removed; stopping watch")
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
        if fp == item.get("fingerprint"):
            continue

        t = s["handled"].get(key)
        if launch(repo, n, pr, "review/discussion changed", a.agent,
                  a.max_agents, a.stale_lock_hours, a.dry_run,
                  gate=lambda: since(items, t)):
            if not a.dry_run:
                item["fingerprint"] = fp
                s["handled"][key] = now_iso()

    log(
        f"{'dry-run ' if a.dry_run else ''}summary: "
        f"own_new={stats['mine']} non_owned={stats['candidate']} "
        f"watched={len(s['watched'])} ignored={stats['ignored']}"
        + (" (first run: existing own notifications recorded only)"
           if first_live and not a.process_existing else "")
    )
    if not a.dry_run:
        s["initialized"] = True
        save_state(s)


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--repo", action="append", default=[])
    p.add_argument("--exclude-repo", action="append", default=[])
    p.add_argument("--agent", choices=["codex", "claude"], default="codex")
    p.add_argument("--remote", help="remote of the local checkout to watch "
                   "(default: origin; e.g. upstream for a fork)")
    p.add_argument("--launcher", choices=["auto", "terminal", "tmux"],
                   default="auto",
                   help="auto: Terminal on a macOS desktop session, else tmux")
    p.add_argument("--interval", type=int, default=60)
    p.add_argument("--lookback-hours", type=int, default=168)
    p.add_argument("--max-agents", type=int, default=1)
    p.add_argument("--candidate-limit", type=int, default=50)
    p.add_argument("--stale-lock-hours", type=int, default=12)
    p.add_argument("--process-existing", action="store_true")
    p.add_argument("--once", action="store_true")
    p.add_argument("--dry-run", action="store_true")
    p.add_argument("--jev-cmd", default="npx -y jev-use@0.8.0",
                   help="jev-use command for the pre-launch check")
    p.add_argument("--no-jev", action="store_true",
                   help="launch on every trigger without asking Jev first")
    p.add_argument("--reset-state", action="store_true")
    a = p.parse_args()
    a.repo = [repo_pattern(x) for x in a.repo]
    a.exclude_repo = [repo_pattern(x) for x in a.exclude_repo]

    # Inside a local checkout: watch its GitHub repo and branch worktrees
    # off it instead of a cached clone.
    top = run(["git", "rev-parse", "--show-toplevel"], check=False)
    if top.returncode == 0:
        remote = a.remote or "origin"
        url = run(["git", "remote", "get-url", remote], cwd=top.stdout.strip(),
                  check=False).stdout.strip()
        repo = repo_pattern(url)
        if re.fullmatch(r"[\w.-]+/[\w.-]+", repo):
            LOCAL[repo] = (Path(top.stdout.strip()), remote)
            if not a.repo:
                a.repo = [repo]
        elif a.remote:
            raise SystemExit(f"remote {a.remote!r} is not a GitHub repo here")
    elif a.remote:
        raise SystemExit("--remote needs to run inside a git checkout")

    global LAUNCHER, LOGIN, JEV
    if not a.no_jev:
        JEV = shlex.split(a.jev_cmd)
        if not shutil.which(JEV[0]):
            log(f"jev check disabled: {JEV[0]} not found")
            JEV = None
    LAUNCHER = a.launcher
    if LAUNCHER == "auto":
        gui = sys.platform == "darwin" and run(
            ["launchctl", "managername"], check=False).stdout.strip() == "Aqua"
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
    while True:
        try:
            login = run(["gh", "api", "user", "--jq", ".login"]).stdout.strip()
            if login:
                break
            err = "empty login"
        except subprocess.CalledProcessError as e:
            err = (e.stderr or "").strip() or e
        if a.once:
            raise SystemExit(f"cannot determine GitHub user: {err}")
        log(f"cannot determine GitHub user, retrying in 30s: {err}")
        time.sleep(30)

    LOGIN = login
    log(f"GitHub user: {login}")
    log(f"agent: {a.agent} (interactive)")
    log(f"launcher: {LAUNCHER}")
    log(f"max active agents: {a.max_agents}")
    log(f"jev check before launch: {' '.join(JEV) if JEV else 'off'}")
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


if __name__ == "__main__":
    main()
