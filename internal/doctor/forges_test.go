package doctor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/forgejo/forgejotest"
)

const forgeToken = "doctor-secret-token-1234"

func forgeConfig(url string) string {
	return "forges:\n" +
		"  - {kind: forgejo, host: api.example, url: '" + url + "', token_env: FORGE_TOKEN}\n" +
		"  - {kind: forgejo, host: fj.example, url: '" + url + "', access: fj}\n"
}

func TestCheckForges(t *testing.T) {
	srv := forgejotest.New(t, forgeToken)
	fjArgs := "fj --style minimal --host " + srv.URL + " whoami"
	for _, tc := range []struct {
		name    string
		setup   func(m *machine)
		api, fj Status
		apiWant string
		fjWant  string
	}{
		{"logged in", func(m *machine) {
			m.env["FORGE_TOKEN"] = forgeToken
			m.tools = append(m.tools, "fj")
			m.outputs[fjArgs] = "currently signed into fjuser@fj.example\n"
		}, OK, OK, "(access api, Forgejo 15.0.0) as bot, with a token from $FORGE_TOKEN", "(access fj) as fjuser, with fj's login"},
		{"wrong token, fj logged out", func(m *machine) {
			m.env["FORGE_TOKEN"] = forgeToken + "-wrong"
			m.tools = append(m.tools, "fj")
		}, Fail, Fail, "the login fails: forge api.example: GET user: 401 Unauthorized: token is invalid: token [redacted]", "the login fails"},
		{"no token, no fj", nil, Fail, Fail, "$FORGE_TOKEN is empty or unset", "fj is not on PATH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := require.New(t)
			m := newMachine()
			m.cfg = forgeConfig(srv.URL)
			if tc.setup != nil {
				tc.setup(m)
			}
			got := checkForges(t.Context(), m.input(t))
			r.Len(got, 2)
			r.Equal("forge api.example", got[0].Name)
			r.Equal(tc.api, got[0].Status, got[0].Detail)
			r.Contains(got[0].Detail, tc.apiWant)
			r.Equal(tc.fj, got[1].Status, got[1].Detail)
			r.Contains(got[1].Detail, tc.fjWant)
			for _, res := range got {
				r.NotContains(res.Detail+res.Fix, forgeToken)
			}
		})
	}
}

func TestCheckForgeUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	m := newMachine()
	m.cfg = forgeConfig(url)
	got := checkForges(t.Context(), m.input(t))
	require.Equal(t, Fail, got[0].Status)
	require.True(t, strings.Contains(got[0].Detail, "does not answer"), got[0].Detail)
}

func TestNoForgesNoChecks(t *testing.T) {
	require.Empty(t, checkForges(t.Context(), newMachine().input(t)))
}
