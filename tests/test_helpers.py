import json
import subprocess
import sys

import pytest

from llm_review_agent import app


@pytest.mark.parametrize(
    ("raw", "want"),
    [
        ("owner/repo", "owner/repo"),
        ("https://github.com/owner/repo", "owner/repo"),
        ("https://github.com/owner/repo.git/", "owner/repo"),
        ("git@github.com:owner/repo.git", "owner/repo"),
        ("ssh://git@github.com/owner/repo.git", "owner/repo"),
        ("  owner/* ", "owner/*"),
    ],
)
def test_repo_pattern(raw: str, want: str) -> None:
    assert app.repo_pattern(raw) == want


def test_repo_ok() -> None:
    assert app.repo_ok("o/r", [], [])
    assert app.repo_ok("o/r", ["o/*"], [])
    assert not app.repo_ok("x/r", ["o/*"], [])
    assert not app.repo_ok("o/r", ["o/*"], ["o/r"])


def item(at: str, **kw: object) -> app.ActivityItem:
    base: app.ActivityItem = {
        "kind": "comment",
        "id": 1,
        "state": None,
        "updated_at": at,
        "submitted_at": None,
        "user": "bob",
        "path": None,
        "at": at,
        "body": "",
    }
    return {**base, **kw}  # type: ignore[typeddict-item]


def test_newer_than_filters_and_sorts() -> None:
    items = [item("2026-01-03"), item("2026-01-01"), item("2026-01-02")]
    got = app.newer_than(items, "2026-01-01")
    assert [x["at"] for x in got] == ["2026-01-02", "2026-01-03"]


def test_newer_than_never_judged_caps_to_latest_ten() -> None:
    items = [item(f"2026-01-{d:02}") for d in range(1, 16)]
    got = app.newer_than(items, None)
    assert len(got) == 10
    assert got[-1]["at"] == "2026-01-15"


def test_fingerprint_is_stable() -> None:
    # Stored fingerprints are compared across versions; if this value changes,
    # every watched PR relaunches after an upgrade.
    items = [
        item("2026-01-01", id=7, state="COMMENTED", user="alice", body="x"),
        item("2026-01-02", id=8, updated_at=None, submitted_at="2026-01-02"),
    ]
    # value produced by the original review_fingerprint() for the same data
    golden = "bebc3e735402918524528a5a887b2147e7259d21d7519cb431f4bf244ff1e953"
    assert app.fingerprint(items) == golden
    items[0]["body"] = "edited body, same updated_at"
    assert app.fingerprint(items) == golden


def test_failing_checks() -> None:
    pr = {
        "statusCheckRollup": [
            {"name": "lint", "conclusion": "SUCCESS"},
            {"name": "test", "conclusion": "FAILURE"},
            {"context": "ci/legacy", "state": "ERROR"},
            {"name": "slow", "status": "IN_PROGRESS", "conclusion": None},
        ]
    }
    assert app.failing_checks(pr) == ["test", "ci/legacy"]
    assert app.failing_checks({}) == []


def test_load_state_defaults_and_migration() -> None:
    assert app.load_state()["watched"] == {}
    app.STATE.parent.mkdir(parents=True)
    app.STATE.write_text(json.dumps({"123": "2026-01-01T00:00:00Z"}))
    s = app.load_state()
    assert s["seen"] == {"123": "2026-01-01T00:00:00Z"}
    assert s["handled"] == {}
    assert not s["initialized"]


def test_state_roundtrip() -> None:
    s = app.load_state()
    s["handled"]["o/r#1"] = "2026-01-01T00:00:00Z"
    app.save_state(s)
    assert app.load_state()["handled"] == {"o/r#1": "2026-01-01T00:00:00Z"}


def test_run_timeout_becomes_called_process_error() -> None:
    with pytest.raises(subprocess.CalledProcessError) as e:
        app.run([sys.executable, "-c", "import time; time.sleep(5)"], timeout=0.2)
    assert "timed out" in e.value.stderr


def test_gh_json_invalid_json(monkeypatch: pytest.MonkeyPatch) -> None:
    def fake_run(args: list[str], **kw: object) -> subprocess.CompletedProcess[str]:
        return subprocess.CompletedProcess(args, 0, stdout="<html>oops</html>")

    monkeypatch.setattr(app, "run", fake_run)
    with pytest.raises(subprocess.CalledProcessError) as e:
        app.gh_json(["api", "user"])
    assert "invalid JSON" in e.value.stderr


@pytest.mark.parametrize(
    ("pattern", "login", "hit"),
    [
        ("*[bot]", "netlify[bot]", True),
        ("*[bot]", "robot", False),  # brackets are literal, not a class
        ("netlify[bot]", "Netlify[Bot]", True),
        ("coderabbit?i*", "coderabbitai[bot]", True),
        ("alice", "alice2", False),
    ],
)
def test_login_glob(pattern: str, login: str, hit: bool) -> None:
    assert bool(app.login_glob(pattern).fullmatch(login)) is hit
