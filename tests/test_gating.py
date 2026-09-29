"""Supervised sessions: deny rules + Jev tool gate; autonomous: none."""

import json
import shutil
import subprocess
from pathlib import Path
from typing import Any

import pytest

from llm_review_agent import app, classifiers, config

JEV_GATE = classifiers.Resolved(
    name="jev",
    kind="jev",
    launch_cmd=None,
    hook_cmd=["jev-use", "hook", "gate"],
    timeout=5,
)
LOCAL_GATE = classifiers.Resolved(
    name="local", kind="command", launch_cmd=None, hook_cmd=["cls", "hook"], timeout=5
)


@pytest.fixture
def launched(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> Any:
    monkeypatch.setattr(app, "worktree", lambda repo, n: tmp_path)
    monkeypatch.setattr(shutil, "which", lambda tool: f"/bin/{tool}")
    monkeypatch.setattr(
        app, "run", lambda *a, **k: subprocess.CompletedProcess([], 0, stdout="")
    )

    def launch(author: str, *flags: str, cfg: str = "") -> dict[str, Any]:
        p = tmp_path / "c.yaml"
        p.write_text(cfg)
        a = app.parse_args(["--agent", "claude", "--config", str(p), *flags])
        assert app.launch("o/r", 1, {"title": "T", "author": {"login": author}}, "t", a)
        session = app.ROOT / "sessions" / "o__r" / "pr-1"
        sf = session / "claude-settings.json"
        runner = (session / "run-agent.command").read_text()
        return {
            "policy": json.loads((session / "policy.json").read_text()),
            "prompt": (session / "prompt.txt").read_text(),
            "runner": runner,
            "settings": json.loads(sf.read_text()) if sf.exists() else None,
            "uses_settings": "--settings" in runner,
        }

    return launch


def test_review_only_session_denies_edits_and_pushes(
    launched: Any, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(app, "TOOL_GATE", JEV_GATE)
    got = launched("bob")
    deny = got["settings"]["permissions"]["deny"]
    assert {"Edit", "Write", "Bash(git commit:*)", "Bash(git push:*)"} <= set(deny)
    hook = got["settings"]["hooks"]["PreToolUse"][0]
    assert hook["matcher"] == "Bash|Write|Edit|NotebookEdit"
    assert hook["hooks"][0]["command"] == "jev-use hook gate"
    assert "REVIEW ONLY" in got["settings"]["env"]["JEV_GATE_STATE"]
    assert got["uses_settings"]
    assert "LLM_REVIEW_AGENT_PUSH=review-only" in got["runner"]


def test_own_pr_session_allows_work_but_not_force(
    launched: Any, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(app, "TOOL_GATE", JEV_GATE)
    got = launched("me")
    deny = got["settings"]["permissions"]["deny"]
    assert "Edit" not in deny
    assert "Bash(git push --force:*)" in deny
    assert "Bash(git push:*)" not in deny
    assert "Bash(osascript:*)" in deny  # can't click its own approval dialog
    assert "own PR" in got["settings"]["env"]["JEV_GATE_STATE"]
    assert "asks the owner" in got["settings"]["env"]["JEV_GATE_STATE"]
    assert "LLM_REVIEW_AGENT_PUSH=ask" in got["runner"]
    assert "GIT_CONFIG_COUNT=5" in got["runner"]
    assert got["policy"]["push"] == "ask"
    assert "opens a dialog for" in got["prompt"]


def test_push_never_keeps_commits_local(
    launched: Any, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(app, "TOOL_GATE", JEV_GATE)
    got = launched("me", cfg="push: never\n")
    assert "Bash(git push:*)" in got["settings"]["permissions"]["deny"]
    assert "LLM_REVIEW_AGENT_PUSH=never" in got["runner"]
    assert "do not push" in got["prompt"]
    assert not got["policy"]["push_allowed"]


def test_without_jev_deny_rules_still_apply(launched: Any) -> None:
    got = launched("bob")  # no tool gate classifier available
    assert "hooks" not in got["settings"]
    assert "Bash(git push:*)" in got["settings"]["permissions"]["deny"]


def test_autonomous_mode_has_no_gating_and_may_push(launched: Any) -> None:
    got = launched("bob", cfg="mode: autonomous\n")
    assert got["settings"] is None
    assert not got["uses_settings"]
    assert "LLM_REVIEW_AGENT_PUSH=allow" in got["runner"]
    assert "GIT_CONFIG_COUNT" not in got["runner"]


def test_autonomous_mode_can_still_ask_before_pushing(launched: Any) -> None:
    got = launched("me", cfg="mode: autonomous\npush: ask\n")
    assert "LLM_REVIEW_AGENT_PUSH=ask" in got["runner"]


def test_autonomous_mode_respects_explicit_review_only(launched: Any) -> None:
    got = launched("bob", cfg="mode: autonomous\nothers_prs: {allow_push: false}\n")
    assert "LLM_REVIEW_AGENT_PUSH=review-only" in got["runner"]
    assert got["settings"] is None


@pytest.mark.parametrize(
    ("text", "allowed"),
    [
        ("", False),
        ("mode: autonomous\n", True),
        ("others_prs: {allow_push: true}\n", True),
        ("mode: autonomous\nothers_prs: {allow_push: false}\n", False),
    ],
)
def test_allow_push_follows_mode(text: str, allowed: bool, tmp_path: Path) -> None:
    p = tmp_path / "c.yaml"
    p.write_text(text)
    assert config.allow_push_to_others(config.load(p)) is allowed


def test_policy_names_the_pr_and_rules() -> None:
    text = app.session_rules("o/r", 7, "bob", own=False, push="review-only")
    assert "o/r#7 by bob" in text
    assert "deny every file edit, git commit, git push" in text


def test_custom_rules_and_threshold_reach_the_gate(
    launched: Any, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(app, "TOOL_GATE", JEV_GATE)
    got = launched(
        "me",
        cfg="tool_gate:\n"
        "  threshold: 0.8\n"
        "  matcher: Bash\n"
        '  rules: ["never modify generated/"]\n'
        "  include_prompt_extra: true\n"
        "prompts: {extra: 'no make release'}\n",
    )
    env = got["settings"]["env"]
    assert "(1) never modify generated/ (2) no make release" in env["JEV_GATE_STATE"]
    assert env["JEV_GATE_THRESHOLD"] == "0.8"
    assert got["settings"]["hooks"]["PreToolUse"][0]["matcher"] == "Bash"
    assert "export JEV_GATE_THRESHOLD=0.8" in got["runner"]  # for codex too


def test_local_classifier_as_tool_gate(
    launched: Any, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(app, "TOOL_GATE", LOCAL_GATE)
    got = launched("bob")
    hook = got["settings"]["hooks"]["PreToolUse"][0]["hooks"][0]
    assert hook["command"] == "cls hook"
    env = got["settings"]["env"]
    assert "REVIEW ONLY" in env["LLM_REVIEW_AGENT_GATE_TEXT"]
    assert "JEV_GATE_STATE" not in env
    assert env["LLM_REVIEW_AGENT_POLICY_FILE"].endswith("policy.json")


def test_policy_file_describes_the_session(
    launched: Any, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(app, "TOOL_GATE", JEV_GATE)
    got = launched("bob")
    policy = got["policy"]
    assert policy["review_only"] is True
    assert policy["own_pr"] is False
    assert policy["mode"] == "supervised"
    assert policy["tool_gate"]["classifier"] == "jev"
    assert "Bash(git push:*)" in policy["deny_rules"]
    assert "policy.json" in got["prompt"]
    assert "LLM_REVIEW_AGENT_POLICY_FILE=" in got["runner"]


def test_github_writes_ask_by_default_in_supervised_mode(launched: Any) -> None:
    got = launched("bob")
    assert "export LLM_REVIEW_AGENT_GH_WRITES=ask" in got["runner"]
    assert "export LLM_REVIEW_AGENT_REPO=o/r" in got["runner"]
    assert "export LLM_REVIEW_AGENT_PR=1" in got["runner"]
    assert got["policy"]["github_writes"] == "ask"
    assert "post to GitHub only when" in got["prompt"]
    assert "opens a dialog for" in got["prompt"]
    assert "do NOT post comments" not in got["prompt"]
    assert "do NOT merge/close the PR" in got["prompt"]


def test_github_writes_never_keeps_github_read_only(launched: Any) -> None:
    got = launched("me", cfg="github_writes: never\n")
    assert "export LLM_REVIEW_AGENT_GH_WRITES=never" in got["runner"]
    assert "gh is read-only" in got["runner"]
    assert "do NOT post comments" in got["prompt"]


def test_github_writes_follows_mode(launched: Any) -> None:
    got = launched("me", cfg="mode: autonomous\n")
    assert got["policy"]["github_writes"] == "allow"
    assert "opens a dialog for Matthias showing" not in got["prompt"]


def test_gate_text_allows_posts_only_through_gh() -> None:
    text = app.session_rules("o/r", 7, "bob", own=False, push="review-only",
                             gh_writes="ask")  # fmt: skip
    assert "with plain `gh` is fine; the gh guard asks the owner" in text
    assert "except the posts allowed below" in text
    assert "with plain `gh`" not in app.session_rules(
        "o/r", 7, "bob", own=False, push="review-only"
    )


def test_github_writes_flag_overrides_config(launched: Any) -> None:
    got = launched("bob", "--github-writes", "allow", cfg="github_writes: never\n")
    assert "export LLM_REVIEW_AGENT_GH_WRITES=allow" in got["runner"]
    assert got["policy"]["github_writes"] == "allow"
