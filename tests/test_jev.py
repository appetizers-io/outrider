import json
import subprocess
from typing import Any

import pytest

from gh_review_agent import app

PR: dict[str, Any] = {"title": "T", "author": {"login": "bob"}}


def item(body: str, user: str = "alice") -> app.ActivityItem:
    return {
        "kind": "comment",
        "id": 1,
        "state": None,
        "updated_at": "2026-01-01T00:00:00Z",
        "submitted_at": None,
        "user": user,
        "path": None,
        "at": "2026-01-01T00:00:00Z",
        "body": body,
    }


@pytest.fixture
def jev(monkeypatch: pytest.MonkeyPatch) -> list[dict[str, Any]]:
    """Fake jev-use; append the JSON it should print next."""
    monkeypatch.setattr(app, "JEV", ["jev-use"])
    outputs: list[dict[str, Any]] = []

    def fake_run(args: list[str], **kw: Any) -> subprocess.CompletedProcess[str]:
        assert args == ["jev-use", "judge"]
        assert kw["check"] is False  # exit 3 (escalated) must still be parsed
        json.loads(kw["input"])
        return subprocess.CompletedProcess(args, 3, stdout=json.dumps(outputs.pop(0)))

    monkeypatch.setattr(app, "run", fake_run)
    return outputs


def verdict(**kw: Any) -> dict[str, Any]:
    return {"verdicts": [{"id": "act", "escalate": False, **kw}]}


def test_confident_no_skips(jev: list[dict[str, Any]]) -> None:
    jev.append(verdict(answer=0.04, confidence=0.92))
    go, note = app.jev_worth_it("o/r", 1, PR, "t", [item("LGTM")])
    assert not go
    assert "p=0.04" in note


def test_yes_launches(jev: list[dict[str, Any]]) -> None:
    jev.append(verdict(answer=0.89, confidence=0.78))
    assert app.jev_worth_it("o/r", 1, PR, "t", [item("please fix")])[0]


def test_unsure_launches(jev: list[dict[str, Any]]) -> None:
    jev.append(verdict(answer=0.1, escalate=True, reason="unsure"))
    go, note = app.jev_worth_it("o/r", 1, PR, "t", [item("hm")])
    assert go
    assert "unsure" in note


@pytest.mark.parametrize("bad", [{}, {"verdicts": []}, verdict(answer="yes")])
def test_malformed_launches(jev: list[dict[str, Any]], bad: dict[str, Any]) -> None:
    jev.append(bad)
    assert app.jev_worth_it("o/r", 1, PR, "t", [item("x")])[0]


def test_unreachable_launches(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(app, "JEV", ["definitely-not-installed-jev"])
    go, note = app.jev_worth_it("o/r", 1, PR, "t", [item("x")])
    assert go
    assert "unavailable" in note


def test_request_keeps_newest_when_trimming() -> None:
    new = [item(f"old {i} " + "x" * 1400) for i in range(60)] + [item("NEWEST")]
    state = app.jev_request("o/r", 1, PR, "t", new)["state"]
    assert "NEWEST" in state
    assert "old 0 " not in state
    assert len(state) < 45000


def test_request_mentions_ownership_and_failing_checks() -> None:
    own = {
        **PR,
        "author": {"login": "ME"},
        "statusCheckRollup": [{"name": "unit", "conclusion": "FAILURE"}],
    }
    state = app.jev_request("o/r", 1, own, "t", [])["state"]
    assert "Matthias's own PR" in state
    assert "Failing CI checks: unit" in state
    assert "- (none)" in state
