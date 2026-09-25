import subprocess
from pathlib import Path
from typing import Any

import pytest

from gh_review_agent import app

PR: dict[str, Any] = {"title": "T", "author": {"login": "bob"}}


@pytest.fixture
def jev_calls(monkeypatch: pytest.MonkeyPatch) -> list[str]:
    calls: list[str] = []

    def fake(
        repo: str, n: int, pr: Any, trigger: str, new: Any, settings: Any
    ) -> tuple[bool, str]:
        calls.append(trigger)
        return trigger != "noise", "jev test"

    monkeypatch.setattr(app, "JEV", ["jev-use"])
    monkeypatch.setattr(app, "jev_worth_it", fake)
    return calls


def lock_for(n: int) -> Path:
    p: Path = app.ROOT / "locks" / f"o__r__{n}.lock"
    p.parent.mkdir(parents=True, exist_ok=True)
    return p


def test_skip_counts_as_handled(jev_calls: list[str]) -> None:
    a = app.parse_args(["--dry-run"])
    assert app.launch("o/r", 1, PR, "noise", a, gate=list)
    assert jev_calls == ["noise"]


def test_no_gate_never_asks_jev(jev_calls: list[str]) -> None:
    assert app.launch("o/r", 1, PR, "👀", app.parse_args(["--dry-run"]))
    assert jev_calls == []


def test_running_agent_keeps_event_pending_without_jev(jev_calls: list[str]) -> None:
    lock_for(1).write_text("{}")
    assert not app.launch("o/r", 1, PR, "t", app.parse_args([]), gate=list)
    assert jev_calls == []


def test_agent_limit_keeps_event_pending_without_jev(
    jev_calls: list[str], monkeypatch: pytest.MonkeyPatch
) -> None:
    lock_for(2).write_text("{}")  # another PR's agent is running
    monkeypatch.setattr(
        app, "run", lambda *a, **k: subprocess.CompletedProcess([], 0, stdout="")
    )
    a = app.parse_args(["--max-agents", "1"])
    assert not app.launch("o/r", 1, PR, "t", a, gate=list)
    assert jev_calls == []


def test_launch_errors_keep_event_pending(monkeypatch: pytest.MonkeyPatch) -> None:
    def boom(repo: str, n: int) -> Path:
        raise subprocess.CalledProcessError(1, "git", stderr="network down")

    monkeypatch.setattr(app, "worktree", boom)
    assert not app.launch("o/r", 1, PR, "t", app.parse_args([]))
    assert not lock_for(1).exists()
