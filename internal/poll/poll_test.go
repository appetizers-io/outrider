package poll

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/llm-review-agent/internal/config"
	"github.com/appetizers-io/llm-review-agent/internal/github"
	"github.com/appetizers-io/llm-review-agent/internal/proc"
	"github.com/appetizers-io/llm-review-agent/internal/session"
)

// hub is an in-memory GitHub behind a fake gh.
type hub struct {
	t              *testing.T
	notifications  []map[string]any
	prs            map[string]map[string]any
	activity       map[string][]map[string]any // conversation comments
	reviewComments map[string][]map[string]any
	eyes           map[string]*string // where my reaction is; missing: lookup fails
	involved       []map[string]any
	searchFails    bool
	launches       []launch
	launchOK       bool
}

type launch struct {
	key, trigger string
	gated        []github.Activity
	hasGate      bool
	scope        []string
}

func newHub(t *testing.T) *hub {
	return &hub{t: t, prs: map[string]map[string]any{}, activity: map[string][]map[string]any{},
		reviewComments: map[string][]map[string]any{}, eyes: map[string]*string{}, launchOK: true}
}

func (h *hub) pr(n int, author string, state ...string) {
	st := "OPEN"
	if len(state) > 0 {
		st = state[0]
	}
	h.prs[fmt.Sprintf("o/r#%d", n)] = map[string]any{"number": n, "title": fmt.Sprintf("PR %d", n), "state": st, "author": map[string]any{"login": author}}
}

func (h *hub) notify(nid string, n int, updated string, reason ...string) {
	r := "comment"
	if len(reason) > 0 {
		r = reason[0]
	}
	h.notifications = append(h.notifications, map[string]any{
		"id": nid, "updated_at": updated, "reason": r,
		"repository": map[string]any{"full_name": "o/r"},
		"subject":    map[string]any{"type": "PullRequest", "url": fmt.Sprintf("https://api.github.com/repos/o/r/pulls/%d", n)},
	})
}

func act(id int, at string, user string, body string) map[string]any {
	return map[string]any{"id": id, "updated_at": at, "user": map[string]any{"login": user}, "body": body}
}

func mention(id int, at, body string, user ...string) map[string]any {
	u := "bob"
	if len(user) > 0 {
		u = user[0]
	}
	m := act(id, at, u, body)
	m["html_url"] = fmt.Sprintf("https://x/c%d", id)
	return m
}

var (
	alias      = regexp.MustCompile(`p(\d+): repository\(owner: "([^"]*)", name: "([^"]*)"\) \{ pullRequest\(number: (\d+)\)`)
	pullsPath  = regexp.MustCompile(`^repos/o/r/(pulls|issues)/(\d+)/(comments|reviews)\?per_page=100$`)
	yesEyes    = map[string]any{"reactionGroups": []any{map[string]any{"content": "EYES", "viewerHasReacted": true}}}
	noReaction = map[string]any{"reactionGroups": []any{}}
)

