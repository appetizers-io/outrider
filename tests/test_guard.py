"""The agent's gh shim passes reads, gates posts on the session PR, refuses the
rest; its git shim gates pushes."""

import os
import stat
import subprocess
import sys
from pathlib import Path
from typing import Any

import pytest

from llm_review_agent import app


@pytest.fixture
def guard(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    real = tmp_path / "real-gh"
    real.write_text("#!/bin/sh\nprintf '%s\\n' REAL \"$@\"\n")
    real.chmod(real.stat().st_mode | stat.S_IEXEC)
    g = tmp_path / "gh"
    g.write_text(app.GH_GUARD)
    monkeypatch.setenv("LLM_REVIEW_AGENT_REAL_GH", str(real))
    return g


def gh(guard: Path, *args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, str(guard), *args],
        capture_output=True,
        text=True,
        check=False,
    )


@pytest.mark.parametrize(
    "args",
    [
        ["pr", "view", "1"],
        ["pr", "diff", "1"],
        ["pr", "checkout", "1"],
        ["run", "view", "123", "--log-failed"],
        ["api", "repos/o/r/pulls/1/comments"],
        ["api", "-X", "GET", "search/issues", "-f", "q=is:pr"],
        ["api", "graphql", "-f", "query=query { viewer { login } }"],
        ["search", "prs", "foo"],
        ["browse"],
    ],
)
def test_reads_pass_through(guard: Path, args: list[str]) -> None:
    r = gh(guard, *args)
    assert r.returncode == 0, r.stderr
    assert r.stdout.splitlines() == ["REAL", *args]


@pytest.mark.parametrize(
    "args",
    [
        [],
        ["pr", "comment", "1", "-b", "x"],
        ["pr", "review", "1", "--approve"],
        ["pr", "merge", "1"],
        ["pr", "close", "1"],
        ["pr"],
        ["issue", "comment", "1"],
        ["repo", "delete"],
        ["api", "-X", "POST", "repos/o/r/issues/1/comments"],
        ["api", "--method=PATCH", "repos/o/r/pulls/1"],
        ["api", "-XDELETE", "repos/o/r/issues/comments/1/reactions/2"],
        ["api", "repos/o/r/issues/1/comments", "-f", "body=hi"],
        ["api", "repos/o/r/issues/1/comments", "--raw-field=body=hi"],
        ["api", "repos/o/r/issues/1/comments", "--input", "body.json"],
        ["api", "graphql", "-f", "query=mutation { addReaction }"],
        ["api", "graphql", "-F", "query=@q.graphql"],
    ],
)
def test_writes_are_blocked(guard: Path, args: list[str]) -> None:
    r = gh(guard, *args)
    assert r.returncode != 0
    assert "blocked" in r.stderr
    assert "REAL" not in r.stdout


# --- gh: posts on the session's PR follow LLM_REVIEW_AGENT_GH_WRITES --------

POSTS = [
    ["pr", "comment", "7", "-b", "hi"],
    ["pr", "comment", "https://github.com/o/r/pull/7", "--body=hi"],
    ["pr", "review", "7", "--comment", "-b", "hi"],
    ["pr", "review", "7", "-R", "o/r", "--approve"],
    ["issue", "comment", "7", "-b", "hi"],
    ["api", "repos/o/r/pulls/7/comments", "-f", "body=hi", "-F", "line=3"],
    ["api", "/repos/O/R/pulls/7/comments/11/replies", "-f", "body=hi"],
    ["api", "-X", "POST", "repos/o/r/pulls/7/reviews", "-f", "event=COMMENT"],
    ["api", "repos/o/r/issues/7/comments", "-f", "body=hi"],
    ["api", "-X", "PATCH", "repos/o/r/issues/comments/9", "-f", "body=hi"],
    ["api", "repos/o/r/pulls/comments/9/reactions", "-f", "content=+1"],
    ["api", "-XDELETE", "repos/o/r/issues/comments/9/reactions/2"],
]

NEVER_POSTS = [
    ["pr", "merge", "7"],
    ["pr", "close", "7"],
    ["pr", "edit", "7", "--add-label", "x"],
    ["pr", "comment", "8", "-b", "hi"],  # another PR
    ["pr", "comment", "7", "-R", "o/other", "-b", "hi"],
    ["pr", "comment", "-b", "hi"],  # PR from the branch: not explicit
    ["pr", "comment", "7", "--web"],
    ["pr", "comment", "7", "--body-file", "-"],
    ["api", "repos/o/r/pulls/8/comments", "-f", "body=hi"],
    ["api", "repos/o/other/pulls/7/comments", "-f", "body=hi"],
    ["api", "-X", "PUT", "repos/o/r/pulls/7/merge"],
    ["api", "-X", "PATCH", "repos/o/r/pulls/7", "-f", "state=closed"],
    ["api", "-X", "DELETE", "repos/o/r/issues/comments/9"],
    ["api", "repos/o/r/issues/7/labels", "-f", "labels[]=x"],
    ["api", "repos/{owner}/{repo}/pulls/7/comments", "-f", "body=hi"],
    ["api", "graphql", "-f", "query=mutation { addComment }"],
]


