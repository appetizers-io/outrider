package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/config"
)

// The docs must stay correct: relative links resolve (files and headings),
// and every YAML snippet is a valid config.

func docFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("docs/*.md")
	require.NoError(t, err)
	examples, err := filepath.Glob("docs/examples/*.md")
	require.NoError(t, err)
	nested, err := filepath.Glob("docs/examples/*/*.md")
	require.NoError(t, err)
	files = append(append(files, examples...), nested...)
	return append(files, "README.md", "CONTRIBUTING.md", "MAINTAINERS.md")
}

var (
	link    = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	heading = regexp.MustCompile(`(?m)^#{1,6} (.+)$`)
	yamlBlk = regexp.MustCompile("(?s)```yaml\n(.*?)```")
	comment = regexp.MustCompile(`(?s)<!--.*?-->`)
)

// slug is GitHub's heading anchor.
func slug(h string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(h) {
		switch {
		case r == ' ':
			b.WriteRune('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

func anchors(t *testing.T, file string) []string {
	t.Helper()
	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	var out []string
	for _, m := range heading.FindAllStringSubmatch(string(raw), -1) {
		out = append(out, slug(m[1]))
	}
	return out
}

func TestDocsLinksResolve(t *testing.T) {
	for _, file := range docFiles(t) {
		raw, err := os.ReadFile(file)
		require.NoError(t, err)
		text := comment.ReplaceAllString(string(raw), "") // e.g. the demo placeholder
		for _, m := range link.FindAllStringSubmatch(text, -1) {
			target := m[1]
			if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			path, anchor, _ := strings.Cut(target, "#")
			resolved := file
			if path != "" {
				resolved = filepath.Join(filepath.Dir(file), filepath.FromSlash(path))
				_, err := os.Stat(resolved)
				require.NoError(t, err, "%s links to %s", file, target)
			}
			if anchor != "" && strings.HasSuffix(resolved, ".md") {
				require.Contains(t, anchors(t, resolved), anchor, "%s links to %s", file, target)
			}
		}
	}
}

func TestDocsConfigSnippetsAreValid(t *testing.T) {
	for _, file := range docFiles(t) {
		raw, err := os.ReadFile(file)
		require.NoError(t, err)
		for _, m := range yamlBlk.FindAllStringSubmatch(string(raw), -1) {
			_, err := config.Parse([]byte(m[1]), file)
			require.NoError(t, err, "%s:\n%s", file, m[1])
		}
	}
}

func TestDocsExampleConfigsAreValid(t *testing.T) {
	files, err := filepath.Glob("docs/examples/configs/*.yaml")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, file := range files {
		_, err := config.Load(file)
		require.NoError(t, err, file)
	}
}
