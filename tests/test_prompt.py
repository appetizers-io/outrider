from pathlib import Path
from typing import Any

import pytest

from gh_review_agent import app


def pr(author: str) -> dict[str, Any]:
    return {
        "author": {"login": author},
        "baseRefName": "main",
        "headRefName": "feat/x",
        "title": "T",
        "url": "https://github.com/o/r/pull/1",
    }


def test_own_pr_rebases_and_force_with_lease() -> None:
    text = app.prompt("o/r", 1, pr("Me"), "my PR notification")
    assert "OWN PR" in text
    assert "git fetch origin && git rebase origin/main" in text
    assert "--force-with-lease" in text


def test_others_pr_never_rebases() -> None:
    text = app.prompt("o/r", 1, pr("bob"), "t")
    assert "belongs to bob" in text
    assert "do NOT rebase" in text
    assert "git rebase" not in text
    assert "--force-with-lease" not in text
    assert "- do NOT force-push\n" in text


def test_fork_checkout_rebases_on_watched_remote(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setitem(app.LOCAL, "o/r", (Path("/src/r"), "upstream"))
    text = app.prompt("o/r", 1, pr("me"), "t")
    assert "git rebase upstream/main" in text


def test_strict_rules_always_present() -> None:
    for author in ("me", "bob"):
        text = app.prompt("o/r", 1, pr(author), "t")
        assert "do NOT post comments" in text
        assert "do NOT merge/close the PR" in text
