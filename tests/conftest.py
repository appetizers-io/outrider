from collections.abc import Iterator
from pathlib import Path

import pytest

from llm_review_agent import app


@pytest.fixture(autouse=True)
def isolated(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Iterator[None]:
    """Keep state, locks and module globals away from the real machine."""
    monkeypatch.setattr(app, "ROOT", tmp_path / "root")
    monkeypatch.setattr(app, "STATE", tmp_path / "state" / "state.json")
    # never touch the developer's real pre-rename directories
    monkeypatch.setattr(app, "LEGACY_ROOT", tmp_path / "legacy-cache")
    monkeypatch.setattr(app, "LEGACY_STATE_DIR", tmp_path / "legacy-state")
    monkeypatch.setattr(app, "LEGACY_CONFIG_DIR", tmp_path / "legacy-config")
    monkeypatch.setattr(app, "legacy_watcher_pids", list)
    monkeypatch.setattr(app, "LOGIN", "me")
    monkeypatch.setattr(app, "OWNER", "Matthias")
    # never pick up the developer's real config file or Jev key
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path / "xdg"))
    monkeypatch.delenv("LLM_REVIEW_AGENT_CONFIG", raising=False)
    for key in (
        "TYPESAFE_API_KEY",
        "OPENROUTER_API_KEY",
        "AI_GATEWAY_API_KEY",
        "JEV_BACKEND",
    ):
        monkeypatch.delenv(key, raising=False)
    monkeypatch.setattr(app, "LAUNCH_CHECK", None)
    monkeypatch.setattr(app, "TOOL_GATE", None)
    monkeypatch.setattr(app, "LAUNCHER", "tmux")
    monkeypatch.setattr(app, "LOCAL", {})
    yield
