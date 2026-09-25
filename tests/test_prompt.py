from pathlib import Path
from typing import Any

import pytest

from llm_review_agent import app


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


def test_others_pr_is_review_only() -> None:
    text = app.prompt("o/r", 1, pr("bob"), "t")
    assert "REVIEW ONLY: this PR belongs to bob" in text
    assert "not even trivial fixes (lint" in text
    assert "`git push` is blocked" in text
    assert "push to that EXISTING branch" not in text
    assert "make the smallest appropriate change" not in text
    assert "git rebase" not in text


def test_others_pr_push_only_when_allowed() -> None:
    text = app.prompt("o/r", 1, pr("bob"), "t", allow_push=True)
    assert "REVIEW ONLY" not in text
    assert "push fast-forward only" in text
    assert "--force-with-lease" not in text


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


def test_reply_session_is_scoped_to_its_threads() -> None:
    urls = ["https://github.com/o/r/pull/1#discussion_r11"]
    text = app.prompt("o/r", 1, pr("bob"), "reply", scope=urls)
    assert "ONLY about the comment(s) below, where someone replied" in text
    assert "- https://github.com/o/r/pull/1#discussion_r11" in text
    assert "inspect recent reviews" not in text


def test_whole_pr_without_scope() -> None:
    text = app.prompt("o/r", 1, pr("bob"), "👀")
    assert "inspect recent reviews" in text
    assert "SCOPE" not in text


def test_owner_name_and_extra_instructions(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(app, "OWNER", "Matze")
    text = app.prompt("o/r", 1, pr("me"), "t", extra="Run make test first.\n")
    assert "explain it to Matze" in text
    assert "Matze's OWN PR" in text
    assert "Matthias" not in text
    assert text.rstrip().endswith("Run make test first.")


def test_mention_session_says_why() -> None:
    text = app.prompt(
        "o/r",
        1,
        pr("bob"),
        "m",
        scope=["https://x/c1"],
        scope_why="where someone mentioned @me",
    )
    assert "ONLY about the comment(s) below, where someone mentioned @me" in text
