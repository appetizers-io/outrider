"""poll() against an in-memory GitHub: which events launch, and what is recorded."""

import datetime as dt
import subprocess
from dataclasses import dataclass, field
from typing import Any

import pytest

from gh_review_agent import app


def act(id: int, at: str, user: str = "bob", body: str = "") -> app.ActivityItem:
    return {
        "kind": "comment",
        "id": id,
        "state": None,
        "updated_at": at,
        "submitted_at": None,
        "user": user,
        "path": None,
        "at": at,
        "body": body,
    }


@dataclass
class Launch:
    key: str
    trigger: str
    gated: list[app.ActivityItem] | None
    scope: list[str] | None = None


@dataclass
class Hub:
    notifications: list[dict[str, Any]] = field(default_factory=list)
    prs: dict[str, dict[str, Any]] = field(default_factory=dict)
    activity: dict[str, list[app.ActivityItem]] = field(default_factory=dict)
    review_comments: dict[str, list[dict[str, Any]]] = field(default_factory=dict)
    eyes: dict[str, str | None] = field(default_factory=dict)
    involved: dict[str, tuple[str, int, str]] = field(default_factory=dict)
    launches: list[Launch] = field(default_factory=list)
    launch_ok: bool = True

    def pr(self, n: int, author: str, state: str = "OPEN") -> None:
        self.prs[f"o/r#{n}"] = {
            "number": n,
            "title": f"PR {n}",
            "state": state,
            "author": {"login": author},
        }

    def notify(self, nid: str, n: int, updated: str, reason: str = "comment") -> None:
        self.notifications.append(
            {
                "id": nid,
                "updated_at": updated,
                "reason": reason,
                "repository": {"full_name": "o/r"},
                "subject": {
                    "type": "PullRequest",
                    "url": f"https://api.github.com/repos/o/r/pulls/{n}",
                },
            }
        )


@pytest.fixture
def hub(monkeypatch: pytest.MonkeyPatch) -> Hub:
    h = Hub()

    def api(endpoint: str) -> list[Any]:
        if endpoint.startswith("notifications"):
            return h.notifications
        if "/pulls/" in endpoint and "/comments" in endpoint:
            n = endpoint.split("/pulls/")[1].split("/", maxsplit=1)[0]
            return h.review_comments.get(f"o/r#{n}", [])
        raise AssertionError(f"unexpected endpoint {endpoint}")

    def pr_view(repo: str, n: int) -> dict[str, Any]:
        return h.prs[f"{repo}#{n}"]

    def launch(
        repo: str,
        n: int,
        pr: dict[str, Any],
        trigger: str,
        a: app.Args,
        gate: app.Gate | None = None,
        scope: list[str] | None = None,
    ) -> bool:
        # Always evaluate the gate: it runs inside poll's scope, where a
        # shadowed name once turned it into "'str' object is not callable".
        gated = gate() if gate else None
        h.launches.append(Launch(f"{repo}#{n}", trigger, gated, scope))
        return h.launch_ok

    monkeypatch.setattr(app, "api", api)
    monkeypatch.setattr(app, "pr_view", pr_view)
    monkeypatch.setattr(app, "launch", launch)
    monkeypatch.setattr(app, "activity", lambda r, n: h.activity.get(f"{r}#{n}", []))
    monkeypatch.setattr(app, "involved_prs", lambda login: h.involved)
    monkeypatch.setattr(
        app,
        "my_eyes",
        lambda prs: {
            f"{r}#{n}": h.eyes[f"{r}#{n}"] for r, n in prs if f"{r}#{n}" in h.eyes
        },
    )
    return h


def args(*extra: str) -> app.Args:
    return app.parse_args(["--once", *extra])


def live_state() -> app.State:
    s = app.load_state()
    s["initialized"] = True
    return s


def test_first_run_only_records_existing_own_notifications(hub: Hub) -> None:
    hub.pr(1, "me")
    hub.notify("n1", 1, "t1")
    s = app.load_state()
    app.poll(s, args(), "me")
    assert hub.launches == []
    assert s["seen"] == {"n1": "t1"}
    assert s["initialized"]


