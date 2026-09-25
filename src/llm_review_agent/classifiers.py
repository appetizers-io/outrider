"""Decision backends for the launch check and the tool gate.

A classifier is resolved once at startup into the commands it can run.
Unavailable ones (disabled, binary missing, Jev without a backend key) resolve
to None with a reason, and their roles are simply off.

Launch check protocol for `kind: command`: the request below is written to
stdin as JSON; the command prints {"launch": bool} or {"probability": 0..1},
optionally with "reason". Anything else, a timeout or a failure launches.

The tool gate is a Claude Code / Codex PreToolUse hook. It gets the session's
rules in $LLM_REVIEW_AGENT_GATE_TEXT and the full session policy as JSON in
$LLM_REVIEW_AGENT_POLICY_FILE (Jev reads $JEV_GATE_STATE, set to the same text).
"""

import json
import os
import shlex
import shutil
import subprocess
from collections.abc import Callable
from typing import Any

from pydantic import BaseModel, ConfigDict

from llm_review_agent import config

QUESTION = (
    "Should a coding agent working for {owner} act on this PR now: a concrete "
    "change request, a question that needs {owner}'s answer, a reply in their "
    "review thread that needs a response, or a failing CI check on their own PR?"
)

Runner = Callable[..., subprocess.CompletedProcess[str]]


class Resolved(BaseModel):
    model_config = ConfigDict(frozen=True)

    name: str
    kind: str  # jev | command
    launch_cmd: list[str] | None
    hook_cmd: list[str] | None
    timeout: int
    confidence_threshold: float | None = None  # jev only


def _split(cmd: str) -> list[str]:
    parts = shlex.split(cmd)
    return [os.path.expanduser(parts[0]), *parts[1:]]


def _available(cmd: list[str]) -> bool:
    return bool(shutil.which(cmd[0]))


def resolve(
    name: str, c: config.JevClassifier | config.CommandClassifier
) -> tuple[Resolved | None, str]:
    """The runnable classifier, or None with the reason it is off."""
    if c.enabled is False:
        return None, "disabled"
    if isinstance(c, config.JevClassifier):
        if c.command:
            base = _split(c.command)
        elif shutil.which("jev-use"):
            base = ["jev-use"]
        else:
            base = ["npx", "-y", "jev-use@0.8.0"]
        if not _available(base):
            return None, f"{base[0]} not found"
        if c.enabled == "auto" and not config.jev_backend_configured():
            return None, "no backend key (" + ", ".join(config.JEV_BACKEND_ENV) + ")"
        return (
            Resolved(
                name=name,
                kind="jev",
                launch_cmd=[*base, "judge"],
                hook_cmd=[*base, "hook", "gate"],
                timeout=c.timeout_seconds,
                confidence_threshold=c.confidence_threshold,
            ),
            shlex.join(base),
        )
    launch = _split(c.launch_command) if c.launch_command else None
    hook = _split(c.hook_command) if c.hook_command else None
    for cmd in (launch, hook):
        if cmd and not _available(cmd):
            return None, f"{cmd[0]} not found"
    resolved = Resolved(
        name=name,
        kind="command",
        launch_cmd=launch,
        hook_cmd=hook,
        timeout=c.timeout_seconds,
    )
    return resolved, "command"


def resolve_role(cfg: config.Config, name: str | None) -> tuple[Resolved | None, str]:
    if name is None:
        return None, "off"
    r, note = resolve(name, cfg.classifiers[name])
    return r, f"{name}: {note}"


def check_launch(
    r: Resolved,
    request: dict[str, Any],
    skip_below: float,
    run: Runner,
) -> tuple[bool, str]:
    """(launch, note). Anything but a confident "no" launches."""
    assert r.launch_cmd is not None
    if r.kind == "jev":
        payload: dict[str, Any] = {
            "state": request["state_text"],
            "questions": [
                {
                    "id": "act",
                    "type": "noul",
                    "question": request["question"],
                    "criteria": {
                        "true": "at least one item needs a code change or a "
                        f"response from {request['owner']}",
                        "false": "only bot summaries, approvals, LGTMs, thanks, "
                        f"acknowledgements, {request['owner']}'s own activity, "
                        "or nothing",
                    },
                }
            ],
        }
        if r.confidence_threshold is not None:
            payload["confidence_threshold"] = r.confidence_threshold
    else:
        payload = request
    try:
        # jev-use exits 3 when escalated; the verdict is still on stdout
        out = run(
            r.launch_cmd, input=json.dumps(payload), timeout=r.timeout, check=False
        ).stdout
        answer = json.loads(out)
    except (subprocess.CalledProcessError, OSError, ValueError) as e:
        return True, f"{r.name} unavailable, launching anyway: {e!r}"
    if not isinstance(answer, dict):
        return True, f"{r.name}: unexpected answer, launching anyway"
    if r.kind == "jev":
        return _jev_verdict(r.name, answer, skip_below)
    reason = f" ({answer['reason']})" if answer.get("reason") else ""
    if isinstance(answer.get("launch"), bool):
        return answer["launch"], f"{r.name}: launch={answer['launch']}{reason}"
    p = answer.get("probability")
    if isinstance(p, int | float) and not isinstance(p, bool):
        return p >= skip_below, f"{r.name}: p={p}{reason}"
    return True, f"{r.name}: unexpected answer, launching anyway"


def _jev_verdict(
    name: str, answer: dict[str, Any], skip_below: float
) -> tuple[bool, str]:
    try:
        v = answer["verdicts"][0]
    except KeyError, IndexError, TypeError:
        return True, f"{name}: unexpected answer, launching anyway"
    note = f"{name} p={v.get('answer')} conf={v.get('confidence')}"
    if v.get("escalate"):
        return True, f"{note} unsure ({v.get('reason')}), launching anyway"
    p = v.get("answer", 1)
    if not isinstance(p, int | float) or isinstance(p, bool):
        return True, f"{note} unexpected answer, launching anyway"
    return p >= skip_below, note


def hook_env(
    r: Resolved, gate_text: str, threshold: float | None, policy_file: str
) -> dict[str, str]:
    """Environment the tool-gate hook runs with."""
    env = {
        "LLM_REVIEW_AGENT_POLICY_FILE": policy_file,
        "LLM_REVIEW_AGENT_GATE_TEXT": gate_text,
    }
    if threshold is not None:
        env["LLM_REVIEW_AGENT_GATE_THRESHOLD"] = str(threshold)
    if r.kind == "jev":
        env["JEV_GATE_STATE"] = gate_text
        if threshold is not None:
            env["JEV_GATE_THRESHOLD"] = str(threshold)
    return env
