"""poll() against an in-memory GitHub: which events launch, and what is recorded."""

import datetime as dt
import subprocess
from typing import Any

import pytest
from pydantic import BaseModel, Field

from llm_review_agent import app


def act(
    id: int, at: str, user: str = "bob", body: str = "", url: str | None = None
) -> app.ActivityItem:
    return app.ActivityItem(
        kind="comment",
        id=id,
        state=None,
        updated_at=at,
        submitted_at=None,
        user=user,
        path=None,
        at=at,
        body=body,
        url=url,
    )


class Launch(BaseModel):
    key: str
    trigger: str
    gated: list[app.ActivityItem] | None
    scope: list[str] | None = None


class Hub(BaseModel):
    notifications: list[dict[str, Any]] = Field(default_factory=list)
    prs: dict[str, dict[str, Any]] = Field(default_factory=dict)
    activity: dict[str, list[app.ActivityItem]] = Field(default_factory=dict)
    review_comments: dict[str, list[dict[str, Any]]] = Field(default_factory=dict)
    eyes: dict[str, str | None] = Field(default_factory=dict)
    involved: dict[str, tuple[str, int, str]] = Field(default_factory=dict)
    launches: list[Launch] = Field(default_factory=list)
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
        scope_why: str | None = None,
    ) -> bool:
        # Always evaluate the gate: it runs inside poll's scope, where a
        # shadowed name once turned it into "'str' object is not callable".
        gated = gate() if gate else None
        h.launches.append(
            Launch(key=f"{repo}#{n}", trigger=trigger, gated=gated, scope=scope)
        )
        return h.launch_ok

    monkeypatch.setattr(app, "api", api)
    monkeypatch.setattr(app, "pr_view", pr_view)
    monkeypatch.setattr(app, "launch", launch)
    monkeypatch.setattr(app, "activity", lambda r, n: h.activity.get(f"{r}#{n}", []))
    monkeypatch.setattr(app, "involved_prs", lambda login: h.involved)
    monkeypatch.setattr(
        app,
        "my_eyes",
        lambda prs, reaction="eyes", where=app.WHERE_ALL: {
            f"{r}#{n}": h.eyes[f"{r}#{n}"] for r, n in prs if f"{r}#{n}" in h.eyes
        },
    )
    return h


def args(*extra: str) -> app.Args:
    return app.parse_args(["--once", *extra])


def live_state() -> app.State:
    s = app.load_state()
    s.initialized = True
    return s


def test_first_run_only_records_existing_own_notifications(hub: Hub) -> None:
    hub.pr(1, "me")
    hub.notify("n1", 1, "t1")
    s = app.load_state()
    app.poll(s, args(), "me")
    assert hub.launches == []
    assert s.seen == {"n1": "t1"}
    assert s.initialized


def test_own_pr_notification_launches_with_new_activity(hub: Hub) -> None:
    hub.pr(1, "me")
    hub.notify("n1", 1, "t1", reason="review_requested")
    hub.activity["o/r#1"] = [act(1, "2026-01-01"), act(2, "2026-01-03")]
    s = live_state()
    s.handled["o/r#1"] = "2026-01-02"
    app.poll(s, args(), "me")
    [launch] = hub.launches
    assert launch.trigger == "my PR notification (review_requested)"
    assert launch.gated is not None
    assert [x.id for x in launch.gated] == [2]
    assert s.seen == {"n1": "t1"}
    assert s.handled["o/r#1"] > "2026-01-02"

    app.poll(s, args(), "me")  # same notification again: nothing new
    assert len(hub.launches) == 1


def test_failed_launch_keeps_notification_pending(hub: Hub) -> None:
    hub.pr(1, "me")
    hub.notify("n1", 1, "t1")
    hub.launch_ok = False
    s = live_state()
    app.poll(s, args(), "me")
    assert s.seen == {}
    hub.launch_ok = True
    app.poll(s, args(), "me")
    assert len(hub.launches) == 2
    assert s.seen == {"n1": "t1"}


def test_closed_pr_is_ignored(hub: Hub) -> None:
    hub.pr(1, "me", state="MERGED")
    hub.notify("n1", 1, "t1")
    s = live_state()
    app.poll(s, args(), "me")
    assert hub.launches == []
    assert s.seen == {"n1": "t1"}


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
    assert "o/r#5" in s.watched
    assert s.watched["o/r#5"].fingerprint == app.fingerprint(hub.activity["o/r#5"])
    assert "o/r#5" not in s.candidates


def test_own_prs_from_search_are_not_candidates(hub: Hub) -> None:
    hub.involved = {"o/r#6": ("o/r", 6, "me")}
    s = live_state()
    app.poll(s, args(), "me")
    assert s.candidates == {}


def watched_state(fp: str | None) -> app.State:
    s = live_state()
    s.watched["o/r#5"] = app.Watched(repo="o/r", pr=5, fingerprint=fp)
    s.handled["o/r#5"] = "2026-01-01"
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
    assert [x.id for x in launch.gated] == [2]
    assert s.watched["o/r#5"].fingerprint == app.fingerprint(hub.activity["o/r#5"])


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
    assert "o/r#5" not in s.watched