def test_own_pr_notification_launches_with_new_activity(hub: Hub) -> None:
    hub.pr(1, "me")
    hub.notify("n1", 1, "t1", reason="review_requested")
    hub.activity["o/r#1"] = [act(1, "2026-01-01"), act(2, "2026-01-03")]
    s = live_state()
    s["handled"]["o/r#1"] = "2026-01-02"
    app.poll(s, args(), "me")
    [launch] = hub.launches
    assert launch.trigger == "my PR notification (review_requested)"
    assert launch.gated is not None
    assert [x["id"] for x in launch.gated] == [2]
    assert s["seen"] == {"n1": "t1"}
    assert s["handled"]["o/r#1"] > "2026-01-02"

    app.poll(s, args(), "me")  # same notification again: nothing new
    assert len(hub.launches) == 1


def test_failed_launch_keeps_notification_pending(hub: Hub) -> None:
    hub.pr(1, "me")
    hub.notify("n1", 1, "t1")
    hub.launch_ok = False
    s = live_state()
    app.poll(s, args(), "me")
    assert s["seen"] == {}
    hub.launch_ok = True
    app.poll(s, args(), "me")
    assert len(hub.launches) == 2
    assert s["seen"] == {"n1": "t1"}


def test_closed_pr_is_ignored(hub: Hub) -> None:
    hub.pr(1, "me", state="MERGED")
    hub.notify("n1", 1, "t1")
    s = live_state()
    app.poll(s, args(), "me")
    assert hub.launches == []
    assert s["seen"] == {"n1": "t1"}


def test_repo_filter(hub: Hub) -> None:
    hub.pr(1, "me")
    hub.notify("n1", 1, "t1")
    s = live_state()
    app.poll(s, args("--repo", "other/*"), "me")
    assert hub.launches == []


def test_eyes_on_involved_pr_opts_in_without_gate(hub: Hub) -> None:
    hub.pr(5, "bob")
    hub.involved = {"o/r#5": ("o/r", 5, "bob")}  # no notification at all
    hub.eyes["o/r#5"] = "review comment"
    hub.activity["o/r#5"] = [act(1, "2026-01-01")]
    s = live_state()
    app.poll(s, args(), "me")
    [launch] = hub.launches
    assert launch.trigger == "👀 opt-in (on review comment)"
    assert launch.gated is None  # explicit opt-in never asks Jev
    assert "o/r#5" in s["watched"]
    assert s["watched"]["o/r#5"]["fingerprint"] == app.fingerprint(
        hub.activity["o/r#5"]
    )
    assert "o/r#5" not in s["candidates"]


def test_own_prs_from_search_are_not_candidates(hub: Hub) -> None:
    hub.involved = {"o/r#6": ("o/r", 6, "me")}
    s = live_state()
    app.poll(s, args(), "me")
    assert s["candidates"] == {}


def watched_state(fp: str | None) -> app.State:
    s = live_state()
    s["watched"]["o/r#5"] = {"repo": "o/r", "pr": 5, "fingerprint": fp}
    s["handled"]["o/r#5"] = "2026-01-01"
    return s


def test_watched_pr_relaunches_on_change_with_gate(hub: Hub) -> None:
    hub.pr(5, "bob")
    hub.eyes["o/r#5"] = "comment"
    hub.activity["o/r#5"] = [act(1, "2026-01-01"), act(2, "2026-01-02")]
    s = watched_state("stale")
    app.poll(s, args(), "me")
    [launch] = hub.launches
    assert launch.trigger == "review/discussion changed"
    assert launch.gated is not None
    assert [x["id"] for x in launch.gated] == [2]
    assert s["watched"]["o/r#5"]["fingerprint"] == app.fingerprint(
        hub.activity["o/r#5"]
    )


def test_watched_pr_unchanged_does_nothing(hub: Hub) -> None:
    hub.pr(5, "bob")
    hub.eyes["o/r#5"] = "comment"
    hub.activity["o/r#5"] = [act(1, "2026-01-01")]
    s = watched_state(app.fingerprint(hub.activity["o/r#5"]))
    app.poll(s, args(), "me")
    assert hub.launches == []