@pytest.fixture
def posting_guard(guard: Path, tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Any:
    """gh guard for PR o/r#7 in a mode, with a fake osascript answering `button`."""
    monkeypatch.setenv("LLM_REVIEW_AGENT_REPO", "o/r")
    monkeypatch.setenv("LLM_REVIEW_AGENT_PR", "7")

    def make(mode: str, button: str | None = "Post") -> Path:
        monkeypatch.setenv("LLM_REVIEW_AGENT_GH_WRITES", mode)
        osa = tmp_path / "osascript"
        if button is None:
            osa.unlink(missing_ok=True)
        else:
            osa.write_text(
                f'#!/bin/sh\necho "$@" > {tmp_path}/dialog\n'
                + ("echo Post\n" if button == "Post" else "exit 1\n")
            )
            osa.chmod(0o755)
        guard.write_text(app.GH_GUARD.replace('"/usr/bin/osascript"', repr(str(osa))))
        return guard

    return make


@pytest.mark.parametrize("args", POSTS)
def test_posts_are_blocked_when_writes_are_off(
    posting_guard: Any, args: list[str]
) -> None:
    r = gh(posting_guard("never"), *args)
    assert r.returncode != 0
    assert "read-only" in r.stderr
    assert "REAL" not in r.stdout


@pytest.mark.parametrize("args", POSTS)
def test_posts_on_the_session_pr_pass_when_allowed(
    posting_guard: Any, tmp_path: Path, args: list[str]
) -> None:
    r = gh(posting_guard("allow"), *args)
    assert r.returncode == 0, r.stderr
    assert r.stdout.splitlines() == ["REAL", *args]
    assert not (tmp_path / "dialog").exists()


@pytest.mark.parametrize("mode", ["ask", "allow"])
@pytest.mark.parametrize("args", NEVER_POSTS)
def test_other_writes_stay_blocked(
    posting_guard: Any, tmp_path: Path, mode: str, args: list[str]
) -> None:
    r = gh(posting_guard(mode), *args)
    assert r.returncode != 0
    assert "blocked" in r.stderr
    assert "REAL" not in r.stdout
    assert not (tmp_path / "dialog").exists()


def test_ask_shows_the_text_and_posts_after_approval(
    posting_guard: Any, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.chdir(tmp_path)
    (tmp_path / "comment.md").write_text("Please handle the error here.")
    args = [
        "api", "repos/o/r/pulls/7/comments", "-f", "path=store.go",
        "-F", "line=618", "-F", "body=@comment.md",
    ]  # fmt: skip
    r = gh(posting_guard("ask"), *args)
    assert r.returncode == 0, r.stderr
    assert r.stdout.splitlines() == ["REAL", *args]
    dialog = (tmp_path / "dialog").read_text()
    assert "gh api repos/o/r/pulls/7/comments" in dialog
    assert "body: Please handle the error here." in dialog


@pytest.mark.parametrize("button", ["Deny", None])
def test_denied_or_unavailable_approval_blocks_post(
    posting_guard: Any, button: str | None
) -> None:
    r = gh(posting_guard("ask", button), "pr", "comment", "7", "-b", "hi")
    assert r.returncode != 0
    assert "did not approve" in r.stderr
    assert "REAL" not in r.stdout


def test_ask_mode_does_not_ask_before_gh_reads(
    posting_guard: Any, tmp_path: Path
) -> None:
    r = gh(posting_guard("ask", "Deny"), "api", "repos/o/r/pulls/7/comments")
    assert r.returncode == 0
    assert not (tmp_path / "dialog").exists()


# --- git: review-only sessions must not push --------------------------------


@pytest.fixture
def git_guard(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    real = tmp_path / "real-git"
    # fake git: answers `config --get alias.X` from env, echoes everything else
    real.write_text(
        "#!/bin/sh\n"
        'if [ "$1" = config ] && [ "$2" = --get ]; then\n'
        '  case "$3" in alias.p) echo "push origin";; alias.shipit) echo "!git push";;'
        " alias.st) echo status;; esac; exit 0\n"
        "fi\n"
        "printf '%s\\n' REAL \"$@\"\n"
    )
    real.chmod(real.stat().st_mode | stat.S_IEXEC)
    g = tmp_path / "git"
    g.write_text(app.GIT_GUARD)
    monkeypatch.setenv("LLM_REVIEW_AGENT_REAL_GIT", str(real))
    monkeypatch.setenv("LLM_REVIEW_AGENT_PUSH", "review-only")
    return g


@pytest.mark.parametrize(
    "args",
    [
        ["push"],
        ["push", "origin", "HEAD:feature"],
        ["-C", "/repo", "push"],
        ["-c", "user.name=x", "push", "--force"],
        ["--git-dir=/repo/.git", "push"],
        ["p"],  # alias to push
        ["shipit"],  # shell alias running push
    ],
)
def test_git_push_blocked_in_review_only_session(
    git_guard: Path, args: list[str]
) -> None:
    r = gh(git_guard, *args)
    assert r.returncode != 0
    assert "review only, never push" in r.stderr
    assert "REAL" not in r.stdout


@pytest.mark.parametrize(
    "args",
    [["status"], ["st"], ["log", "--oneline"], ["-C", "/repo", "diff"], ["fetch"]],
)
def test_git_reads_pass_through(git_guard: Path, args: list[str]) -> None:
    r = gh(git_guard, *args)
    assert r.returncode == 0, r.stderr
    assert r.stdout.splitlines() == ["REAL", *args]


def test_git_push_allowed_when_configured(
    git_guard: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("LLM_REVIEW_AGENT_PUSH", "allow")
    r = gh(git_guard, "push")
    assert r.returncode == 0
    assert r.stdout.splitlines() == ["REAL", "push"]


@pytest.mark.parametrize("args", [["push"], ["p"], ["-C", "/repo", "push"]])
def test_git_push_blocked_when_pushing_is_off(
    git_guard: Path, monkeypatch: pytest.MonkeyPatch, args: list[str]
) -> None:
    monkeypatch.setenv("LLM_REVIEW_AGENT_PUSH", "never")
    r = gh(git_guard, *args)
    assert r.returncode != 0
    assert "Pushing is off" in r.stderr
    assert "REAL" not in r.stdout


def test_unknown_push_mode_is_review_only(
    git_guard: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.delenv("LLM_REVIEW_AGENT_PUSH")
    assert "review only" in gh(git_guard, "push").stderr


@pytest.fixture
def asking_guard(
    git_guard: Path, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> Any:
    """Guard in ask mode with a fake osascript answering `button`."""
    monkeypatch.setenv("LLM_REVIEW_AGENT_PUSH", "ask")
    monkeypatch.setenv("GIT_CONFIG_COUNT", "1")
    monkeypatch.setenv("GIT_CONFIG_KEY_0", "url.x://.pushInsteadOf")
    real = Path(os.environ["LLM_REVIEW_AGENT_REAL_GIT"])
    real.write_text(
        real.read_text().replace(
            "printf '%s\\n' REAL",
            "printf 'count=%s\\n' \"$GIT_CONFIG_COUNT\"\nprintf '%s\\n' REAL",
        )
    )

    def make(button: str | None) -> Path:
        osa = tmp_path / "osascript"
        if button is None:
            osa.unlink(missing_ok=True)
        else:
            osa.write_text(
                f'#!/bin/sh\necho "$@" > {tmp_path}/dialog\n'
                + ("echo Push\n" if button == "Push" else "exit 1\n")
            )
            osa.chmod(0o755)
        git_guard.write_text(
            app.GIT_GUARD.replace('"/usr/bin/osascript"', repr(str(osa)))
        )
        return git_guard

    return make


def test_approved_push_runs_without_the_push_trap(
    asking_guard: Any, tmp_path: Path
) -> None:
    r = gh(asking_guard("Push"), "push", "origin")
    assert r.returncode == 0, r.stderr
    assert "REAL\npush\norigin" in r.stdout
    assert "count=\n" in r.stdout  # GIT_CONFIG_* dropped for the real push
    assert "git push origin" in (tmp_path / "dialog").read_text()


@pytest.mark.parametrize("button", ["Deny", None])
def test_denied_or_unavailable_approval_blocks_push(
    asking_guard: Any, button: str | None
) -> None:
    r = gh(asking_guard(button), "push")
    assert r.returncode != 0
    assert "did not approve" in r.stderr
    assert "REAL\npush" not in r.stdout


def test_ask_mode_does_not_ask_for_reads(asking_guard: Any, tmp_path: Path) -> None:
    r = gh(asking_guard("Deny"), "status")
    assert r.returncode == 0
    assert "count=1" in r.stdout  # the push trap stays for everything else
    assert not (tmp_path / "dialog").exists()
