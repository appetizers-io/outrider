package poll

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/github"
	"github.com/appetizers-io/outrider/internal/golden"
)

func at(at string, mod ...func(*github.Activity)) github.Activity {
	a := github.Activity{Kind: "comment", ID: new(int64(1)), UpdatedAt: new(at), User: new("bob"), At: at}
	for _, m := range mod {
		m(&a)
	}
	return a
}

func TestNewerThanFiltersAndSorts(t *testing.T) {
	got := NewerThan([]github.Activity{at("2026-01-03"), at("2026-01-01"), at("2026-01-02")}, "2026-01-01")
	require.Equal(t, "2026-01-02", got[0].At)
	require.Equal(t, "2026-01-03", got[1].At)
	require.Len(t, got, 2)
}

func TestNewerThanNeverJudgedCapsToLatestTen(t *testing.T) {
	var items []github.Activity
	for d := 1; d <= 15; d++ {
		items = append(items, at("2026-01-"+string(rune('0'+d/10))+string(rune('0'+d%10))))
	}
	got := NewerThan(items, "")
	require.Len(t, got, 10)
	require.Equal(t, "2026-01-15", got[9].At)
}

func TestFingerprintIsStable(t *testing.T) {
	// Stored fingerprints are compared across versions; if this value changes,
	// every watched PR relaunches after an upgrade.
	items := []github.Activity{
		at("2026-01-01", func(a *github.Activity) {
			a.ID = new(int64(7))
			a.State = new("COMMENTED")
			a.User = new("alice")
			a.Body = "x"
		}),
		at("2026-01-02", func(a *github.Activity) { a.ID = new(int64(8)); a.UpdatedAt = nil; a.SubmittedAt = new("2026-01-02") }),
	}
	golden := "bebc3e735402918524528a5a887b2147e7259d21d7519cb431f4bf244ff1e953" // what state files hold today
	require.Equal(t, golden, Fingerprint(items))
	items[0].Body = "edited body, same updated_at"
	require.Equal(t, golden, Fingerprint(items))
}

func TestFingerprintGolden(t *testing.T) {
	// fingerprints are kept in state.json: a different value relaunches every
	// watched PR once, so this one must not change
	items := []github.Activity{
		{Kind: "comment", ID: new(int64(1)), UpdatedAt: new("2026-01-01T00:00:00Z"), User: new("alice")},
		{Kind: "review", ID: new(int64(2)), State: new("APPROVED"), UpdatedAt: new("2026-01-02T00:00:00Z"), User: new("bob")},
		{Kind: "inline comment", ID: new(int64(3)), UpdatedAt: new("2026-01-03T00:00:00Z"), User: new("carol")},
	}
	golden.Check(t, "fingerprint.txt", Fingerprint(items)+"\n")
	// non-ASCII characters are escaped as \uXXXX, HTML characters are not
	got, err := asciiJSON("ü🚀 <&>\n\x7f\u2028")
	require.NoError(t, err)
	require.Equal(t, "\"\\u00fc\\ud83d\\ude80 <&>\\n\x7f\\u2028\"", got)
}

func TestMentions(t *testing.T) {
	mentions := Mentions("me")
	for text, hit := range map[string]bool{
		"@me":                           true,
		"what do you think, @Me?":       true,
		"(@me)":                         true,
		"cc @meadow":                    false,
		"me@example.com":                false,
		"x@me":                          false,
		"see a/@me":                     false,
		"@@me":                          false,
		"@me-too":                       false,
		"@me_":                          false,
		"ü@me":                          false,
		"line\n@me.":                    true,
		"nothing here":                  false,
		"@meadow and later @me as well": true,
	} {
		require.Equal(t, hit, mentions(text), text)
	}
}

func TestLoadStateDefaultsAndMigration(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	s, err := LoadState(p)
	r.NoError(err)
	r.Empty(s.Watched)
	r.NoError(os.WriteFile(p, []byte(`{"123": "2026-01-01T00:00:00Z"}`), 0o600))
	s, err = LoadState(p)
	r.NoError(err)
	r.Equal(map[string]string{"123": "2026-01-01T00:00:00Z"}, s.Seen)
	r.Empty(s.Handled)
	r.False(s.Initialized)
	r.NoError(os.WriteFile(p, []byte(`{"notifications": {"9": "t"}}`), 0o600))
	s, err = LoadState(p)
	r.NoError(err)
	r.Equal(map[string]string{"9": "t"}, s.Seen)
}

func TestStateRoundtripAndOlderFiles(t *testing.T) {
	r := require.New(t)
	p := filepath.Join(t.TempDir(), "state.json")
	// an older state file: a candidate without seen_at, an unknown key
	older := `{
  "candidates": {"o/r#5": {"pr": 5, "repo": "o/r", "seen_at": 1790000000.5}, "o/r#6": {"pr": 6, "repo": "o/r"}},
  "handled": {"o/r#1": "2026-01-01T00:00:00Z"},
  "initialized": true,
  "mentions": {"o/r#9": ["comment:2", "description:0"]},
  "replies": {"o/r#7": [11]},
  "seen": {"n1": "t1"},
  "watched": {"o/r#8": {"fingerprint": null, "pr": 8, "repo": "o/r"}},
  "unknown_future_key": 1
}`
	r.NoError(os.WriteFile(p, []byte(older), 0o600))
	s, err := LoadState(p)
	r.NoError(err)
	r.InDelta(1790000000.5, s.Candidates["o/r#5"].SeenAt, 0)
	r.Zero(s.Candidates["o/r#6"].SeenAt)
	r.Nil(s.Watched["o/r#8"].Fingerprint)
	r.Equal([]int64{11}, s.Replies["o/r#7"])
	r.NoError(s.Save(p))
	again, err := LoadState(p)
	r.NoError(err)
	r.Equal(s, again)
	_, err = os.Stat(filepath.Join(filepath.Dir(p), "state.tmp"))
	r.ErrorIs(err, os.ErrNotExist)
}