func (h *hub) gh(_ context.Context, c proc.Cmd) (proc.Result, error) {
	a := c.Args[1:]
	out := func(v any) (proc.Result, error) {
		b, err := json.Marshal(v)
		require.NoError(h.t, err)
		return proc.Result{Stdout: string(b)}, nil
	}
	fail := func() (proc.Result, error) {
		return proc.Result{Code: 1}, &proc.Error{Args: c.Args, Code: 1, Stderr: "boom"}
	}
	switch {
	case a[0] == "pr" && a[1] == "view":
		pr, ok := h.prs[a[4]+"#"+a[2]]
		if !ok {
			return fail()
		}
		return out(pr)
	case a[0] == "api" && strings.HasPrefix(a[1], "notifications?"):
		return out([]any{h.notifications})
	case a[0] == "api" && a[1] == "graphql":
		data := map[string]any{}
		for _, m := range alias.FindAllStringSubmatch(a[3], -1) {
			where, ok := h.eyes[m[2]+"/"+m[3]+"#"+m[4]]
			if !ok {
				return fail()
			}
			pr := map[string]any{
				"reactionGroups": []any{},
				"comments":       map[string]any{"nodes": []any{}},
				"reviews":        map[string]any{"nodes": []any{}},
				"reviewThreads":  map[string]any{"nodes": []any{}},
			}
			if where != nil {
				switch *where {
				case "PR description":
					pr["reactionGroups"] = []any{
						map[string]any{"content": "EYES", "viewerHasReacted": true},
						map[string]any{"content": "ROCKET", "viewerHasReacted": true},
					}
				case "comment":
					pr["comments"] = map[string]any{"nodes": []any{noReaction, yesEyes}}
				case "review comment":
					pr["reviewThreads"] = map[string]any{"nodes": []any{map[string]any{"comments": map[string]any{"nodes": []any{yesEyes}}}}}
				}
			}
			data["p"+m[1]] = map[string]any{"pullRequest": pr}
		}
		return out(map[string]any{"data": data})
	case a[0] == "api" && a[1] == "-X" && a[3] == "search/issues":
		if h.searchFails {
			return fail()
		}
		if strings.Contains(a[5], "involves:") {
			return out(map[string]any{"items": h.involved})
		}
		return out(map[string]any{"items": []any{}})
	case a[0] == "api":
		m := pullsPath.FindStringSubmatch(a[1])
		require.NotNil(h.t, m, a)
		k := "o/r#" + m[2]
		switch {
		case m[1] == "pulls" && m[3] == "comments":
			return out([]any{h.reviewComments[k]})
		case m[1] == "issues":
			return out([]any{h.activity[k]})
		}
		return out([]any{[]any{}})
	}
	h.t.Fatalf("unexpected gh call %v", a)
	return proc.Result{}, nil
}

func (h *hub) launch(ctx context.Context, r session.Request) bool {
	// Always evaluate the gate, so a broken one shows up here.
	l := launch{key: fmt.Sprintf("%s#%d", r.Repo, r.N), trigger: r.Trigger, scope: r.Scope, hasGate: r.Gate != nil}
	if r.Gate != nil {
		items, err := r.Gate(ctx)
		require.NoError(h.t, err)
		l.gated = items
	}
	h.launches = append(h.launches, l)
	return h.launchOK
}

func (h *hub) poller(cfgText string) *Poller {
	cfg, err := config.Parse([]byte(cfgText), "c.yaml")
	require.NoError(h.t, err)
	ignore, err := config.LoginGlobs(cfg.IgnoreAuthors)
	require.NoError(h.t, err)
	return &Poller{
		GH: &github.Client{Run: h.gh}, Cfg: &cfg, Login: "me", Launch: h.launch, IgnoreAuthors: ignore,
		Log: slog.New(slog.DiscardHandler), Now: time.Now, StatePath: filepath.Join(h.t.TempDir(), "state.json"),
	}
}

func (h *hub) poll(p *Poller, s *State) {
	h.t.Helper()
	require.NoError(h.t, p.Poll(h.t.Context(), s))
}

func emptyState(t *testing.T) *State {
	s, err := LoadState(filepath.Join(t.TempDir(), "missing.json"))
	require.NoError(t, err)
	return s
}

func liveState(t *testing.T) *State {
	s := emptyState(t)
	s.Initialized = true
	return s
}

func ids(items []github.Activity) []int64 {
	var out []int64
	for _, x := range items {
		out = append(out, *x.ID)
	}
	return out
}

func fresh(minutesAgo int) string {
	return iso(time.Now().Add(-time.Duration(minutesAgo) * time.Minute))
}

func TestFirstRunOnlyRecordsExistingOwnNotifications(t *testing.T) {
	h := newHub(t)
	h.pr(1, "me")
	h.notify("n1", 1, "t1")
	s := emptyState(t)
	h.poll(h.poller(""), s)
	require.Empty(t, h.launches)
	require.Equal(t, map[string]string{"n1": "t1"}, s.Seen)
	require.True(t, s.Initialized)
}

