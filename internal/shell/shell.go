// Package shell splits and quotes command lines like Python's shlex (POSIX).
package shell

import (
	"errors"
	"strings"
)

// ErrUnclosedQuote is a command line that ends inside a quote or after a backslash.
var ErrUnclosedQuote = errors.New("no closing quotation")

// Split splits a command line into words, honouring quotes and backslashes.
func Split(s string) ([]string, error) {
	var words []string
	var word strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case ' ', '\t', '\n', '\r':
			if inWord {
				words = append(words, word.String())
				word.Reset()
				inWord = false
			}
		case '\\':
			if i+1 >= len(s) {
				return nil, ErrUnclosedQuote
			}
			i++
			word.WriteByte(s[i])
			inWord = true
		case '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return nil, ErrUnclosedQuote
			}
			word.WriteString(s[i+1 : i+1+end])
			i += end + 1
			inWord = true
		case '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("\\\"$`\n", s[i+1]) >= 0 {
					i++
				}
				word.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, ErrUnclosedQuote
			}
			inWord = true
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, word.String())
	}
	return words, nil
}

func safe(r rune) bool {
	return r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_@%+=:,./-", r))
}

// Quote quotes s for a POSIX shell, leaving safe words alone.
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool { return !safe(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// Join quotes and joins words into a command line.
func Join(words []string) string {
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = Quote(w)
	}
	return strings.Join(q, " ")
}
