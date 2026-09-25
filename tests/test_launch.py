import subprocess
from pathlib import Path
from typing import Any

import pytest

from llm_review_agent import app

PR: dict[str, Any] = {"title": "T", "author": {"login": "bob"}}


@pytest.fixture
def jev_calls(monkeypatch: pytest.MonkeyPatch) -> list[str]:
    calls: list[str] = []

    def fake(
        repo: str, n: int, pr: Any, trigger: str, new: Any, cfg: Any
    ) -> tuple[bool, str]:
        calls.append(trigger)
        return trigger != "noise", "check test"

    monkeypatch.setattr(app, "LAUNCH_CHECK", object())
    monkeypatch.setattr(app, "worth_launching", fake)
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


@pytest.mark.parametrize(("author", "no_push"), [("bob", "1"), ("me", "0")])
def test_session_blocks_push_on_others_prs(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path, author: str, no_push: str
) -> None:
    import shutil

    monkeypatch.setattr(app, "worktree", lambda repo, n: tmp_path)
    monkeypatch.setattr(shutil, "which", lambda tool: f"/bin/{tool}")
    monkeypatch.setattr(
        app, "run", lambda *a, **k: subprocess.CompletedProcess([], 0, stdout="")
    )
    pr = {"title": "T", "author": {"login": author}}
    assert app.launch("o/r", 1, pr, "t", app.parse_args([]))
    session = app.ROOT / "sessions" / "o__r" / "pr-1"
    runner = (session / "run-agent.command").read_text()
    assert f"export LLM_REVIEW_AGENT_NO_PUSH={no_push}" in runner
    assert (app.ROOT / "bin" / "git").read_text() == app.GIT_GUARD
    review_only = "REVIEW ONLY" in (session / "prompt.txt").read_text()
    assert review_only is (no_push == "1")