def test_removing_eyes_stops_watch(hub: Hub) -> None:
    hub.pr(5, "bob")
    hub.eyes["o/r#5"] = None
    s = watched_state("x")
    app.poll(s, args(), "me")
    assert "o/r#5" not in s["watched"]


def test_failed_eyes_lookup_keeps_watch(hub: Hub) -> None:
    hub.pr(5, "bob")  # my_eyes returns nothing for o/r#5
    s = watched_state("x")
    app.poll(s, args(), "me")
    assert "o/r#5" in s["watched"]
    assert hub.launches == []


def fresh(minutes_ago: int) -> str:
    t = dt.datetime.now(dt.UTC) - dt.timedelta(minutes=minutes_ago)
    return t.strftime("%Y-%m-%dT%H:%M:%SZ")


def thread(reply_at: str) -> list[dict[str, Any]]:
    return [
        {"id": 10, "user": {"login": "me"}, "body": "why?", "created_at": "2026-01-01"},
        {
            "id": 11,
            "in_reply_to_id": 10,
            "user": {"login": "bob"},
            "body": "because",
            "created_at": reply_at,
            "html_url": "https://x/c11",
        },
        # someone else's thread: never mine, never triggers
        {"id": 20, "user": {"login": "carol"}, "body": "nit", "created_at": fresh(1)},
    ]


def test_reply_in_my_thread_launches_scoped_to_that_thread(hub: Hub) -> None:
    hub.pr(7, "bob")
    hub.notify("n7", 7, "t1")
    hub.review_comments["o/r#7"] = thread(fresh(5))
    s = live_state()
    app.poll(s, args(), "me")
    [launch] = hub.launches
    assert launch.trigger == "reply to my review comment(s): https://x/c11"
    assert launch.scope == ["https://x/c11"]
    assert launch.gated is not None
    assert [x["body"] for x in launch.gated] == ["why?", "because"]
    assert s["replies"]["o/r#7"] == [11]

    hub.notifications[0]["updated_at"] = "t2"  # new notification, same reply
    app.poll(s, args(), "me")
    assert len(hub.launches) == 1


def test_old_reply_outside_lookback_does_not_launch(hub: Hub) -> None:
    hub.pr(7, "bob")
    hub.notify("n7", 7, "t1")
    hub.review_comments["o/r#7"] = thread(fresh(5 * 60))
    s = live_state()
    app.poll(s, args("--lookback-hours", "2"), "me")
    assert hub.launches == []
    assert s["seen"] == {"n7": "t1"}


def test_watched_pr_ignores_my_own_activity(hub: Hub) -> None:
    hub.pr(5, "bob")
    hub.eyes["o/r#5"] = "comment"
    hub.activity["o/r#5"] = [
        act(1, "2026-01-01"),
        act(2, "2026-01-02", user="ME", body="tested this again"),
    ]
    s = watched_state("stale")
    app.poll(s, args(), "me")
    assert hub.launches == []
    assert s["watched"]["o/r#5"]["fingerprint"] == app.fingerprint(
        hub.activity["o/r#5"]
    )

    hub.activity["o/r#5"].append(act(3, "2099-01-01", body="please fix"))
    app.poll(s, args(), "me")
    [launch] = hub.launches
    assert launch.scope is None  # 👀: the whole PR
    assert launch.gated is not None
    assert [x["id"] for x in launch.gated] == [3]


def test_dry_run_changes_nothing(hub: Hub) -> None:
    hub.pr(1, "me")
    hub.notify("n1", 1, "t1")
    s = live_state()
    app.poll(s, args("--dry-run"), "me")
    assert len(hub.launches) == 1
    assert s["seen"] == {}
    assert not app.STATE.exists()


def test_search_failure_is_not_fatal(hub: Hub, monkeypatch: pytest.MonkeyPatch) -> None:
    def boom(login: str) -> Any:
        raise subprocess.CalledProcessError(1, "gh", stderr="rate limited")

    monkeypatch.setattr(app, "involved_prs", boom)
    app.poll(live_state(), args(), "me")
