"""The agent's gh shim must pass reads through and refuse every write."""

import stat
import subprocess
import sys
from pathlib import Path

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
    monkeypatch.setenv("LLM_REVIEW_AGENT_NO_PUSH", "1")
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


def test_git_push_allowed_on_own_pr(
    git_guard: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("LLM_REVIEW_AGENT_NO_PUSH", "0")
    r = gh(git_guard, "push")
    assert r.returncode == 0
    assert r.stdout.splitlines() == ["REAL", "push"]
