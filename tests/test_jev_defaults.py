"""Smart Jev defaults: on when it can work, off with a reason otherwise."""

import shutil

import pytest

from gh_review_agent import app, config


def which(*present: str) -> object:
    return lambda tool: f"/bin/{tool}" if tool in present else None


def test_auto_off_without_backend_key(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(shutil, "which", which("npx"))
    cmd, why = app.resolve_jev(config.Jev())
    assert cmd is None
    assert "no backend key" in why


def test_auto_on_with_key_prefers_installed_jev_use(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setenv("TYPESAFE_API_KEY", "x")
    monkeypatch.setattr(shutil, "which", which("npx", "jev-use"))
    assert app.resolve_jev(config.Jev())[0] == ["jev-use"]


def test_auto_falls_back_to_npx(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("OPENROUTER_API_KEY", "x")
    monkeypatch.setattr(shutil, "which", which("npx"))
    assert app.resolve_jev(config.Jev())[0] == ["npx", "-y", "jev-use@0.8.0"]


def test_off_when_command_missing(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("TYPESAFE_API_KEY", "x")
    monkeypatch.setattr(shutil, "which", which())
    cmd, why = app.resolve_jev(config.Jev())
    assert cmd is None
    assert why == "npx not found"


def test_explicit_true_needs_no_key(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(shutil, "which", which("my-jev"))
    cmd, _ = app.resolve_jev(config.Jev(enabled=True, command="my-jev --flag"))
    assert cmd == ["my-jev", "--flag"]


def test_disabled(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("TYPESAFE_API_KEY", "x")
    assert app.resolve_jev(config.Jev(enabled=False)) == (None, "disabled")


def test_no_jev_flag_overrides_config(tmp_path: object) -> None:
    assert app.parse_args(["--no-jev"]).cfg.jev.enabled is False
    assert app.parse_args(["--jev-cmd", "x y"]).cfg.jev.command == "x y"