func TestProcessExistingLaunchesOnTheFirstRun(t *testing.T) {
	h := newHub(t)
	h.pr(1, "me")
	h.notify("n1", 1, "t1")
	p := h.poller("")
	p.ProcessExisting = true
	h.poll(p, emptyState(t))
	require.Len(t, h.launches, 1)
}

func TestOwnPRNotificationLaunchesWithNewActivity(t *testing.T) {
	r := require.New(t)
	h := newHub(t)
	h.pr(1, "me")
	h.notify("n1", 1, "t1", "review_requested")
	h.activity["o/r#1"] = []map[string]any{act(1, "2026-01-01", "bob", ""), act(2, "2026-01-03", "bob", "")}
	s := liveState(t)
	s.Handled["o/r#1"] = "2026-01-02"
	p := h.poller("")
	h.poll(p, s)
	r.Len(h.launches, 1)
	r.Equal("my PR notification (review_requested)", h.launches[0].trigger)
	r.True(h.launches[0].hasGate)
	r.Equal([]int64{2}, ids(h.launches[0].gated))
	r.Equal(map[string]string{"n1": "t1"}, s.Seen)
	r.Greater(s.Handled["o/r#1"], "2026-01-02")

	h.poll(p, s) // same notification again: nothing new
	r.Len(h.launches, 1)
}

func TestFailedLaunchKeepsNotificationPending(t *testing.T) {
	h := newHub(t)
	h.pr(1, "me")
	h.notify("n1", 1, "t1")
	h.launchOK = false
	s := liveState(t)
	p := h.poller("")
	h.poll(p, s)
	require.Empty(t, s.Seen)
	h.launchOK = true
	h.poll(p, s)
	require.Len(t, h.launches, 2)
	require.Equal(t, map[string]string{"n1": "t1"}, s.Seen)
}

func TestClosedPRIsIgnored(t *testing.T) {
	h := newHub(t)
	h.pr(1, "me", "MERGED")
	h.notify("n1", 1, "t1")
	s := liveState(t)
	h.poll(h.poller(""), s)
	require.Empty(t, h.launches)
	require.Equal(t, map[string]string{"n1": "t1"}, s.Seen)
}

func TestRepoFilter(t *testing.T) {
	h := newHub(t)
	h.pr(1, "me")
	h.notify("n1", 1, "t1")
	p := h.poller("")
	include, err := config.RepoGlobs([]string{"other/*"})
	require.NoError(t, err)
	p.Include = include
	h.poll(p, liveState(t))
	require.Empty(t, h.launches)
}

func TestEyesOnInvolvedPROptsInWithoutGate(t *testing.T) {
	r := require.New(t)
	h := newHub(t)
	h.pr(5, "bob")
	h.involved = []map[string]any{{"repository_url": "https://api.github.com/repos/o/r", "number": 5, "user": map[string]any{"login": "bob"}}}
	h.eyes["o/r#5"] = new("review comment")
	h.activity["o/r#5"] = []map[string]any{act(1, "2026-01-01", "bob", "")}
	s := liveState(t)
	p := h.poller("")
	h.poll(p, s)
	r.Len(h.launches, 1)
	r.Equal("👀 opt-in (on review comment)", h.launches[0].trigger)
	r.False(h.launches[0].hasGate) // explicit opt-in never asks the classifier
	r.Contains(s.Watched, "o/r#5")
	items, err := p.GH.Activity(t.Context(), "o/r", 5)
	r.NoError(err)
	r.Equal(Fingerprint(items), *s.Watched["o/r#5"].Fingerprint)
	r.NotContains(s.Candidates, "o/r#5")
}

func TestOwnPRsFromSearchAreNotCandidates(t *testing.T) {
	h := newHub(t)
	h.involved = []map[string]any{{"repository_url": "https://api.github.com/repos/o/r", "number": 6, "user": map[string]any{"login": "me"}}}
	s := liveState(t)
	h.poll(h.poller(""), s)
	require.Empty(t, s.Candidates)
}

