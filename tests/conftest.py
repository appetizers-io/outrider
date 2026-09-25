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
    monkeypatch.setattr(app, "JEV", None)
    monkeypatch.setattr(app, "LAUNCHER", "tmux")
    monkeypatch.setattr(app, "LOCAL", {})
    yield
