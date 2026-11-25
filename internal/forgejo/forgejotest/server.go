// Package forgejotest is a fake Forgejo API for tests: one repository o/r
// with pull request #7 from a fork, its reviews, comments, statuses, linked
// issues and docs at the base commit.
package forgejotest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Commit ids of PR #7.
const (
	BaseSha = "1111111111111111111111111111111111111111"
	HeadSha = "2222222222222222222222222222222222222222"
)

// Web is where the fake forge says its pages are, unlike its API address.
const Web = "https://forge.example"

// Diff is PR #7's diff.
const Diff = "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-package old\n+package main\n"

// Body is PR #7's description: it links issues #3 (local), #4 (a URL on
// this forge), o/other#5 (a cross reference) and #6 on another host.
const Body = "Fixes #3. See " + Web + "/o/r/issues/4 and o/other#5, not https://elsewhere.example/o/r/issues/6."

// Server is the fake forge.
type Server struct {
	*httptest.Server
	Token string

	mu       sync.Mutex
	Requests []string // method and path of every request, without the query
	Auth     []string // the Authorization header of every request
}

// pageCap is the most items a page holds, whatever the client asks for, so
// clients have to page.
const pageCap = 2

// New starts the fake forge, which accepts token.
func New(t *testing.T, token string) *Server {
	t.Helper()
	s := &Server{Token: token}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func user(login string) map[string]any {
	return map[string]any{"login": login, "full_name": strings.ToUpper(login[:1]) + login[1:] + " Example", "email": login + "@forge.invalid"}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.Requests = append(s.Requests, r.Method+" "+r.URL.Path)
	s.Auth = append(s.Auth, r.Header.Get("Authorization"))
	s.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/")
	if path == "version" && r.Header.Get("Authorization") == "" {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Only signed in user is allowed to call APIs."}`))
		return
	}
	if r.Header.Get("Authorization") != "token "+s.Token {
		// a careless server that echoes what it was sent
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "token is invalid: " + r.Header.Get("Authorization")})
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	pr := "repos/o/r/pulls/7"
	switch path {
	case "version":
		s.json(w, map[string]string{"version": "15.0.0"})
	case "user":
		s.json(w, user("bot"))
	case "repos/o/r":
		s.json(w, map[string]any{"name": "r", "full_name": "o/r", "fork": false, "html_url": Web + "/o/r"})
	case "repos/alice/r-fork":
		s.json(w, map[string]any{"name": "r-fork", "full_name": "alice/r-fork", "fork": true, "parent": map[string]any{"full_name": "o/r"}})
	case pr:
		s.json(w, map[string]any{
			"number": 7, "title": "Fix <main>", "body": Body, "state": "open", "html_url": Web + "/o/r/pulls/7",
			"user": user("alice"), "labels": []any{map[string]any{"name": "bug"}}, "draft": false, "merged": false,
			"allow_maintainer_edit": true, "created_at": "2026-11-01T10:00:00Z",
			"head": map[string]any{"ref": "feat/x", "sha": HeadSha, "repo": map[string]any{"name": "r-fork", "full_name": "alice/r-fork", "owner": user("alice")}},
			"base": map[string]any{"ref": "main", "sha": BaseSha, "repo": map[string]any{"name": "r", "full_name": "o/r", "owner": user("o"), "html_url": Web + "/o/r"}},
		})
	case pr + ".diff":
		_, _ = w.Write([]byte(Diff))
	case "repos/o/r/commits/" + HeadSha + "/status":
		s.json(w, map[string]any{"state": "failure", "statuses": []any{
			map[string]any{"context": "ci/build", "status": "failure", "target_url": "https://ci.example/1"},
			map[string]any{"context": "ci/lint", "status": "success", "target_url": "https://ci.example/2"},
		}})
	case pr + "/files":
		s.page(w, r, []any{
			map[string]any{"filename": "main.go", "status": "changed", "additions": 1, "deletions": 1},
			map[string]any{"filename": "docs/new.md", "status": "added", "additions": 3, "deletions": 0},
			map[string]any{"filename": "bad\nname", "status": "added", "additions": 1, "deletions": 0},
		})
	case pr + "/commits":
		s.page(w, r, []any{
			map[string]any{"sha": HeadSha, "author": user("alice"), "commit": map[string]any{"message": "fix: main\n\nLonger text.", "author": map[string]any{"name": "Alice"}, "committer": map[string]any{"date": "2026-11-01T10:00:00Z"}}},
		})
	case pr + "/reviews":
		s.page(w, r, []any{
			map[string]any{"id": 11, "user": user("carol"), "state": "REQUEST_CHANGES", "body": "Please fix.", "comments_count": 2, "submitted_at": "2026-11-02T10:00:00Z"},
			map[string]any{"id": 12, "user": user("dave"), "state": "COMMENT", "body": "", "comments_count": 0},
			map[string]any{"id": 13, "user": user("erin"), "state": "APPROVED", "body": "LGTM", "comments_count": 0},
		})
	case pr + "/reviews/11/comments":
		// not paged: every page is the whole list
		s.json(w, []any{
			map[string]any{"id": 101, "body": "Rename this.", "path": "main.go", "position": 1, "pull_request_review_id": 11, "user": user("carol")},
			map[string]any{"id": 102, "body": "And this.", "path": "main.go", "position": 1, "pull_request_review_id": 11, "user": user("carol")},
		})
	case "repos/o/r/issues/7/comments":
		w.Header().Set("X-Total-Count", "1")
		s.json(w, []any{map[string]any{"id": 201, "user": user("frank"), "body": "Any news?", "created_at": "2026-11-03T10:00:00Z"}})
	case "repos/o/r/issues/3", "repos/o/r/issues/4", "repos/o/other/issues/5":
		n := path[strings.LastIndex(path, "/")+1:]
		s.json(w, map[string]any{"number": n, "title": "Issue " + n, "body": "Spec for " + n})
	case "repos/o/r/git/trees/" + BaseSha:
		s.json(w, map[string]any{"sha": BaseSha, "truncated": false, "total_count": 4, "tree": []any{
			map[string]any{"path": "README.md", "type": "blob"},
			map[string]any{"path": "docs", "type": "tree"},
			map[string]any{"path": "docs/spec.md", "type": "blob"},
			map[string]any{"path": "main.go", "type": "blob"},
		}})
	case "repos/o/r/raw/docs/spec.md":
		if r.URL.Query().Get("ref") != BaseSha {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("# Spec at base\n"))
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}
}

func (s *Server) json(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// page serves items in pages of at most pageCap, like a server whose
// MAX_RESPONSE_ITEMS is lower than what the client asks for.
func (s *Server) page(w http.ResponseWriter, r *http.Request, items []any) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	from := min((page-1)*pageCap, len(items))
	to := min(from+pageCap, len(items))
	w.Header().Set("X-Total-Count", fmt.Sprint(len(items)))
	s.json(w, items[from:to])
}

// Count is how many requests went to path.
func (s *Server) Count(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.Requests {
		if strings.HasSuffix(r, " /api/v1/"+path) {
			n++
		}
	}
	return n
}