func watchedState(t *testing.T, fp *string) *State {
	s := liveState(t)
	s.Watched["o/r#5"] = Watched{Repo: "o/r", PR: 5, Fingerprint: fp}
	s.Handled["o/r#5"] = "2026-01-01"
	return s
}

func (h *hub) fingerprint(k string) string {
	n := strings.TrimPrefix(k, "o/r#")
	var num int
	_, _ = fmt.Sscan(n, &num)
	items, err := (&github.Client{Run: h.gh}).Activity(h.t.Context(), "o/r", num)
	require.NoError(h.t, err)
	return Fingerprint(items)
}

func TestWatchedPRRelaunchesOnChangeWithGate(t *testing.T) {
	r := require.New(t)
	h := newHub(t)
	h.pr(5, "bob")
	h.eyes["o/r#5"] = new("comment")
	h.activity["o/r#5"] = []map[string]any{act(1, "2026-01-01", "bob", ""), act(2, "2026-01-02", "bob", "")}
	s := watchedState(t, new("stale"))
	h.poll(h.poller(""), s)
	r.Len(h.launches, 1)
	r.Equal("review/discussion changed", h.launches[0].trigger)
	r.Equal([]int64{2}, ids(h.launches[0].gated))
	r.Equal(h.fingerprint("o/r#5"), *s.Watched["o/r#5"].Fingerprint)
}

func TestWatchedPRUnchangedDoesNothing(t *testing.T) {
	h := newHub(t)
	h.pr(5, "bob")
	h.eyes["o/r#5"] = new("comment")
	h.activity["o/r#5"] = []map[string]any{act(1, "2026-01-01", "bob", "")}
	h.poll(h.poller(""), watchedState(t, new(h.fingerprint("o/r#5"))))
	require.Empty(t, h.launches)
}

func TestRemovingEyesStopsWatch(t *testing.T) {
	h := newHub(t)
	h.pr(5, "bob")
	h.eyes["o/r#5"] = nil
	s := watchedState(t, new("x"))
	h.poll(h.poller(""), s)
	require.NotContains(t, s.Watched, "o/r#5")
}

func TestFailedEyesLookupKeepsWatch(t *testing.T) {
	h := newHub(t)
	h.pr(5, "bob") // the reaction lookup fails for o/r#5
	s := watchedState(t, new("x"))
	h.poll(h.poller(""), s)
	require.Contains(t, s.Watched, "o/r#5")
	require.Empty(t, h.launches)
}

func thread(replyAt string) []map[string]any {
	return []map[string]any{
		{"id": 10, "user": map[string]any{"login": "me"}, "body": "why?", "created_at": "2026-01-01"},
		{"id": 11, "in_reply_to_id": 10, "user": map[string]any{"login": "bob"}, "body": "because",
			"created_at": replyAt, "html_url": "https://x/c11"},
		// someone else's thread: never mine, never triggers
		{"id": 20, "user": map[string]any{"login": "carol"}, "body": "nit", "created_at": fresh(1)},
	}
}

func TestReplyInMyThreadLaunchesScopedToThatThread(t *testing.T) {
	r := require.New(t)
	h := newHub(t)
	h.pr(7, "bob")
	h.notify("n7", 7, "t1")
	h.reviewComments["o/r#7"] = thread(fresh(5))
	s := liveState(t)
	p := h.poller("")
	h.poll(p, s)
	r.Len(h.launches, 1)
	r.Equal("reply to my review comment(s): https://x/c11", h.launches[0].trigger)
	r.Equal([]string{"https://x/c11"}, h.launches[0].scope)
	var bodies []string
	for _, x := range h.launches[0].gated {
		bodies = append(bodies, x.Body)
	}
	r.Equal([]string{"why?", "because"}, bodies)
	r.Equal([]int64{11}, s.Replies["o/r#7"])

	h.notifications[0]["updated_at"] = "t2" // new notification, same reply
	h.poll(p, s)
	r.Len(h.launches, 1)
}

