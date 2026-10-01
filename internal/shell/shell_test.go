package shell

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplit(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"jev-use", []string{"jev-use"}},
		{"  my-jev --flag  ", []string{"my-jev", "--flag"}},
		{`~/bin/cls 'a b' "c \"d\"" e\ f`, []string{"~/bin/cls", "a b", `c "d"`, "e f"}},
		{`x "a\b"`, []string{"x", `a\b`}},
		{"''", []string{""}},
		{"", nil},
	} {
		got, err := Split(tc.in)
		require.NoError(t, err, tc.in)
		require.Equal(t, tc.want, got, tc.in)
	}
	for _, bad := range []string{`'open`, `"open`, `x\`} {
		_, err := Split(bad)
		require.ErrorIs(t, err, ErrUnclosedQuote, bad)
	}
}

func TestQuoteAndJoin(t *testing.T) {
	require.Equal(t, "jev-use hook gate", Join([]string{"jev-use", "hook", "gate"}))
	require.Equal(t, `'PR o/r#1' '' 'it'"'"'s'`, Join([]string{"PR o/r#1", "", "it's"}))
	for _, words := range [][]string{{"a b", "c'd", `e"f`, "$x", "ü"}} {
		back, err := Split(Join(words))
		require.NoError(t, err)
		require.Equal(t, words, back)
	}
}
