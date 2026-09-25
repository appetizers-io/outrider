"""Launch-check and tool-gate classifiers: Jev and any local command."""

import json
import shutil
import stat
import subprocess
from pathlib import Path
from typing import Any

import pytest

from llm_review_agent import app, classifiers, config

PR: dict[str, Any] = {"title": "T", "author": {"login": "bob"}}


def item(body: str, user: str = "alice") -> app.ActivityItem:
    return app.ActivityItem(
        kind="comment",
        id=1,
        state=None,
        updated_at="2026-01-01T00:00:00Z",
        submitted_at=None,
        user=user,
        path=None,
        at="2026-01-01T00:00:00Z",
        body=body,
        url="https://x/c1",
    )


def which(*present: str) -> Any:
    return lambda tool: f"/bin/{tool}" if tool in present else None


# --- resolving: on only when it can work ------------------------------------


def test_jev_auto_off_without_backend_key(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(shutil, "which", which("npx"))
    r, why = classifiers.resolve("jev", config.JevClassifier())
    assert r is None
    assert "no backend key" in why


def test_jev_prefers_installed_jev_use(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("TYPESAFE_API_KEY", "x")
    monkeypatch.setattr(shutil, "which", which("npx", "jev-use"))
    r, _ = classifiers.resolve("jev", config.JevClassifier())
    assert r is not None
    assert r.launch_cmd == ["jev-use", "judge"]
    assert r.hook_cmd == ["jev-use", "hook", "gate"]


def test_jev_falls_back_to_npx(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("OPENROUTER_API_KEY", "x")
    monkeypatch.setattr(shutil, "which", which("npx"))
    r, _ = classifiers.resolve("jev", config.JevClassifier())
    assert r is not None
    assert r.launch_cmd == ["npx", "-y", "jev-use@0.8.0", "judge"]


def test_jev_off_when_command_missing(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("TYPESAFE_API_KEY", "x")
    monkeypatch.setattr(shutil, "which", which())
    assert classifiers.resolve("jev", config.JevClassifier()) == (None, "npx not found")


def test_jev_explicitly_enabled_needs_no_key(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(shutil, "which", which("my-jev"))
    c = config.JevClassifier(enabled=True, command="my-jev --flag")
    r, _ = classifiers.resolve("jev", c)
    assert r is not None
    assert r.hook_cmd == ["my-jev", "--flag", "hook", "gate"]


def test_disabled(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("TYPESAFE_API_KEY", "x")
    c = config.JevClassifier(enabled=False)
    assert classifiers.resolve("jev", c) == (None, "disabled")


def test_command_classifier_needs_its_binaries(monkeypatch: pytest.MonkeyPatch) -> None:
    c = config.CommandClassifier(kind="command", hook_command="~/bin/cls hook")
    monkeypatch.setattr(shutil, "which", which())
    r, why = classifiers.resolve("local", c)
    assert r is None
    assert why.endswith("/bin/cls not found")
    monkeypatch.setattr(shutil, "which", lambda tool: tool)
    r, _ = classifiers.resolve("local", c)
    assert r is not None
    assert r.hook_cmd is not None
    assert r.hook_cmd[0].endswith("/bin/cls")  # ~ expanded
    assert r.launch_cmd is None


def test_no_jev_and_jev_cmd_flags() -> None:
    jev = app.parse_args(["--no-jev"]).cfg.classifiers["jev"]
    assert isinstance(jev, config.JevClassifier)
    assert jev.enabled is False
    jev = app.parse_args(["--jev-cmd", "x y"]).cfg.classifiers["jev"]
    assert isinstance(jev, config.JevClassifier)
    assert jev.command == "x y"


# --- launch check through Jev -----------------------------------------------

JEV = classifiers.Resolved(
    name="jev", kind="jev", launch_cmd=["jev-use", "judge"], hook_cmd=None, timeout=5
)


def fake_run(outputs: list[Any], seen: list[dict[str, Any]]) -> Any:
    def run(args: list[str], **kw: Any) -> subprocess.CompletedProcess[str]:
        assert kw["check"] is False  # jev-use exit 3 (escalated) must be parsed
        seen.append({"args": args, "payload": json.loads(kw["input"])})
        out = outputs.pop(0)
        stdout = out if isinstance(out, str) else json.dumps(out)
        return subprocess.CompletedProcess(args, 3, stdout=stdout)

    return run


def verdict(**kw: Any) -> dict[str, Any]:
    return {"verdicts": [{"id": "act", "escalate": False, **kw}]}


def request(new: list[app.ActivityItem] | None = None) -> dict[str, Any]:
    return app.launch_request("o/r", 1, PR, "t", new or [item("x")])


@pytest.mark.parametrize(
    ("answer", "launch"),
    [
        (verdict(answer=0.04, confidence=0.92), False),
        (verdict(answer=0.89, confidence=0.78), True),
        (verdict(answer=0.1, escalate=True, reason="unsure"), True),
        ({}, True),
        ({"verdicts": []}, True),
        (verdict(answer="yes"), True),
        ("not json", True),
    ],
)
def test_jev_launch_decisions(answer: Any, launch: bool) -> None:
    seen: list[dict[str, Any]] = []
    go, _ = classifiers.check_launch(JEV, request(), 0.5, fake_run([answer], seen))
    assert go is launch
    assert seen[0]["payload"]["questions"][0]["type"] == "noul"


def test_jev_payload_carries_confidence_threshold() -> None:
    seen: list[dict[str, Any]] = []
    r = JEV.model_copy(update={"confidence_threshold": 0.7})
    classifiers.check_launch(r, request(), 0.5, fake_run([verdict(answer=1)], seen))
    assert seen[0]["payload"]["confidence_threshold"] == 0.7


def test_unreachable_launches() -> None:
    r = JEV.model_copy(update={"launch_cmd": ["definitely-not-installed-jev"]})
    go, note = classifiers.check_launch(r, request(), 0.5, app.run)
    assert go
    assert "unavailable" in note


def test_request_keeps_newest_when_trimming() -> None:
    new = [item(f"old {i} " + "x" * 1400) for i in range(60)] + [item("NEWEST")]
    state = request(new)["state_text"]
    assert "NEWEST" in state
    assert "old 0 " not in state
    assert len(state) < 45000


def test_request_mentions_ownership_and_failing_checks() -> None:
    own = {
        **PR,
        "author": {"login": "ME"},
        "statusCheckRollup": [{"name": "unit", "conclusion": "FAILURE"}],
    }
    req = app.launch_request("o/r", 1, own, "t", [])
    assert "Matthias's own PR" in req["state_text"]
    assert req["failing_checks"] == ["unit"]
    assert req["own"] is True


# --- launch check through a local command -----------------------------------


@pytest.fixture
def local(tmp_path: Path) -> Any:
    """A real local classifier script: answers from $ANSWER, logs its input."""

    def make(answer: str) -> classifiers.Resolved:
        script = tmp_path / "cls"
        script.write_text(
            f"#!/bin/sh\ncat > {tmp_path}/request.json\nprintf '%s' '{answer}'\n"
        )
        script.chmod(script.stat().st_mode | stat.S_IEXEC)
        return classifiers.Resolved(
            name="local",
            kind="command",
            launch_cmd=[str(script), "launch"],
            hook_cmd=None,
            timeout=5,
        )

    return make


@pytest.mark.parametrize(
    ("answer", "launch"),
    [
        ('{"launch": false, "reason": "only a bot"}', False),
        ('{"launch": true}', True),
        ('{"probability": 0.1}', False),
        ('{"probability": 0.9}', True),
        ('{"probability": true}', True),  # not a number: fail open
        ("garbage", True),
    ],
)
def test_local_classifier_protocol(
    local: Any, tmp_path: Path, answer: str, launch: bool
) -> None:
    go, note = classifiers.check_launch(local(answer), request(), 0.5, app.run)
    assert go is launch
    sent = json.loads((tmp_path / "request.json").read_text())
    assert sent["version"] == 1
    assert sent["repo"] == "o/r"
    assert sent["activity"][0]["body"] == "x"
    assert "question" in sent
    if "reason" in answer:
        assert "only a bot" in note


# --- tool gate environment --------------------------------------------------


def test_hook_env_generic_and_jev() -> None:
    local = classifiers.Resolved(
        name="l", kind="command", launch_cmd=None, hook_cmd=["h"], timeout=5
    )
    env = classifiers.hook_env(local, "rules", 0.7, "/p.json")
    assert env == {
        "LLM_REVIEW_AGENT_POLICY_FILE": "/p.json",
        "LLM_REVIEW_AGENT_GATE_TEXT": "rules",
        "LLM_REVIEW_AGENT_GATE_THRESHOLD": "0.7",
    }
    env = classifiers.hook_env(JEV, "rules", None, "/p.json")
    assert env["JEV_GATE_STATE"] == "rules"
    assert "JEV_GATE_THRESHOLD" not in env
