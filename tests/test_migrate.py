"""Moving gh-review-agent's directories to llm-review-agent."""

import json
import subprocess
from pathlib import Path

import pytest

from llm_review_agent import app, config


def git(*args: str) -> str:
    return subprocess.run(
        ["git", *args], capture_output=True, text=True, check=True
    ).stdout


def new_repo(path: Path) -> Path:
    git("init", "-q", str(path))
    git(
        "-C",
        str(path),
        "-c",
        "user.name=t",
        "-c",
        "user.email=t@t",
        "commit",
        "-q",
        "--allow-empty",
        "--no-gpg-sign",
        "-m",
        "init",
    )
    return path


def test_state_config_and_cache_move_and_worktrees_relink(tmp_path: Path) -> None:
    old = app.LEGACY_ROOT
    # a clone cached inside the old cache, and a checkout outside of it
    clone = new_repo(old / "repos" / "o__r")
    local = new_repo(tmp_path / "checkout")
    wt_clone = old / "worktrees" / "o__r" / "pr-1"
    wt_local = old / "worktrees" / "x__y" / "pr-2"
    wt_clone.parent.mkdir(parents=True)
    wt_local.parent.mkdir(parents=True)
    git("-C", str(clone), "worktree", "add", "-q", "-b", "review/pr-1", str(wt_clone))
    git("-C", str(local), "worktree", "add", "-q", "-b", "review/pr-2", str(wt_local))
    app.LEGACY_STATE_DIR.mkdir()
    (app.LEGACY_STATE_DIR / "state.json").write_text(json.dumps({"seen": {"1": "t"}}))
    app.LEGACY_CONFIG_DIR.mkdir()
    (app.LEGACY_CONFIG_DIR / "config.yaml").write_text("agent: claude\n")

    app.migrate_legacy()

    assert not old.exists()
    assert app.load_state().seen == {"1": "t"}
    assert config.load(config.find(None)).agent == "claude"
    new_clone = app.ROOT / "repos" / "o__r"
    for main, wt in (
        (new_clone, app.ROOT / "worktrees" / "o__r" / "pr-1"),
        (local, app.ROOT / "worktrees" / "x__y" / "pr-2"),
    ):
        listed = git("-C", str(main), "worktree", "list", "--porcelain")
        assert f"worktree {wt.resolve()}" in listed or f"worktree {wt}" in listed
        assert "prunable" not in listed
        assert git("-C", str(wt), "status", "--short") == ""  # usable


def test_running_old_sessions_block_the_move(tmp_path: Path) -> None:
    (app.LEGACY_ROOT / "locks").mkdir(parents=True)
    (app.LEGACY_ROOT / "locks" / "o__r__1.lock").write_text("{}")
    with pytest.raises(SystemExit, match="close the running gh-review-agent"):
        app.migrate_legacy()
    assert app.LEGACY_ROOT.exists()


def test_nothing_to_migrate_is_a_no_op() -> None:
    app.migrate_legacy()
    assert not app.ROOT.exists()


def test_existing_new_dirs_are_never_overwritten() -> None:
    app.LEGACY_STATE_DIR.mkdir()
    (app.LEGACY_STATE_DIR / "state.json").write_text('{"seen": {"old": "x"}}')
    app.STATE.parent.mkdir(parents=True, exist_ok=True)
    app.STATE.write_text('{"seen": {"new": "y"}}')
    app.migrate_legacy()
    assert app.load_state().seen == {"new": "y"}


def test_running_old_watcher_blocks_the_move(monkeypatch: pytest.MonkeyPatch) -> None:
    app.LEGACY_STATE_DIR.mkdir()
    monkeypatch.setattr(app, "legacy_watcher_pids", lambda: ["4242"])
    with pytest.raises(SystemExit, match="stop the running gh-review-agent watcher"):
        app.migrate_legacy()
    assert app.LEGACY_STATE_DIR.exists()
