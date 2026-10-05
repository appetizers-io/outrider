package github

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/proc"
)

func TestCIPassed(t *testing.T) {
	for _, tc := range []struct {
		checks []Check
		want   bool
	}{
		{nil, false},
		{[]Check{{Status: "COMPLETED", Conclusion: "SUCCESS"}, {State: "SUCCESS"}}, true},
		{[]Check{{Status: "IN_PROGRESS"}}, false},
		{[]Check{{Status: "COMPLETED", Conclusion: "FAILURE"}}, false},
		{[]Check{{Status: "COMPLETED", Conclusion: "SKIPPED"}}, false},
		{[]Check{{State: "PENDING"}}, false},
		{[]Check{{}}, false},
	} {
		require.Equal(t, tc.want, CIPassed(PR{StatusCheckRollup: tc.checks}))
	}
}

func TestDiscussionsResolvedPagination(t *testing.T) {
	for _, last := range []string{"true", "false", "null"} {
		t.Run(last, func(t *testing.T) {
			calls := 0
			c := &Client{Run: func(_ context.Context, cmd proc.Cmd) (proc.Result, error) {
				calls++
				query := strings.Join(cmd.Args, " ")
				require.Contains(t, query, "reviewThreads(first: 100")
				if calls == 1 {
					return proc.Result{Stdout: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"isResolved":true}],"pageInfo":{"hasNextPage":true,"endCursor":"next"}}}}}}`}, nil
				}
				require.Contains(t, query, `after: "next"`)
				return proc.Result{Stdout: fmt.Sprintf(`{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"isResolved":%s}],"pageInfo":{"hasNextPage":false}}}}}}`, last)}, nil
			}}
			ready, err := c.DiscussionsResolved(context.Background(), "o/r", 1)
			if last == "null" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, last == "true", ready)
			require.Equal(t, 2, calls)
		})
	}
}

func TestDiscussionsResolvedMissingData(t *testing.T) {
	for _, raw := range []string{
		`{"errors":[{"message":"no access"}],"data":null}`,
		`{"data":{"repository":null}}`,
		`{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{}]}}}}}`,
		`{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":true}}}}}}`,
	} {
		c := &Client{Run: func(context.Context, proc.Cmd) (proc.Result, error) { return proc.Result{Stdout: raw}, nil }}
		ready, err := c.DiscussionsResolved(context.Background(), "o/r", 1)
		require.Error(t, err)
		require.False(t, ready)
	}
	c := &Client{Run: func(context.Context, proc.Cmd) (proc.Result, error) {
		return proc.Result{Stdout: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}`}, nil
	}}
	ready, err := c.DiscussionsResolved(context.Background(), "o/r", 1)
	require.NoError(t, err)
	require.False(t, ready)
}
