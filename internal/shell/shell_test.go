package shell

import (
	"testing"

	"github.com/kballard/go-shellquote"
	"github.com/stretchr/testify/require"
)

func TestJoinRoundTrips(t *testing.T) {
	for _, words := range [][]string{
		{"#x", "a#b", "#", "a b", "it's", `"q"`, "$HOME", "`x`", "~", "!x", `back\slash`, "", "ü", "-e"},
		{"jev-use", "hook", "gate"},
	} {
		back, err := shellquote.Split(Join(words...))
		require.NoError(t, err)
		require.Equal(t, words, back)
	}
	require.Equal(t, `a \#x`, Join("a", "#x"))
	require.Equal(t, "jev-use hook gate", Join("jev-use", "hook", "gate"))
}
