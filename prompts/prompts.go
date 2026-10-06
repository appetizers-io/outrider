// Package prompts holds the text of the agent prompt (prompt.tmpl), filled in
// by internal/session with text/template.
package prompts

import "embed"

// FS holds the prompt templates.
//
//go:embed *.tmpl profiles/*.md
var FS embed.FS
