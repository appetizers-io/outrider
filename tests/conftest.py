from collections.abc import Iterator
from pathlib import Path

import pytest

from gh_review_agent import app


@pytest.fixture(autouse=True)
def isolated(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Iterator[None]:
    """Keep state, locks and module globals away from the real machine."""
    monkeypatch.setattr(app, "ROOT", tmp_path / "root")
    monkeypatch.setattr(app, "STATE", tmp_path / "state.json")
    monkeypatch.setattr(app, "LOGIN", "me")
    monkeypatch.setattr(app, "OWNER", "Matthias")
    # never pick up the developer's real config file or Jev key
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path / "xdg"))
    monkeypatch.delenv("GH_REVIEW_AGENT_CONFIG", raising=False)
    for key in (
        "TYPESAFE_API_KEY",
        "OPENROUTER_API_KEY",
        "AI_GATEWAY_API_KEY",
        "JEV_BACKEND",
    ):
        monkeypatch.delenv(key, raising=False)
    monkeypatch.setattr(app, "JEV", None)
    monkeypatch.setattr(app, "LAUNCHER", "tmux")
    monkeypatch.setattr(app, "LOCAL", {})
    yield