def test_failed_eyes_lookup_keeps_watch(hub: Hub) -> None:
    hub.pr(5, "bob")  # my_eyes returns nothing for o/r#5
    s = watched_state("x")
    app.poll(s, args(), "me")
    assert "o/r#5" in s.watched
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
    assert [x.body for x in launch.gated] == ["why?", "because"]
    assert s.replies["o/r#7"] == [11]

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
    assert s.seen == {"n7": "t1"}


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
    assert s.watched["o/r#5"].fingerprint == app.fingerprint(hub.activity["o/r#5"])

    hub.activity["o/r#5"].append(act(3, "2099-01-01", body="please fix"))
    app.poll(s, args(), "me")
    [launch] = hub.launches
    assert launch.scope is None  # 👀: the whole PR
    assert launch.gated is not None
    assert [x.id for x in launch.gated] == [3]


def test_dry_run_changes_nothing(hub: Hub) -> None:
    hub.pr(1, "me")
    hub.notify("n1", 1, "t1")
    s = live_state()
    app.poll(s, args("--dry-run"), "me")
    assert len(hub.launches) == 1
    assert s.seen == {}
    assert not app.STATE.exists()


def test_search_failure_is_not_fatal(hub: Hub, monkeypatch: pytest.MonkeyPatch) -> None:
    def boom(login: str) -> Any:
        raise subprocess.CalledProcessError(1, "gh", stderr="rate limited")

    monkeypatch.setattr(app, "involved_prs", boom)
    app.poll(live_state(), args(), "me")


# --- behaviour configured through the YAML file ------------------------------


@pytest.fixture
def cfg_args(tmp_path: Any) -> Any:
    def make(text: str, *extra: str) -> app.Args:
        p = tmp_path / "config.yaml"
        p.write_text(text)
        return app.parse_args(["--once", "--config", str(p), *extra])

    return make


def test_own_prs_disabled(hub: Hub, cfg_args: Any) -> None:
    hub.pr(1, "me")
    hub.notify("n1", 1, "t1")
    s = live_state()
    app.poll(s, cfg_args("triggers: {own_prs: {enabled: false}}"), "me")
    assert hub.launches == []
    assert s.seen == {"n1": "t1"}


def test_gate_none_launches_without_jev(hub: Hub, cfg_args: Any) -> None:
    hub.pr(1, "me")
    hub.notify("n1", 1, "t1")
    app.poll(live_state(), cfg_args("triggers: {own_prs: {check: false}}"), "me")
    [launch] = hub.launches
    assert launch.gated is None


def test_ignore_authors_on_own_pr(hub: Hub, cfg_args: Any) -> None:
    hub.pr(1, "me")
    hub.notify("n1", 1, "t1")
    hub.activity["o/r#1"] = [act(1, "2026-01-02", user="netlify[bot]")]
    s = live_state()
    app.poll(s, cfg_args('ignore_authors: ["*[bot]"]'), "me")
    assert hub.launches == []
    assert s.seen == {"n1": "t1"}

    hub.notifications[0]["updated_at"] = "t2"
    hub.activity["o/r#1"].append(act(2, "2099-01-01", user="alice"))
    app.poll(s, cfg_args('ignore_authors: ["*[bot]"]'), "me")
    assert len(hub.launches) == 1


def test_ignore_authors_on_watched_pr(hub: Hub, cfg_args: Any) -> None:
    hub.pr(5, "bob")
    hub.eyes["o/r#5"] = "comment"
    hub.activity["o/r#5"] = [act(2, "2026-01-02", user="coderabbitai[bot]")]
    app.poll(
        watched_state("stale"), cfg_args('ignore_authors: ["coderabbitai*"]'), "me"
    )
    assert hub.launches == []


def test_own_activity_relaunches_when_not_ignored(hub: Hub, cfg_args: Any) -> None:
    hub.pr(5, "bob")
    hub.eyes["o/r#5"] = "comment"
    hub.activity["o/r#5"] = [act(2, "2026-01-02", user="me")]
    text = "triggers: {opt_in: {on_change: {ignore_own_activity: false}}}"
    app.poll(watched_state("stale"), cfg_args(text), "me")
    assert len(hub.launches) == 1


def test_opt_in_disabled_skips_reaction_lookups(hub: Hub, cfg_args: Any) -> None:
    hub.pr(5, "bob")
    hub.involved = {"o/r#5": ("o/r", 5, "bob")}
    hub.eyes["o/r#5"] = "comment"
    s = watched_state("stale")
    app.poll(s, cfg_args("triggers: {opt_in: {enabled: false}}"), "me")
    assert hub.launches == []
    assert s.candidates == {}


