// Package shell joins words into a POSIX shell command line with
// github.com/kballard/go-shellquote, also quoting a word-initial "#" (which
// go-shellquote leaves bare, so sh would read it as a comment).
package shell

import (
	"strings"

	"github.com/kballard/go-shellquote"
)

// Join quotes words for a POSIX shell and joins them with spaces.
func Join(words ...string) string {
	q := make([]string, len(words))
	for i, w := range words {
		if q[i] = shellquote.Join(w); strings.HasPrefix(q[i], "#") {
			q[i] = `\` + q[i]
		}
	}
	return strings.Join(q, " ")
}
