import json
from pathlib import Path
from typing import Any

import jsonschema
import pytest
import yaml

from llm_review_agent import app, config

REPO = Path(__file__).resolve().parents[1]


def test_defaults() -> None:
    c = config.Config()
    assert c.agent == "codex"
    assert c.triggers.opt_in.on_change.ignore_own_activity is True
    assert c.triggers.review_replies.scope == "thread"
    jev = c.classifiers["jev"]
    assert isinstance(jev, config.JevClassifier)
    assert jev.enabled == "auto"
    assert c.launch_check.classifier == "jev"
    assert c.launch_check.skip_below == 0.5
    assert c.tool_gate.classifier == "jev"


def test_empty_file_is_defaults(tmp_path: Path) -> None:
    p = tmp_path / "c.yaml"
    p.write_text("# nothing yet\n")
    assert config.load(p) == config.Config()


@pytest.mark.parametrize(
    ("data", "message"),
    [
        ({"agent": "gpt"}, "agent: Input should be 'codex' or 'claude'"),
        ({"max_agents": 0}, "max_agents: Input should be greater than or equal to 1"),
        ({"typo": 1}, "typo: Extra inputs are not permitted"),
        (
            {"classifiers": {"jev": {"enabled": "yes"}}},
            "classifiers.jev.enabled: Input should be 'auto', True or",
        ),
        (
            {"launch_check": {"skip_below": 2}},
            "launch_check.skip_below: Input should be less than or",
        ),
        (
            {"tool_gate": {"classifier": "nope"}},
            "tool_gate.classifier: unknown classifier 'nope' (defined: jev)",
        ),
        (
            {
                "classifiers": {"l": {"kind": "command", "hook_command": "x"}},
                "launch_check": {"classifier": "l"},
            },
            "launch_check.classifier: 'l' has no launch_command",
        ),
        ({"classifiers": {"l": {"kind": "magic"}}}, "classifiers.l: kind must be"),
        (
            {"classifiers": {"jev": {"hook_command": "x"}}},
            "classifiers.jev.hook_command: Extra inputs are not permitted",
        ),
        (
            {"triggers": {"opt_in": {"reaction": "eyez"}}},
            "triggers.opt_in.reaction: Input should be",
        ),
        (
            {"triggers": {"opt_in": {"where": ["comment", "comment"]}}},
            "triggers.opt_in.where: entries must be unique",
        ),
        ({"triggers": {"opt_in": {"where": []}}}, "triggers.opt_in.where: List should"),
        ({"ignore_authors": "bot"}, "ignore_authors: Input should be a valid list"),
        ({"max_agents": "3"}, "max_agents: Input should be a valid integer"),
        (["not", "a", "mapping"], "Input should be a valid dictionary"),
    ],
)
def test_invalid(data: Any, message: str) -> None:
    with pytest.raises(config.ConfigError) as e:
        config.parse(data, "c.yaml")
    assert message in str(e.value)
    assert str(e.value).startswith("c.yaml is invalid")


def test_unreadable_and_broken_files(tmp_path: Path) -> None:
    with pytest.raises(config.ConfigError, match="cannot read config"):
        config.load(tmp_path / "missing.yaml")
    p = tmp_path / "broken.yaml"
    p.write_text("agent: [claude\n")
    with pytest.raises(config.ConfigError, match="not valid YAML"):
        config.load(p)


def test_find_precedence(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    assert config.find(None) is None  # nothing configured
    default = config.default_path()
    default.parent.mkdir(parents=True)
    default.write_text("{}")
    assert config.find(None) == default
    monkeypatch.setenv(config.ENV_VAR, str(tmp_path / "env.yaml"))
    assert config.find(None) == tmp_path / "env.yaml"
    assert config.find("cli.yaml") == Path("cli.yaml")


def test_committed_schema_is_up_to_date() -> None:
    committed = (REPO / "config.schema.json").read_text()
    assert committed == config.schema_text(), (
        "config.schema.json is stale; regenerate with "
        "`uv run llm-review-agent config schema > config.schema.json`"
    )


def test_schema_documents_defaults() -> None:
    s = config.json_schema()
    on_change = s["$defs"]["OnChange"]["properties"]
    assert on_change["ignore_own_activity"]["default"] is True
    assert s["additionalProperties"] is False


def test_example_config_is_valid_for_pydantic_and_the_schema() -> None:
    data = yaml.safe_load((REPO / "config.example.yaml").read_text())
    config.parse(data)
    jsonschema.Draft202012Validator(config.json_schema()).validate(data)


@pytest.mark.parametrize(
    "bad",
    [
        {"agent": "gpt"},
        {"typo": 1},
        {"triggers": {"opt_in": {"where": ["comment", "comment"]}}},
        {"classifiers": {"jev": {"enabled": "yes"}}},
        {"classifiers": {"l": {"kind": "magic"}}},
        {"interval_seconds": 5},
    ],
)
def test_schema_rejects_what_pydantic_rejects(bad: dict[str, Any]) -> None:
    # editors validating against the schema must agree with the tool
    with pytest.raises(config.ConfigError):
        config.parse(bad)
    assert not jsonschema.Draft202012Validator(config.json_schema()).is_valid(bad)


def run_cli(*argv: str) -> int:
    with pytest.raises(SystemExit) as e:
        app.main(["config", *argv])
    return int(e.value.code or 0)


def test_cli_schema(capsys: pytest.CaptureFixture[str]) -> None:
    assert run_cli("schema") == 0
    assert (
        json.loads(capsys.readouterr().out)["title"] == "llm-review-agent configuration"
    )


def test_cli_check(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    good, bad = tmp_path / "good.yaml", tmp_path / "bad.yaml"
    good.write_text("agent: claude\n")
    bad.write_text("agent: gpt\n")
    assert run_cli("check", str(good)) == 0
    assert f"ok: {good}" in capsys.readouterr().out
    assert run_cli("check", str(bad)) == 1
    assert "agent: Input should be" in capsys.readouterr().err
    assert run_cli("check") == 0
    assert "built-in defaults" in capsys.readouterr().out


def test_cli_show_fills_defaults(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    p = tmp_path / "c.yaml"
    p.write_text("max_agents: 3\n")
    assert run_cli("show", str(p)) == 0
    shown = yaml.safe_load(capsys.readouterr().out)
    assert shown["max_agents"] == 3
    assert shown["triggers"]["opt_in"]["on_change"]["ignore_own_activity"] is True


def test_local_classifier_config() -> None:
    c = config.parse(
        {
            "classifiers": {
                "local": {
                    "kind": "command",
                    "launch_command": "~/bin/cls launch",
                    "hook_command": "~/bin/cls hook",
                }
            },
            "launch_check": {"classifier": "local"},
            "tool_gate": {"classifier": None},
        }
    )
    assert sorted(c.classifiers) == ["jev", "local"]  # jev stays defined
    assert isinstance(c.classifiers["local"], config.CommandClassifier)
    assert c.tool_gate.classifier is None
