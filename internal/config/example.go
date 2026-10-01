package config

import _ "embed"

// Example is config.example.yaml: every key at its default, documented.
// `config generate` prints it.
//
//go:embed config.example.yaml
var Example string
