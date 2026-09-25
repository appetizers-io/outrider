"""YAML configuration.

The pydantic models below are the single source of truth: types, defaults,
constraints and descriptions are declared once, the JSON Schema is generated
from them (`gh-review-agent config schema`), and loading validates with them.
"""

import json
import os
from pathlib import Path
from typing import Annotated, Any, Literal

import yaml
from pydantic import BaseModel, ConfigDict, Field, ValidationError, field_validator

ENV_VAR = "GH_REVIEW_AGENT_CONFIG"
SCHEMA_ID = "https://github.com/appetizers-io/llm-review-agent/config.schema.json"
JEV_BACKEND_ENV = (
    "TYPESAFE_API_KEY",
    "OPENROUTER_API_KEY",
    "AI_GATEWAY_API_KEY",
    "JEV_BACKEND",
)

Gate = Annotated[
    Literal["jev", "none"],
    Field(description="jev: ask Jev first (when Jev is enabled). none: always launch."),
]
Reaction = Literal["+1", "-1", "laugh", "confused", "heart", "hooray", "rocket", "eyes"]
Where = Literal["description", "comment", "review", "review_comment"]
Glob = Annotated[str, Field(min_length=1)]


class Model(BaseModel):
    # strict: YAML's `yes`/`1` must not silently become booleans or strings
    model_config = ConfigDict(extra="forbid", frozen=True, strict=True)


class Repos(Model):
    include: list[Glob] = Field(
        default=[],
        description="owner/repo globs or GitHub URLs. Empty: the checkout you run "
        "in, else every repo.",
    )
    exclude: list[Glob] = Field(
        default=[], description="owner/repo globs or GitHub URLs that never trigger."
    )


class OwnPrs(Model):
    """Notifications on PRs you authored."""

    enabled: bool = True
    gate: Gate = "jev"


class OnChange(Model):
    """Relaunch when reviews or comments on an opted-in PR change."""

    gate: Gate = "jev"
    ignore_own_activity: bool = Field(
        default=True,
        description="Changes made only by you (or by ignore_authors) never relaunch.",
    )


class OptIn(Model):
    """Your reaction on someone else's PR opts it in; sessions cover the whole PR."""

    enabled: bool = True
    reaction: Reaction = Field(
        default="eyes",
        description="GitHub reaction that opts a PR in. Removing it stops the watch.",
    )
    where: list[Where] = Field(
        default=["description", "comment", "review", "review_comment"],
        min_length=1,
        description="Where the reaction counts.",
        json_schema_extra={"uniqueItems": True},
    )
    on_change: OnChange = OnChange()

    @field_validator("where")
    @classmethod
    def _unique(cls, v: list[Where]) -> list[Where]:
        if len(set(v)) != len(v):
            raise ValueError("entries must be unique")
        return v


class ReviewReplies(Model):
    """Someone replies in a review thread you took part in."""

    enabled: bool = True
    gate: Gate = "jev"
    scope: Literal["thread", "pr"] = Field(
        default="thread",
        description="thread: the session handles only the replied threads. "
        "pr: the whole PR.",
    )
    fresh_within_hours: int | None = Field(
        default=None,
        ge=1,
        description="Only replies this recent trigger. null: lookback_hours.",
    )


class Triggers(Model):
    own_prs: OwnPrs = OwnPrs()
    opt_in: OptIn = OptIn()
    review_replies: ReviewReplies = ReviewReplies()


class Jev(Model):
    """Pre-launch check: Jev judges whether new activity is actionable."""

    enabled: Literal["auto", True, False] = Field(
        default="auto",
        description="auto: on when the command is found and a Jev backend is "
        "configured (" + ", ".join(JEV_BACKEND_ENV) + ").",
    )
    command: Annotated[str, Field(min_length=1)] | None = Field(
        default=None, description="null: jev-use on PATH, else npx -y jev-use@0.8.0."
    )
    skip_below: float = Field(
        default=0.5,
        ge=0,
        le=1,
        description="Skip the launch when Jev is confident the probability that "
        "something is actionable is below this.",
    )
    confidence_threshold: float | None = Field(
        default=None,
        ge=0,
        le=1,
        description="Below this confidence Jev escalates, which launches. "
        "null: jev-use's own defaults (0.5 reported, 0.4 estimated).",
    )
    timeout_seconds: int = Field(
        default=60, ge=1, description="A slower answer launches anyway."
    )


