"""The agent's gh shim must pass reads through and refuse every write."""

import stat
import subprocess
import sys
from pathlib import Path

import pytest

from gh_review_agent import app


@pytest.fixture
def guard(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    real = tmp_path / "real-gh"
    real.write_text("#!/bin/sh\nprintf '%s\\n' REAL \"$@\"\n")
    real.chmod(real.stat().st_mode | stat.S_IEXEC)
    g = tmp_path / "gh"
    g.write_text(app.GH_GUARD)
    monkeypatch.setenv("GH_REVIEW_AGENT_REAL_GH", str(real))
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
