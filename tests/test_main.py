import shutil
import subprocess
import time
from typing import Any

import pytest

from llm_review_agent import app


@pytest.fixture
def machine(monkeypatch: pytest.MonkeyPatch) -> None:
    """Every tool installed, not inside a git checkout, logged in as me."""

    def fake_run(args: list[str], **kw: Any) -> subprocess.CompletedProcess[str]:
        if args[:2] == ["git", "rev-parse"]:
            return subprocess.CompletedProcess(args, 128, stdout="")
        if args[:3] == ["gh", "api", "user"]:
            return subprocess.CompletedProcess(
                args, 0, stdout='{"login": "me", "name": "Me Person"}'
            )
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
def test_startup_reports_config_owner_and_jev(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    monkeypatch.setattr(app, "poll", lambda s, a, login: None)
    app.main(["--once", "--launcher", "tmux"])
    out = capsys.readouterr().out
    assert "config: no config file, built-in defaults" in out
    assert "prompts call you Me" in out  # first name from the GitHub profile
    assert "launch check: off (jev: no backend key" in out
    assert "tool gate: off (jev: no backend key" in out


@pytest.mark.usefixtures("machine")
def test_config_file_sets_owner_and_is_overridden_by_flags(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Any
) -> None:
    cfg = tmp_path / "c.yaml"
    cfg.write_text("owner_name: Matze\nmax_agents: 4\nagent: claude\n")
    seen: list[app.Args] = []
    monkeypatch.setattr(app, "poll", lambda s, a, login: seen.append(a))
    app.main(["--once", "--launcher", "tmux", "--config", str(cfg), "--agent", "codex"])
    assert app.OWNER == "Matze"
    [a] = seen
    assert a.max_agents == 4  # from the file
    assert a.agent == "codex"  # flag wins
    assert a.cfg.agent == "codex"


def test_invalid_config_stops_with_the_path(tmp_path: Any) -> None:
    cfg = tmp_path / "c.yaml"
    cfg.write_text("max_agents: 0\n")
    with pytest.raises(SystemExit) as e:
        app.parse_args(["--config", str(cfg)])
    assert "max_agents: Input should be greater than or equal to 1" in str(e.value)
    assert str(cfg) in str(e.value)