func TestOldReplyOutsideLookbackDoesNotLaunch(t *testing.T) {
	h := newHub(t)
	h.pr(7, "bob")
	h.notify("n7", 7, "t1")
	h.reviewComments["o/r#7"] = thread(fresh(5 * 60))
	s := liveState(t)
	h.poll(h.poller("lookback_hours: 2"), s)
	require.Empty(t, h.launches)
	require.Equal(t, map[string]string{"n7": "t1"}, s.Seen)
}

func TestWatchedPRIgnoresMyOwnActivity(t *testing.T) {
	r := require.New(t)
	h := newHub(t)
	h.pr(5, "bob")
	h.eyes["o/r#5"] = new("comment")
	h.activity["o/r#5"] = []map[string]any{act(1, "2026-01-01", "bob", ""), act(2, "2026-01-02", "ME", "tested this again")}
	s := watchedState(t, new("stale"))
	p := h.poller("")
	h.poll(p, s)
	r.Empty(h.launches)
	r.Equal(h.fingerprint("o/r#5"), *s.Watched["o/r#5"].Fingerprint)

	h.activity["o/r#5"] = append(h.activity["o/r#5"], act(3, "2099-01-01", "bob", "please fix"))
	h.poll(p, s)
	r.Len(h.launches, 1)
	r.Nil(h.launches[0].scope) // 👀: the whole PR
	r.Equal([]int64{3}, ids(h.launches[0].gated))
}

