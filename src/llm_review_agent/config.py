"""YAML configuration.

The pydantic models below are the single source of truth: types, defaults,
constraints and descriptions are declared once, the JSON Schema is generated
from them (`llm-review-agent config schema`), and loading validates with them.
"""

import json
import os
import textwrap
from pathlib import Path
from types import UnionType
from typing import Annotated, Any, Literal, Union, get_args, get_origin

import yaml
from pydantic import (
    BaseModel,
    ConfigDict,
    Discriminator,
    Field,
    Tag,
    ValidationError,
    field_validator,
    model_validator,
)
from pydantic.fields import FieldInfo

ENV_VAR = "LLM_REVIEW_AGENT_CONFIG"
SCHEMA_ID = "https://github.com/appetizers-io/llm-review-agent/config.schema.json"
JEV_BACKEND_ENV = (
    "TYPESAFE_API_KEY",
    "OPENROUTER_API_KEY",
    "AI_GATEWAY_API_KEY",
    "JEV_BACKEND",
)

Check = Annotated[
    bool,
    Field(
        description="Run the launch_check classifier before starting an agent "
        "(only when one is configured and available)."
    ),
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
    check: Check = True


class OnChange(Model):
    """Relaunch when reviews or comments on an opted-in PR change."""

    check: Check = True
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
    check: Check = True
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


class Mentions(Model):
    """Someone @mentions your login on a PR that is neither yours nor opted in
    (those already relaunch for any new activity)."""

    enabled: bool = True
    check: Check = True
    scope: Literal["comment", "pr"] = Field(
        default="comment",
        description="comment: the session handles only the mentioning comments. "
        "pr: the whole PR.",
    )
    fresh_within_hours: int | None = Field(
        default=None,
        ge=1,
        description="Only mentions this recent trigger. null: lookback_hours.",
    )


class Triggers(Model):
    own_prs: OwnPrs = OwnPrs()
    opt_in: OptIn = OptIn()
    review_replies: ReviewReplies = ReviewReplies()
    mentions: Mentions = Mentions()


Cmd = Annotated[str, Field(min_length=1)]


class JevClassifier(Model):
    """Jev via jev-use: `<command> judge` for launch checks, `<command> hook
    gate` as the tool gate."""

    kind: Literal["jev"] = "jev"
    enabled: Literal["auto", True, False] = Field(
        default="auto",
        description="auto: on when the command is found and a Jev backend is "
        "configured (" + ", ".join(JEV_BACKEND_ENV) + ").",
    )
    command: Cmd | None = Field(
        default=None, description="null: jev-use on PATH, else npx -y jev-use@0.8.0."
    )
    confidence_threshold: float | None = Field(
        default=None,
        ge=0,
        le=1,
        description="Launch check: below this confidence Jev escalates, which "
        "launches. null: jev-use's defaults (0.5 reported, 0.4 estimated).",
    )
    timeout_seconds: int = Field(
        default=60, ge=1, description="A slower launch check launches anyway."
    )


class CommandClassifier(Model):
    """Any local classifier.

    launch_command gets a JSON request on stdin and prints
    {"launch": bool} or {"probability": 0..1}, optionally with "reason".
    hook_command is a Claude Code / Codex PreToolUse hook; the session policy
    is in $LLM_REVIEW_AGENT_POLICY_FILE and $LLM_REVIEW_AGENT_GATE_TEXT."""

    kind: Literal["command"]
    enabled: bool = True
    launch_command: Cmd | None = Field(
        default=None, description="Command for launch checks; null: can't do them."
    )
    hook_command: Cmd | None = Field(
        default=None, description="PreToolUse hook command; null: can't gate tools."
    )
    timeout_seconds: int = Field(
        default=30, ge=1, description="A slower launch check launches anyway."
    )


def _classifier_kind(v: Any) -> str:
    kind = v.get("kind", "jev") if isinstance(v, dict) else getattr(v, "kind", "jev")
    return str(kind)


Classifier = Annotated[
    Annotated[JevClassifier, Tag("jev")] | Annotated[CommandClassifier, Tag("command")],
    Discriminator(
        _classifier_kind,
        custom_error_type="classifier_kind",
        custom_error_message="kind must be 'jev' or 'command'",
    ),
]


class LaunchCheck(Model):
    """Ask a classifier whether new activity is worth an agent session."""

    classifier: Cmd | None = Field(
        default="jev", description="Name from classifiers; null: always launch."
    )
    skip_below: float = Field(
        default=0.5,
        ge=0,
        le=1,
        description="Skip when the classifier is confident the probability that "
        "something is actionable is below this.",
    )


class ToolGate(Model):
    """Check every tool call in supervised agent sessions (PreToolUse hook)."""

    classifier: Cmd | None = Field(
        default="jev", description="Name from classifiers; null: no tool gate."
    )
    matcher: Cmd = Field(
        default="Bash|Write|Edit|NotebookEdit", description="Tools the gate sees."
    )
    threshold: float | None = Field(
        default=None,
        ge=0,
        le=1,
        description="Confidence below which the gate asks instead of deciding. "
        "null: the classifier's own default.",
    )
    rules: list[Cmd] = Field(
        default=[],
        description='Your own rules for every tool call, e.g. "never modify '
        'generated/".',
    )
    include_prompt_extra: bool = Field(
        default=False, description="Also enforce prompts.extra through the gate."
    )


class OthersPrs(Model):
    """Sessions on PRs someone else authored."""

    allow_push: bool | None = Field(
        default=None,
        description="false: review only. The agent must not edit, commit or push, "
        "and `git push` is blocked in the session. true: it may push fixes to the "
        "PR branch (fast-forward only). null: false in supervised mode, true in "
        "autonomous mode.",
    )


class Prompts(Model):
    extra: str = Field(
        default="",
        description="Appended to every agent prompt, e.g. house rules for tests "
        "or commits.",
    )


class Config(Model):
    """llm-review-agent configuration. Every key is optional; command-line flags
    override this file."""

    model_config = ConfigDict(title="llm-review-agent configuration")

    owner_name: Annotated[str, Field(min_length=1)] | None = Field(
        default=None,
        description="How prompts refer to you. null: first name from your GitHub "
        "profile, else your login.",
    )
    mode: Literal["supervised", "autonomous"] = Field(
        default="supervised",
        description="supervised: Claude sessions get deny rules for risky tools "
        "and, when its classifier is available, the tool_gate on every call; other "
        "people's PRs are review only. autonomous: no tool gating, and pushing to "
        "other people's PRs follows others_prs.allow_push (default: allowed).",
    )
    push: Literal["ask", "never", "allow"] | None = Field(
        default=None,
        description="Pushes in sessions that may push (your own PRs, others' PRs "
        "with others_prs.allow_push). ask: every `git push` opens a dialog and runs "
        "only after you click Push (macOS; elsewhere it is refused). never: "
        "commits stay local. allow: no question. null: ask in supervised mode, "
        "allow in autonomous mode.",
    )
    github_writes: Literal["ask", "never", "allow"] | None = Field(
        default=None,
        description="Comments, review comments and replies, reviews and reactions "
        "the agent posts with `gh` on the session's PR, in every session. ask: "
        "each one opens a dialog showing the text and runs only after you click "
        "Post (macOS; elsewhere it is refused). never: GitHub stays read-only. "
        "allow: no question. Merging, closing, editing the PR and writes to other "
        "PRs are always refused. null: ask in supervised mode, allow in "
        "autonomous mode.",
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
    others_prs: OthersPrs = OthersPrs()
    classifiers: dict[Cmd, Classifier] = Field(
        default={"jev": JevClassifier()},
        description="Named decision backends. 'jev' is always defined; add "
        "`kind: command` entries for local classifiers.",
    )
    launch_check: LaunchCheck = LaunchCheck()
    tool_gate: ToolGate = ToolGate()
    prompts: Prompts = Prompts()

    @model_validator(mode="after")
    def _classifiers_exist(self) -> Config:
        if "jev" not in self.classifiers:
            # keep the built-in available when only others are added
            object.__setattr__(
                self, "classifiers", {"jev": JevClassifier(), **self.classifiers}
            )
        for role, name, needs in (
            ("launch_check", self.launch_check.classifier, "launch_command"),
            ("tool_gate", self.tool_gate.classifier, "hook_command"),
        ):
            if name is None:
                continue
            c = self.classifiers.get(name)
            if c is None:
                raise ValueError(
                    f"{role}.classifier: unknown classifier {name!r} "
                    f"(defined: {', '.join(sorted(self.classifiers))})"
                )
            if isinstance(c, CommandClassifier) and getattr(c, needs) is None:
                raise ValueError(f"{role}.classifier: {name!r} has no {needs}")
        return self


def allow_push_to_others(cfg: Config) -> bool:
    if cfg.others_prs.allow_push is not None:
        return cfg.others_prs.allow_push
    return cfg.mode == "autonomous"


def push_mode(cfg: Config) -> Literal["ask", "never", "allow"]:
    if cfg.push is not None:
        return cfg.push
    return "ask" if cfg.mode == "supervised" else "allow"


def github_writes_mode(cfg: Config) -> Literal["ask", "never", "allow"]:
    if cfg.github_writes is not None:
        return cfg.github_writes
    return "ask" if cfg.mode == "supervised" else "allow"


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
    return Path(base) / "llm-review-agent" / "config.yaml"


def find(explicit: str | None) -> Path | None:
    """--config, then $LLM_REVIEW_AGENT_CONFIG, then the XDG default path."""
    if explicit:
        return Path(explicit)
    if os.environ.get(ENV_VAR):
        return Path(os.environ[ENV_VAR])
    p = default_path()
    return p if p.exists() else None


def _describe(e: ValidationError) -> str:
    lines = []
    for err in e.errors():
        parts = list(err["loc"])
        if parts[:1] == ["classifiers"] and len(parts) > 2:
            del parts[2]  # the union tag pydantic adds after the name
        msg = err["msg"].removeprefix("Value error, ")
        # cross-field checks carry their own path in the message
        lines.append(f"  {'.'.join(map(str, parts))}: {msg}" if parts else f"  {msg}")
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


# --- `config generate`: a fully commented config with every default ----------

COMMAND_EXAMPLE = CommandClassifier(
    kind="command",
    launch_command="~/bin/pr-classifier launch",
    hook_command="~/bin/pr-classifier hook",
)


def _flatten(annotation: Any) -> list[Any]:
    """The members of a (possibly Annotated / Optional) type."""
    if get_origin(annotation) is Annotated:
        return _flatten(get_args(annotation)[0])
    if get_origin(annotation) in (Union, UnionType):
        return [t for arg in get_args(annotation) for t in _flatten(arg)]
    return [annotation]


def _allowed(annotation: Any) -> str | None:
    members = _flatten(annotation)
    values: list[str] = []
    for t in members:
        if get_origin(t) is Literal:
            values += [
                yaml.safe_dump(v).removesuffix("\n...\n").strip() for v in get_args(t)
            ]
        elif t is type(None):
            values.append("null")
        elif get_origin(t) is list:
            inner = _allowed(get_args(t)[0])
            return f"list of: {inner.removeprefix('one of: ')}" if inner else None
    if values and all(get_origin(t) is Literal or t is type(None) for t in members):
        return "one of: " + ", ".join(values)
    if "null" in values:
        return "or null"
    return None


def _limits(f: FieldInfo) -> str | None:
    parts = []
    for m in f.metadata:
        for attr, label in (("ge", ">="), ("gt", ">"), ("le", "<="), ("lt", "<")):
            if getattr(m, attr, None) is not None:
                parts.append(f"{label} {getattr(m, attr)}")
        if getattr(m, "min_length", None):
            parts.append(f"at least {m.min_length} item(s)")
    return ", ".join(parts) or None


def _scalar(value: Any) -> str:
    # dump inside a flow list so strings get quoted exactly when YAML needs it
    return yaml.safe_dump([value], default_flow_style=True, width=1000).strip()[1:-1]


def _comments(text: str, pad: str) -> list[str]:
    return [
        pad + "# " + line
        for line in textwrap.wrap(" ".join(text.split()), max(40, 78 - len(pad)))
    ]


def _emit(model: type[BaseModel], values: dict[str, Any], pad: str) -> list[str]:
    out: list[str] = []
    for name, f in model.model_fields.items():
        nested = f.annotation if isinstance(f.annotation, type) else None
        is_model = nested is not None and issubclass(nested, BaseModel)
        doc = f.description or (
            (nested.__doc__ or "").split("\n\n")[0] if is_model and nested else ""
        )
        if pad == "" and out:
            out.append("")
        if doc:
            out += _comments(doc, pad)
        hints = [h for h in (_allowed(f.annotation), _limits(f)) if h]
        if hints and not is_model:
            out.append(f"{pad}# ({'; '.join(hints)})")
        value = values[name]
        if name == "classifiers" and model is Config:
            out.append(f"{pad}{name}:")
            for key, c in value.items():
                cls = CommandClassifier if c.get("kind") == "command" else JevClassifier
                out += _comments((cls.__doc__ or "").split("\n\n")[0], pad + "  ")
                out.append(f"{pad}  {key}:")
                out += _emit(cls, c, pad + "    ")
            out += _comments(CommandClassifier.__doc__ or "", pad + "  ")
            example = _emit(
                CommandClassifier,
                COMMAND_EXAMPLE.model_dump(mode="json"),
                pad + "    ",
            )
            out.append(f"{pad}  # local:")
            out += [_comment_out(line, pad) for line in example]
        elif is_model and nested is not None:
            out.append(f"{pad}{name}:")
            out += _emit(nested, value, pad + "  ")
        else:
            out.append(f"{pad}{name}: {_scalar(value)}")
    return out


def _comment_out(line: str, pad: str) -> str:
    """Comment out an example line, keeping its nesting readable."""
    return f"{pad}  # {line[len(pad) + 2 :]}"


def generate() -> str:
    """A complete config: every key at its default, documented in comments."""
    header = [
        "# yaml-language-server: $schema=./config.schema.json",
        "#",
        "# llm-review-agent configuration, generated with every default and option.",
        "# Every key is optional: delete what you don't change. Command-line flags",
        "# override this file. Validate with `llm-review-agent config check`.",
        "#",
        f"# Location: --config, ${ENV_VAR}, or {default_path()}",
        "",
    ]
    return (
        "\n".join(header + _emit(Config, Config().model_dump(mode="json"), "")) + "\n"
    )
