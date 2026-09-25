import subprocess
from typing import Any

import pytest

from llm_review_agent import app

NO = {"reactionGroups": [{"content": "EYES", "viewerHasReacted": False}]}
YES = {"reactionGroups": [{"content": "EYES", "viewerHasReacted": True}]}
OTHER = {"reactionGroups": [{"content": "HEART", "viewerHasReacted": True}]}


def pr(**kw: Any) -> dict[str, Any]:
    body: dict[str, Any] = {
        "reactionGroups": [],
        "comments": {"nodes": [NO]},
        "reviews": {"nodes": [NO]},
        "reviewThreads": {"nodes": [{"comments": {"nodes": [NO]}}]},
    }
    body.update(kw)
    return {"pullRequest": body}


def test_eyes_found_anywhere(monkeypatch: pytest.MonkeyPatch) -> None:
    data = {
        "p0": pr(**YES),
        "p1": pr(comments={"nodes": [NO, YES]}),
        "p2": pr(reviews={"nodes": [YES]}),
        "p3": pr(reviewThreads={"nodes": [{"comments": {"nodes": [NO, YES]}}]}),
        "p4": pr(comments={"nodes": [OTHER]}),
        "p5": {"pullRequest": None},
    }
    monkeypatch.setattr(app, "gh_json", lambda args: {"data": data})
    got = app._eyes_query([("o/r", i) for i in range(6)])
    assert got == {
        "o/r#0": "PR description",
        "o/r#1": "comment",
        "o/r#2": "review",
        "o/r#3": "review comment",
        "o/r#4": None,
        # o/r#5 unknown: missing, so callers don't treat it as "👀 removed"
    }


def test_query_escapes_names(monkeypatch: pytest.MonkeyPatch) -> None:
    seen: list[str] = []

    def fake(args: list[str]) -> Any:
        seen.append(args[-1])
        return {"data": {}}

    monkeypatch.setattr(app, "gh_json", fake)
    app._eyes_query([('o"x/r', 5)])
    assert 'owner: "o\\"x"' in seen[0]
    assert "pullRequest(number: 5)" in seen[0]


def test_one_broken_pr_does_not_hide_the_batch(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    def fake(prs: list[tuple[str, int]], *_: Any) -> dict[str, str | None]:
        if ("o/r", 2) in prs:
            raise subprocess.CalledProcessError(1, "gh")
        return {f"{r}#{n}": "comment" for r, n in prs}

    monkeypatch.setattr(app, "_eyes_query", fake)
    got = app.my_eyes([("o/r", 1), ("o/r", 2), ("o/r", 3)])
    assert got == {"o/r#1": "comment", "o/r#3": "comment"}


def test_batches(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[int] = []

    def fake(prs: list[tuple[str, int]], *_: Any) -> dict[str, str | None]:
        calls.append(len(prs))
        return {}

    monkeypatch.setattr(app, "_eyes_query", fake)
    app.my_eyes([("o/r", i) for i in range(23)])
    assert calls == [10, 10, 3]


def test_configured_reaction_and_places(monkeypatch: pytest.MonkeyPatch) -> None:
    rocket = {"reactionGroups": [{"content": "ROCKET", "viewerHasReacted": True}]}
    data = {
        "p0": pr(comments={"nodes": [rocket]}),  # rocket on a comment
        "p1": pr(**YES),  # eyes on the description: not the configured reaction
        "p2": pr(**rocket),  # rocket on the description
    }
    monkeypatch.setattr(app, "gh_json", lambda args: {"data": data})
    got = app._eyes_query([("o/r", i) for i in range(3)], "rocket", ["description"])
    assert got == {"o/r#0": None, "o/r#1": None, "o/r#2": "PR description"}
