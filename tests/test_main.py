import shutil
import subprocess
import time
from typing import Any

import pytest

from gh_review_agent import app


@pytest.fixture
def machine(monkeypatch: pytest.MonkeyPatch) -> None:
    """Every tool installed, not inside a git checkout, logged in as me."""

    def fake_run(args: list[str], **kw: Any) -> subprocess.CompletedProcess[str]:
        if args[:2] == ["git", "rev-parse"]:
            return subprocess.CompletedProcess(args, 128, stdout="")
        if args[:3] == ["gh", "api", "user"]:
            return subprocess.CompletedProcess(args, 0, stdout="me\n")
        raise AssertionError(args)

    monkeypatch.setattr(app, "run", fake_run)
    monkeypatch.setattr(shutil, "which", lambda tool: f"/bin/{tool}")


@pytest.mark.usefixtures("machine")
def test_main_survives_a_failing_poll(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    def boom(s: app.State, a: app.Args, login: str) -> None:
        raise subprocess.CalledProcessError(1, "gh", stderr="connection reset")

    monkeypatch.setattr(app, "poll", boom)
    app.main(["--once", "--launcher", "tmux", "--no-jev"])
    out = capsys.readouterr().out
    assert "poll failed (1x in a row), will retry: connection reset" in out
    assert app.STATE.exists()  # state saved despite the failure


@pytest.mark.usefixtures("machine")
def test_main_retries_after_unexpected_errors(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[int] = []
    sleeps: list[float] = []

    def flaky(s: app.State, a: app.Args, login: str) -> None:
        calls.append(1)
        if len(calls) < 3:
            raise KeyError("surprise")
        raise SystemExit(0)  # stop the endless loop

    monkeypatch.setattr(app, "poll", flaky)
    monkeypatch.setattr(time, "sleep", sleeps.append)
    with pytest.raises(SystemExit):
        app.main(["--launcher", "tmux", "--no-jev", "--interval", "60"])
    assert len(calls) == 3
    assert sleeps == [120, 240]  # exponential backoff


@pytest.mark.usefixtures("machine")
def test_jev_disabled_when_command_missing(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    monkeypatch.setattr(app, "poll", lambda s, a, login: None)
    monkeypatch.setattr(shutil, "which", lambda t: None if t == "npx" else f"/bin/{t}")
    app.main(["--once", "--launcher", "tmux"])
    assert app.JEV is None
    assert "jev check disabled: npx not found" in capsys.readouterr().out