class Prompts(Model):
    extra: str = Field(
        default="",
        description="Appended to every agent prompt, e.g. house rules for tests "
        "or commits.",
    )


class Config(Model):
    """gh-review-agent configuration. Every key is optional; command-line flags
    override this file."""

    model_config = ConfigDict(title="gh-review-agent configuration")

    owner_name: Annotated[str, Field(min_length=1)] | None = Field(
        default=None,
        description="How prompts refer to you. null: first name from your GitHub "
        "profile, else your login.",
    )
    agent: Literal["codex", "claude"] = Field(
        default="codex", description="Coding agent CLI to launch."
    )
    launcher: Literal["auto", "terminal", "tmux"] = Field(
        default="auto",
        description="auto: Terminal on a macOS desktop session, else tmux.",
    )
    interval_seconds: int = Field(
        default=60,
        ge=10,
        description="Pause between polls. Doubles on consecutive failures, up to "
        "15 minutes.",
    )
    lookback_hours: int = Field(
        default=168,
        ge=1,
        description="Notification window, and how long an unseen candidate PR is kept.",
    )
    max_agents: int = Field(
        default=1, ge=1, description="Agent sessions allowed to run at once."
    )
    candidate_limit: int = Field(
        default=50,
        ge=1,
        description="Most recently seen non-owned PRs checked for the opt-in "
        "reaction per poll.",
    )
    stale_lock_hours: float = Field(
        default=12, gt=0, description="A session lock older than this is dead."
    )
    repos: Repos = Repos()
    ignore_authors: list[Glob] = Field(
        default=[],
        description="Login globs whose activity alone never triggers a session, "
        'e.g. "netlify[bot]" or "*[bot]".',
    )
    triggers: Triggers = Triggers()
    jev: Jev = Jev()
    prompts: Prompts = Prompts()


class ConfigError(Exception):
    """The config file is unreadable or invalid."""


def json_schema() -> dict[str, Any]:
    s = Config.model_json_schema()
    return {
        "$schema": "https://json-schema.org/draft/2020-12/schema",
        "$id": SCHEMA_ID,
        **s,
    }


def schema_text() -> str:
    return json.dumps(json_schema(), indent=2) + "\n"


def default_path() -> Path:
    base = os.environ.get("XDG_CONFIG_HOME") or Path.home() / ".config"
    return Path(base) / "gh-review-agent" / "config.yaml"


def find(explicit: str | None) -> Path | None:
    """--config, then $GH_REVIEW_AGENT_CONFIG, then the XDG default path."""
    if explicit:
        return Path(explicit)
    if os.environ.get(ENV_VAR):
        return Path(os.environ[ENV_VAR])
    p = default_path()
    return p if p.exists() else None


def _describe(e: ValidationError) -> str:
    lines = []
    for err in e.errors():
        loc = ".".join(str(p) for p in err["loc"]) or "(top level)"
        lines.append(f"  {loc}: {err['msg']}")
    return "\n".join(lines)


def parse(data: Any, source: str = "config") -> Config:
    try:
        return Config.model_validate({} if data is None else data)
    except ValidationError as e:
        raise ConfigError(f"{source} is invalid:\n{_describe(e)}") from e


def load(path: Path | None) -> Config:
    """The config at path, or the defaults when there is none."""
    if path is None:
        return Config()
    try:
        data = yaml.safe_load(path.read_text())
    except OSError as e:
        raise ConfigError(f"cannot read config {path}: {e.strerror}") from e
    except yaml.YAMLError as e:
        raise ConfigError(f"config {path} is not valid YAML:\n{e}") from e
    return parse(data, str(path))


def jev_backend_configured() -> bool:
    return any(os.environ.get(k) for k in JEV_BACKEND_ENV)