def test_custom_reaction_in_trigger(hub: Hub, cfg_args: Any) -> None:
    hub.pr(5, "bob")
    hub.involved = {"o/r#5": ("o/r", 5, "bob")}
    hub.eyes["o/r#5"] = "PR description"
    app.poll(live_state(), cfg_args("triggers: {opt_in: {reaction: rocket}}"), "me")
    [launch] = hub.launches
    assert launch.trigger == "🚀 opt-in (on PR description)"


def test_replies_scope_pr_and_disabled(hub: Hub, cfg_args: Any) -> None:
    hub.pr(7, "bob")
    hub.notify("n7", 7, "t1")
    hub.review_comments["o/r#7"] = thread(fresh(5))
    app.poll(live_state(), cfg_args("triggers: {review_replies: {scope: pr}}"), "me")
    [launch] = hub.launches
    assert launch.scope is None

    hub.launches.clear()
    text = "triggers: {review_replies: {enabled: false}}"
    app.poll(live_state(), cfg_args(text), "me")
    assert hub.launches == []


def test_replies_freshness_window(hub: Hub, cfg_args: Any) -> None:
    hub.pr(7, "bob")
    hub.notify("n7", 7, "t1")
    hub.review_comments["o/r#7"] = thread(fresh(3 * 60))  # 3h old
    text = "triggers: {review_replies: {fresh_within_hours: 2}}"
    app.poll(live_state(), cfg_args(text, "--lookback-hours", "24"), "me")
    assert hub.launches == []
    hub.notifications[0]["updated_at"] = "t2"  # the next notification
    text = "triggers: {review_replies: {fresh_within_hours: 4}}"
    app.poll(live_state(), cfg_args(text, "--lookback-hours", "24"), "me")
    assert len(hub.launches) == 1


# --- @mentions ----------------------------------------------------------------


def mention(id: int, at: str, body: str, user: str = "bob") -> app.ActivityItem:
    return act(id, at, user=user, body=body, url=f"https://x/c{id}")


def test_mention_on_others_pr_launches_scoped(hub: Hub) -> None:
    hub.pr(9, "bob")
    hub.notify("n9", 9, "t1", reason="mention")
    hub.activity["o/r#9"] = [
        mention(1, fresh(600), "@me old mention"),  # outside the 2h window
        mention(2, fresh(5), "what do you think, @Me?"),
        mention(3, fresh(5), "cc @meadow and me@example.com"),  # not me
        mention(4, fresh(5), "note to self @me", user="me"),  # my own
    ]
    s = live_state()
    app.poll(s, args("--lookback-hours", "2"), "me")
    [launch] = hub.launches
    assert launch.trigger == "@me mentioned: https://x/c2"
    assert launch.scope == ["https://x/c2"]
    assert launch.gated is not None
    assert [x.id for x in launch.gated] == [2]
    assert s.mentions["o/r#9"] == ["comment:2"]

    hub.notifications[0]["updated_at"] = "t2"  # same mention, next notification
    app.poll(s, args("--lookback-hours", "2"), "me")
    assert len(hub.launches) == 1


def test_mention_in_description(hub: Hub) -> None:
    hub.pr(9, "bob")
    hub.prs["o/r#9"].update(
        body="Implements X. @me could you review?",
        createdAt=fresh(10),
        url="https://github.com/o/r/pull/9",
    )
    hub.notify("n9", 9, "t1", reason="mention")
    app.poll(live_state(), args(), "me")
    [launch] = hub.launches
    assert launch.scope == ["https://github.com/o/r/pull/9"]


def test_mention_that_is_a_handled_reply_does_not_launch_twice(hub: Hub) -> None:
    hub.pr(7, "bob")
    hub.notify("n7", 7, "t1")
    hub.review_comments["o/r#7"] = thread(fresh(5))  # reply id 11
    hub.activity["o/r#7"] = [mention(11, fresh(5), "@me because")]
    app.poll(live_state(), args(), "me")
    assert [x.trigger.split(":")[0] for x in hub.launches] == [
        "reply to my review comment(s)"
    ]


def test_mentions_disabled_and_scope_pr(hub: Hub, cfg_args: Any) -> None:
    hub.pr(9, "bob")
    hub.notify("n9", 9, "t1", reason="mention")
    hub.activity["o/r#9"] = [mention(2, fresh(5), "@me?")]
    app.poll(live_state(), cfg_args("triggers: {mentions: {enabled: false}}"), "me")
    assert hub.launches == []
    hub.notifications[0]["updated_at"] = "t2"
    app.poll(live_state(), cfg_args("triggers: {mentions: {scope: pr}}"), "me")
    [launch] = hub.launches
    assert launch.scope is None


def test_mention_by_ignored_author(hub: Hub, cfg_args: Any) -> None:
    hub.pr(9, "bob")
    hub.notify("n9", 9, "t1", reason="mention")
    hub.activity["o/r#9"] = [mention(2, fresh(5), "@me ping", user="renovate[bot]")]
    app.poll(live_state(), cfg_args('ignore_authors: ["*[bot]"]'), "me")
    assert hub.launches == []