func TestDryRunChangesNothing(t *testing.T) {
	h := newHub(t)
	h.pr(1, "me")
	h.notify("n1", 1, "t1")
	s := liveState(t)
	p := h.poller("")
	p.DryRun = true
	h.poll(p, s)
	require.Len(t, h.launches, 1)
	require.Empty(t, s.Seen)
	_, err := os.Stat(p.StatePath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestSearchFailureIsNotFatal(t *testing.T) {
	h := newHub(t)
	h.searchFails = true
	h.poll(h.poller(""), liveState(t))
}

func TestNotificationFailureIsAnError(t *testing.T) {
	p := newHub(t).poller("")
	p.GH = &github.Client{Run: func(_ context.Context, c proc.Cmd) (proc.Result, error) {
		return proc.Result{Code: 1}, &proc.Error{Args: c.Args, Code: 1, Stderr: "connection reset"}
	}}
	require.ErrorContains(t, p.Poll(t.Context(), liveState(t)), "connection reset")
}

func TestPollSavesTheState(t *testing.T) {
	h := newHub(t)
	h.pr(1, "me")
	h.notify("n1", 1, "t1")
	p := h.poller("")
	h.poll(p, emptyState(t))
	s, err := LoadState(p.StatePath)
	require.NoError(t, err)
	require.True(t, s.Initialized)
	require.Equal(t, map[string]string{"n1": "t1"}, s.Seen)
}

// --- behaviour configured through the YAML file ----------------------------

func TestOwnPRsDisabled(t *testing.T) {
	h := newHub(t)
	h.pr(1, "me")
	h.notify("n1", 1, "t1")
	s := liveState(t)
	h.poll(h.poller("triggers: {own_prs: {enabled: false}}"), s)
	require.Empty(t, h.launches)
	require.Equal(t, map[string]string{"n1": "t1"}, s.Seen)
}

func TestNoCheckLaunchesWithoutGate(t *testing.T) {
	h := newHub(t)
	h.pr(1, "me")
	h.notify("n1", 1, "t1")
	h.poll(h.poller("triggers: {own_prs: {check: false}}"), liveState(t))
	require.Len(t, h.launches, 1)
	require.False(t, h.launches[0].hasGate)
}

func TestIgnoreAuthorsOnOwnPR(t *testing.T) {
	h := newHub(t)
	h.pr(1, "me")
	h.notify("n1", 1, "t1")
	h.activity["o/r#1"] = []map[string]any{act(1, "2026-01-02", "netlify[bot]", "")}
	s := liveState(t)
	p := h.poller(`ignore_authors: ["*[bot]"]`)
	h.poll(p, s)
	require.Empty(t, h.launches)
	require.Equal(t, map[string]string{"n1": "t1"}, s.Seen)

	h.notifications[0]["updated_at"] = "t2"
	h.activity["o/r#1"] = append(h.activity["o/r#1"], act(2, "2099-01-01", "alice", ""))
	h.poll(p, s)
	require.Len(t, h.launches, 1)
}

func TestIgnoreAuthorsOnWatchedPR(t *testing.T) {
	h := newHub(t)
	h.pr(5, "bob")
	h.eyes["o/r#5"] = new("comment")
	h.activity["o/r#5"] = []map[string]any{act(2, "2026-01-02", "coderabbitai[bot]", "")}
	h.poll(h.poller(`ignore_authors: ["coderabbitai*"]`), watchedState(t, new("stale")))
	require.Empty(t, h.launches)
}

func TestOwnActivityRelaunchesWhenNotIgnored(t *testing.T) {
	h := newHub(t)
	h.pr(5, "bob")
	h.eyes["o/r#5"] = new("comment")
	h.activity["o/r#5"] = []map[string]any{act(2, "2026-01-02", "me", "")}
	h.poll(h.poller("triggers: {opt_in: {on_change: {ignore_own_activity: false}}}"), watchedState(t, new("stale")))
	require.Len(t, h.launches, 1)
}

func TestOptInDisabledSkipsReactionLookups(t *testing.T) {
	h := newHub(t)
	h.pr(5, "bob")
	h.involved = []map[string]any{{"repository_url": "https://api.github.com/repos/o/r", "number": 5, "user": map[string]any{"login": "bob"}}}
	h.eyes["o/r#5"] = new("comment")
	s := watchedState(t, new("stale"))
	h.poll(h.poller("triggers: {opt_in: {enabled: false}}"), s)
	require.Empty(t, h.launches)
	require.Empty(t, s.Candidates)
}

func TestCustomReactionInTrigger(t *testing.T) {
	h := newHub(t)
	h.pr(5, "bob")
	h.involved = []map[string]any{{"repository_url": "https://api.github.com/repos/o/r", "number": 5, "user": map[string]any{"login": "bob"}}}
	h.eyes["o/r#5"] = new("PR description")
	h.poll(h.poller("triggers: {opt_in: {reaction: rocket}}"), liveState(t))
	require.Len(t, h.launches, 1)
	require.Equal(t, "🚀 opt-in (on PR description)", h.launches[0].trigger)
}

func TestRepliesScopePRAndDisabled(t *testing.T) {
	h := newHub(t)
	h.pr(7, "bob")
	h.notify("n7", 7, "t1")
	h.reviewComments["o/r#7"] = thread(fresh(5))
	h.poll(h.poller("triggers: {review_replies: {scope: pr}}"), liveState(t))
	require.Len(t, h.launches, 1)
	require.Nil(t, h.launches[0].scope)

	h.launches = nil
	h.poll(h.poller("triggers: {review_replies: {enabled: false}}"), liveState(t))
	require.Empty(t, h.launches)
}

func TestRepliesFreshnessWindow(t *testing.T) {
	h := newHub(t)
	h.pr(7, "bob")
	h.notify("n7", 7, "t1")
	h.reviewComments["o/r#7"] = thread(fresh(3 * 60)) // 3h old
	h.poll(h.poller("triggers: {review_replies: {fresh_within_hours: 2}}\nlookback_hours: 24"), liveState(t))
	require.Empty(t, h.launches)
	h.notifications[0]["updated_at"] = "t2" // the next notification
	h.poll(h.poller("triggers: {review_replies: {fresh_within_hours: 4}}\nlookback_hours: 24"), liveState(t))
	require.Len(t, h.launches, 1)
}

// --- @mentions --------------------------------------------------------------

func TestMentionOnOthersPRLaunchesScoped(t *testing.T) {
	r := require.New(t)
	h := newHub(t)
	h.pr(9, "bob")
	h.notify("n9", 9, "t1", "mention")
	h.activity["o/r#9"] = []map[string]any{
		mention(1, fresh(600), "@me old mention"), // outside the 2h window
		mention(2, fresh(5), "what do you think, @Me?"),
		mention(3, fresh(5), "cc @meadow and me@example.com"), // not me
		mention(4, fresh(5), "note to self @me", "me"),        // my own
	}
	s := liveState(t)
	p := h.poller("lookback_hours: 2")
	h.poll(p, s)
	r.Len(h.launches, 1)
	r.Equal("@me mentioned: https://x/c2", h.launches[0].trigger)
	r.Equal([]string{"https://x/c2"}, h.launches[0].scope)
	r.Equal([]int64{2}, ids(h.launches[0].gated))
	r.Equal([]string{"comment:2"}, s.Mentions["o/r#9"])

	h.notifications[0]["updated_at"] = "t2" // same mention, next notification
	h.poll(p, s)
	r.Len(h.launches, 1)
}

func TestMentionInDescription(t *testing.T) {
	h := newHub(t)
	h.pr(9, "bob")
	h.prs["o/r#9"]["body"] = "Implements X. @me could you review?"
	h.prs["o/r#9"]["createdAt"] = fresh(10)
	h.prs["o/r#9"]["url"] = "https://github.com/o/r/pull/9"
	h.notify("n9", 9, "t1", "mention")
	s := liveState(t)
	h.poll(h.poller(""), s)
	require.Len(t, h.launches, 1)
	require.Equal(t, []string{"https://github.com/o/r/pull/9"}, h.launches[0].scope)
	require.Equal(t, []string{"description:0"}, s.Mentions["o/r#9"])
}

func TestMentionThatIsAHandledReplyDoesNotLaunchTwice(t *testing.T) {
	h := newHub(t)
	h.pr(7, "bob")
	h.notify("n7", 7, "t1")
	h.reviewComments["o/r#7"] = thread(fresh(5)) // reply id 11
	h.activity["o/r#7"] = []map[string]any{mention(11, fresh(5), "@me because")}
	h.poll(h.poller(""), liveState(t))
	require.Len(t, h.launches, 1)
	require.True(t, strings.HasPrefix(h.launches[0].trigger, "reply to my review comment(s)"))
}

func TestMentionsDisabledAndScopePR(t *testing.T) {
	h := newHub(t)
	h.pr(9, "bob")
	h.notify("n9", 9, "t1", "mention")
	h.activity["o/r#9"] = []map[string]any{mention(2, fresh(5), "@me?")}
	h.poll(h.poller("triggers: {mentions: {enabled: false}}"), liveState(t))
	require.Empty(t, h.launches)
	h.notifications[0]["updated_at"] = "t2"
	h.poll(h.poller("triggers: {mentions: {scope: pr}}"), liveState(t))
	require.Len(t, h.launches, 1)
	require.Nil(t, h.launches[0].scope)
}

func TestMentionByIgnoredAuthor(t *testing.T) {
	h := newHub(t)
	h.pr(9, "bob")
	h.notify("n9", 9, "t1", "mention")
	h.activity["o/r#9"] = []map[string]any{mention(2, fresh(5), "@me ping", "renovate[bot]")}
	h.poll(h.poller(`ignore_authors: ["*[bot]"]`), liveState(t))
	require.Empty(t, h.launches)
}

func TestKnownCandidateOnlyRechecksNewNotifications(t *testing.T) {
	h := newHub(t)
	h.pr(9, "bob")
	h.notify("n9", 9, "t1", "mention")
	h.activity["o/r#9"] = []map[string]any{mention(2, fresh(5), "@me?")}
	s := liveState(t)
	p := h.poller("")
	h.poll(p, s) // first sight: PR becomes a candidate, mention launches
	require.Len(t, h.launches, 1)
	h.activity["o/r#9"] = append(h.activity["o/r#9"], mention(3, fresh(1), "@me again"))
	h.poll(p, s) // same notification: no lookups, nothing new
	require.Len(t, h.launches, 1)
	h.notifications[0]["updated_at"] = "t2"
	h.poll(p, s)
	require.Len(t, h.launches, 2)
	require.Equal(t, "@me mentioned: https://x/c3", h.launches[1].trigger)
}
