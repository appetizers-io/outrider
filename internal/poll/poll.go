// Package poll is one pass over GitHub: notifications, opt-in candidates and
// watched PRs, deciding which events start an agent session.
package poll

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/appetizers-io/llm-review-agent/internal/config"
	"github.com/appetizers-io/llm-review-agent/internal/github"
	"github.com/appetizers-io/llm-review-agent/internal/session"
)

// Poller decides which GitHub events start a session.
type Poller struct {
	GH              *github.Client
	Cfg             *config.Config
	Login           string
	Include         []string // owner/repo globs; empty: every repo
	Exclude         []string
	ProcessExisting bool
	DryRun          bool
	StatePath       string // where finished passes save the state
	Launch          func(context.Context, session.Request) bool
	Log             *slog.Logger
	Now             func() time.Time
}

var prNumber = regexp.MustCompile(`/pulls/(\d+)$`)

func iso(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

func unix(t time.Time) float64 { return float64(t.UnixNano()) / 1e9 }

func key(repo string, n int) string { return fmt.Sprintf("%s#%d", repo, n) }

// pass is the per-poll context.
type pass struct {
	*Poller
	s              *State
	now            time.Time
	baseline       bool // first live run: record existing events, launch nothing
	repliesAfter   string
	mentionsAfter  string
	mine, cand, ig int
}

func (p *Poller) hoursAgo(now time.Time, h int) string {
	return iso(now.Add(-time.Duration(h) * time.Hour))
}

// Poll is one pass over notifications, candidates and watched PRs.
func (p *Poller) Poll(ctx context.Context, s *State) error {
	now := p.Now()
	cfg, trig := p.Cfg, p.Cfg.Triggers
	windowStart := p.hoursAgo(now, cfg.LookbackHours)
	ps := &pass{Poller: p, s: s, now: now, repliesAfter: windowStart, mentionsAfter: windowStart}
	if h := trig.ReviewReplies.FreshWithinHours; h != nil {
		ps.repliesAfter = p.hoursAgo(now, *h)
	}
	if h := trig.Mentions.FreshWithinHours; h != nil {
		ps.mentionsAfter = p.hoursAgo(now, *h)
	}

	ns, err := p.GH.Notifications(ctx, windowStart)
	if err != nil {
		return err
	}
	p.Log.Info(fmt.Sprintf("fetched %d notifications from last %dh", len(ns), cfg.LookbackHours))

	firstLive := !s.Initialized && !p.DryRun
	cutoff := unix(now) - float64(cfg.LookbackHours)*3600
	maps.DeleteFunc(s.Candidates, func(_ string, c Candidate) bool { return c.SeenAt < cutoff })
	ps.baseline = firstLive && !p.ProcessExisting

	// 1) Notification feed: own PRs + discover non-owned candidates.
	for _, x := range ns {
		repo := x.Repository.FullName
		if x.Subject.Type != "PullRequest" || !RepoOK(repo, p.Include, p.Exclude) {
			ps.ig++
			continue
		}
		m := prNumber.FindStringSubmatch(x.Subject.URL)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		k := key(repo, n)
		nid, updated := x.ID, x.UpdatedAt

		if _, ok := s.Watched[k]; ok {
			s.Seen[nid] = updated
			continue
		}
		if c, ok := s.Candidates[k]; ok {
			// known non-owned PR: refresh only, the reaction is checked below
			c.SeenAt = unix(now)
			s.Candidates[k] = c
			ps.cand++
			if s.Seen[nid] != updated && !ps.repliesAndMentions(ctx, repo, n, nil) {
				continue // keep the notification pending
			}
			s.Seen[nid] = updated
			continue
		}
		if seen, ok := s.Seen[nid]; ok && seen == updated && !p.DryRun {
			continue // already handled; skip the PR lookup
		}

		pr, err := p.GH.PRView(ctx, repo, n)
		if err != nil {
			continue
		}
		if !pr.Open() {
			delete(s.Candidates, k)
			s.Seen[nid] = updated
			continue
		}

		if !strings.EqualFold(pr.AuthorLogin(), p.Login) {
			// Keep this PR around even if we already saw the notification,
			// so a reaction added later can still opt it in.
			s.Candidates[k] = Candidate{Repo: repo, PR: n, SeenAt: unix(now)}
			ps.cand++
			if ps.repliesAndMentions(ctx, repo, n, &pr) {
				s.Seen[nid] = updated
			}
			continue
		}

		if seen, ok := s.Seen[nid]; ok && seen == updated {
			continue
		}
		ps.mine++
		if (firstLive && !p.ProcessExisting) || !trig.OwnPRs.Enabled {
			s.Seen[nid] = updated
			continue
		}

		t := s.Handled[k]
		if len(cfg.IgnoreAuthors) > 0 {
			items, err := p.GH.Activity(ctx, repo, n)
			if err != nil {
				continue
			}
			if items = NewerThan(items, t); len(items) > 0 && OnlyNoise(items, p.Login, cfg.IgnoreAuthors, false) {
				p.Log.Info(k + ": only ignored authors; not launching")
				if !p.DryRun {
					s.Seen[nid] = updated
					s.Handled[k] = iso(p.Now())
				}
				continue
			}
		}
		var gate func(context.Context) ([]github.Activity, error)
		if trig.OwnPRs.Check {
			gate = func(ctx context.Context) ([]github.Activity, error) {
				items, err := p.GH.Activity(ctx, repo, n)
				if err != nil {
					return nil, fmt.Errorf("activity of %s: %w", k, err)
				}
				return NewerThan(items, t), nil
			}
		}
		req := session.Request{Repo: repo, N: n, PR: pr, Trigger: "my PR notification (" + x.Reason + ")", Gate: gate}
		if p.Launch(ctx, req) && !p.DryRun {
			s.Seen[nid] = updated
			s.Handled[k] = iso(p.Now())
		}
	}

	if trig.OptIn.Enabled {
		ps.optIn(ctx)
	}
	return ps.finish(firstLive)
}

func (ps *pass) optIn(ctx context.Context) {
	s, cfg, optIn := ps.s, ps.Cfg, ps.Cfg.Triggers.OptIn
	emoji := github.ReactionEmoji[optIn.Reaction]

	// 2) Non-owned PRs I'm involved in, even without a recent notification,
	//    so the opt-in reaction on an older PR still counts.
	involved, err := ps.GH.InvolvedPRs(ctx, ps.Login)
	if err != nil {
		ps.Log.Warn("PR search failed; using notification candidates only: " + err.Error())
		involved = nil
	}
	for k, x := range involved {
		_, watched := s.Watched[k]
		if strings.EqualFold(x.Author, ps.Login) || watched || !RepoOK(x.Repo, ps.Include, ps.Exclude) {
			continue
		}
		s.Candidates[k] = Candidate{Repo: x.Repo, PR: x.N, SeenAt: unix(ps.now)}
	}

	// 3) Candidates: detect the opt-in reaction added AFTER we first saw them.
	keys := slices.SortedFunc(maps.Keys(s.Candidates), func(a, b string) int {
		return cmp.Or(cmp.Compare(s.Candidates[b].SeenAt, s.Candidates[a].SeenAt), cmp.Compare(a, b))
	})
	keys = keys[:min(len(keys), cfg.CandidateLimit)]
	refs := make([]github.Ref, len(keys))
	for i, k := range keys {
		refs[i] = github.Ref{Repo: s.Candidates[k].Repo, N: s.Candidates[k].PR}
	}
	eyes := ps.GH.MyEyes(ctx, refs, optIn.Reaction, optIn.Where)
	for _, ref := range refs {
		k := ref.Key()
		where := eyes[k]
		if where == nil {
			continue // no reaction, or lookup failed
		}
		pr, err := ps.GH.PRView(ctx, ref.Repo, ref.N)
		if err != nil {
			continue
		}
		if !pr.Open() {
			delete(s.Candidates, k)
			continue
		}
		if ps.DryRun {
			ps.Log.Info(fmt.Sprintf("%s detected %s (on %s)", emoji, k, *where))
		}
		trigger := fmt.Sprintf("%s opt-in (on %s)", emoji, *where)
		if ps.Launch(ctx, session.Request{Repo: ref.Repo, N: ref.N, PR: pr, Trigger: trigger}) && !ps.DryRun {
			var fp *string
			if items, err := ps.GH.Activity(ctx, ref.Repo, ref.N); err == nil {
				fp = new(Fingerprint(items))
			} // else: the next watch pass relaunches on change
			s.Handled[k] = iso(ps.Now())
			s.Watched[k] = Watched{Repo: ref.Repo, PR: ref.N, Fingerprint: fp}
			delete(s.Candidates, k)
		}
	}

	// 4) Already opted-in PRs: trigger only when review/discussion changes.
	keys = slices.Sorted(maps.Keys(s.Watched))
	refs = make([]github.Ref, len(keys))
	for i, k := range keys {
		refs[i] = github.Ref{Repo: s.Watched[k].Repo, N: s.Watched[k].PR}
	}
	eyes = ps.GH.MyEyes(ctx, refs, optIn.Reaction, optIn.Where)
	for i, k := range keys {
		w, ref := s.Watched[k], refs[i]
		where, ok := eyes[ref.Key()]
		if !ok {
			continue // lookup failed; keep watching
		}
		if where == nil {
			ps.Log.Info(fmt.Sprintf("%s: %s removed; stopping watch", k, emoji))
			if !ps.DryRun {
				delete(s.Watched, k)
			}
			continue
		}
		pr, err := ps.GH.PRView(ctx, ref.Repo, ref.N)
		if err != nil {
			continue
		}
		if !pr.Open() {
			if !ps.DryRun {
				delete(s.Watched, k)
			}
			continue
		}
		items, err := ps.GH.Activity(ctx, ref.Repo, ref.N)
		if err != nil {
			continue
		}
		fp := Fingerprint(items)
		if w.Fingerprint != nil && *w.Fingerprint == fp {
			continue
		}
		t := s.Handled[k]
		newer := NewerThan(items, t)
		if OnlyNoise(newer, ps.Login, cfg.IgnoreAuthors, optIn.OnChange.IgnoreOwnActivity) {
			// my own or ignored authors' activity, or edits/deletions only
			ps.Log.Info(k + ": nothing new from others; not launching")
			if !ps.DryRun {
				w.Fingerprint = &fp
				s.Watched[k] = w
				s.Handled[k] = iso(ps.Now())
			}
			continue
		}
		var gate func(context.Context) ([]github.Activity, error)
		if optIn.OnChange.Check {
			gate = func(context.Context) ([]github.Activity, error) { return newer, nil }
		}
		req := session.Request{Repo: ref.Repo, N: ref.N, PR: pr, Trigger: "review/discussion changed", Gate: gate}
		if ps.Launch(ctx, req) && !ps.DryRun {
			w.Fingerprint = &fp
			s.Watched[k] = w
			s.Handled[k] = iso(ps.Now())
		}
	}
}

func (ps *pass) finish(firstLive bool) error {
	prefix := ""
	if ps.DryRun {
		prefix = "dry-run "
	}
	note := ""
	if firstLive && !ps.ProcessExisting {
		note = " (first run: existing own notifications recorded only)"
	}
	ps.Log.Info(fmt.Sprintf("%ssummary: own_new=%d non_owned=%d watched=%d ignored=%d%s",
		prefix, ps.mine, ps.cand, len(ps.s.Watched), ps.ig, note))
	if ps.DryRun {
		return nil
	}
	ps.s.Initialized = true
	return ps.s.Save(ps.StatePath)
}

// repliesAndMentions runs both non-owned PR triggers; false keeps the notification pending.
func (ps *pass) repliesAndMentions(ctx context.Context, repo string, n int, pr *github.PR) bool {
	ok := ps.replies(ctx, repo, n, pr)
	return ps.mentions(ctx, repo, n, pr) && ok
}

// replies launches for unhandled replies to my review comments; false: retry.
// Only replies newer than repliesAfter count, so an old, unanswered thread
// never triggers a session once the tool (re)starts watching the PR.
func (ps *pass) replies(ctx context.Context, repo string, n int, pr *github.PR) bool {
	rules := ps.Cfg.Triggers.ReviewReplies
	if !rules.Enabled {
		return true
	}
	k := key(repo, n)
	done := ps.s.Replies[k]
	pending, err := ps.GH.PendingReplies(ctx, repo, n, ps.Login)
	if err != nil {
		return false
	}
	var fresh [][]github.Comment
	for _, cs := range pending {
		last := cs[len(cs)-1]
		if last.ID != nil && slices.Contains(done, *last.ID) {
			continue
		}
		if last.CreatedAt >= ps.repliesAfter && !IgnoredAuthor(last.UserLogin(), ps.Cfg.IgnoreAuthors) {
			fresh = append(fresh, cs)
		}
	}
	if len(fresh) > 0 && !ps.baseline {
		if pr == nil {
			got, err := ps.GH.PRView(ctx, repo, n)
			if err != nil {
				return false
			}
			pr = &got
		}
		if !pr.Open() {
			return true
		}
		var urls []string
		var thread []github.Activity
		for _, cs := range fresh {
			url := ""
			if u := cs[len(cs)-1].HTMLURL; u != nil {
				url = *u
			}
			urls = append(urls, url)
			for _, c := range cs {
				thread = append(thread, github.ActivityOf("inline comment", c))
			}
		}
		req := session.Request{Repo: repo, N: n, PR: *pr, Trigger: "reply to my review comment(s): " + strings.Join(urls, " ")}
		if rules.Check {
			req.Gate = func(context.Context) ([]github.Activity, error) { return thread, nil }
		}
		if rules.Scope == "thread" {
			req.Scope = urls
		}
		if !ps.Launch(ctx, req) {
			return false
		}
	}
	if !ps.DryRun {
		ids := slices.Clone(done)
		for _, cs := range fresh {
			if id := cs[len(cs)-1].ID; id != nil && !slices.Contains(ids, *id) {
				ids = append(ids, *id)
			}
		}
		slices.Sort(ids)
		ps.s.Replies[k] = ids
	}
	return true
}

func idString(id *int64) string {
	if id == nil {
		return "None"
	}
	return strconv.FormatInt(*id, 10)
}

// mentions launches for new @mentions of me on someone else's PR; false: retry.
func (ps *pass) mentions(ctx context.Context, repo string, n int, pr *github.PR) bool {
	rules := ps.Cfg.Triggers.Mentions
	if !rules.Enabled {
		return true
	}
	k := key(repo, n)
	done := ps.s.Mentions[k]
	replied := ps.s.Replies[k] // already handled as a reply
	if pr == nil {
		got, err := ps.GH.PRView(ctx, repo, n)
		if err != nil {
			return false
		}
		pr = &got
	}
	if !pr.Open() {
		return true
	}
	desc := github.ActivityOf("description", github.Comment{
		ID: new(int64(0)), User: pr.Author, UpdatedAt: &pr.CreatedAt, Body: &pr.Body, HTMLURL: &pr.URL,
	})
	items, err := ps.GH.Activity(ctx, repo, n)
	if err != nil {
		return false
	}
	var fresh []github.Activity
	for _, x := range append([]github.Activity{desc}, items...) {
		if x.At >= ps.mentionsAfter && Mentions(x.Body, ps.Login) &&
			!strings.EqualFold(x.UserLogin(), ps.Login) &&
			!IgnoredAuthor(x.UserLogin(), ps.Cfg.IgnoreAuthors) &&
			!slices.Contains(done, x.Kind+":"+idString(x.ID)) &&
			(x.ID == nil || !slices.Contains(replied, *x.ID)) {
			fresh = append(fresh, x)
		}
	}
	if len(fresh) > 0 && !ps.baseline {
		var urls []string
		for _, x := range fresh {
			url := pr.URL
			if x.URL != nil && *x.URL != "" {
				url = *x.URL
			}
			urls = append(urls, url)
		}
		req := session.Request{
			Repo: repo, N: n, PR: *pr, Trigger: "@" + ps.Login + " mentioned: " + strings.Join(urls, " "),
			ScopeWhy: "where someone mentioned @" + ps.Login,
		}
		if rules.Check {
			req.Gate = func(context.Context) ([]github.Activity, error) { return fresh, nil }
		}
		if rules.Scope == "comment" {
			req.Scope = urls
		}
		if !ps.Launch(ctx, req) {
			return false
		}
	}
	if !ps.DryRun {
		handled := slices.Clone(done)
		for _, x := range fresh {
			if id := x.Kind + ":" + idString(x.ID); !slices.Contains(handled, id) {
				handled = append(handled, id)
			}
		}
		slices.Sort(handled)
		ps.s.Mentions[k] = handled
	}
	return true
}
