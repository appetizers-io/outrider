package config

import (
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Override is a layer of per-PR keys over the config for the PRs its match
// selects. It keeps the YAML it was written as: For decodes that onto the
// config, so unset keys stay as they are, lists replace and maps merge.
type Override struct {
	Match []Identity
	node  *yaml.Node // the entry as written, match included
}

// Identity selects PRs: every attribute it sets must match; an unset one
// matches anything.
type Identity struct {
	Repo string `yaml:"repo" jsonschema:"minLength=1" jsonschema_description:"owner/repo glob (*, ?, [abc], [!abc], {a,b}), e.g. my-org/*."`
	URL  string `yaml:"url" jsonschema:"pattern=^(https?://|git@|ssh://git@)github\\.com[/:][^/]+/[^/]+?/?$" jsonschema_description:"GitHub repo URL, e.g. https://github.com/my-org/repo."`
	PRs  string `yaml:"prs" jsonschema:"enum=own,enum=others" jsonschema_description:"own: PRs you authored. others: everyone else's."`
}

// overrideKeys is an override as the schema describes it: match and the
// per-PR keys, nothing process-wide.
type overrideKeys struct {
	Match      []Identity `yaml:"match" jsonschema:"required,minItems=1" jsonschema_description:"PRs this override applies to: any of these identities."`
	PRSettings `yaml:",inline"`
}

// JSONSchemaAlias describes Override by its keys.
func (Override) JSONSchemaAlias() any { return overrideKeys{} }

// UnmarshalYAML keeps the entry and decodes its match.
func (o *Override) UnmarshalYAML(n *yaml.Node) error {
	var m struct {
		Match []Identity `yaml:"match"`
	}
	if err := n.Decode(&m); err != nil {
		return err
	}
	*o = Override{Match: m.Match, node: n}
	return nil
}

// MarshalYAML writes the entry as it was read.
func (o Override) MarshalYAML() (any, error) { return o.node, nil }

// keys is the entry without match: what For decodes onto the config.
func (o Override) keys() *yaml.Node {
	out := *o.node
	out.Content = nil
	for i := 0; i+1 < len(o.node.Content); i += 2 {
		if o.node.Content[i].Value != "match" {
			out.Content = append(out.Content, o.node.Content[i], o.node.Content[i+1])
		}
	}
	return &out
}

// without is the override without the given top-level keys.
func (o Override) without(keys []string) Override {
	n := *o.node
	n.Content = nil
	for i := 0; i+1 < len(o.node.Content); i += 2 {
		if !slices.Contains(keys, o.node.Content[i].Value) {
			n.Content = append(n.Content, o.node.Content[i], o.node.Content[i+1])
		}
	}
	return Override{Match: o.Match, node: &n}
}

// matches tells whether the identity selects a PR in repo, yours (own) or not.
func (id Identity) matches(repo string, own bool) bool {
	if id.PRs != "" && (id.PRs == "own") != own {
		return false
	}
	if id.URL != "" && !strings.EqualFold(RepoPattern(id.URL), repo) {
		return false
	}
	if id.Repo != "" {
		g, err := RepoGlobs([]string{id.Repo})
		return err == nil && g.Match(repo)
	}
	return true
}

// String is the identity as written, e.g. "repo: my-org/*, prs: others".
func (id Identity) String() string {
	var parts []string
	for _, a := range [][2]string{{"repo", id.Repo}, {"url", id.URL}, {"prs", id.PRs}} {
		if a[1] != "" {
			parts = append(parts, a[0]+": "+a[1])
		}
	}
	if len(parts) == 0 {
		return "any PR"
	}
	return strings.Join(parts, ", ")
}

func (o Override) matches(repo string, own bool) bool {
	return slices.ContainsFunc(o.Match, func(id Identity) bool { return id.matches(repo, own) })
}

// mayMatch tells whether the override can select your own PRs (own) or others'.
func (o Override) mayMatch(own bool) bool {
	return slices.ContainsFunc(o.Match, func(id Identity) bool { return id.PRs == "" || (id.PRs == "own") == own })
}

// name names the i-th override in messages, e.g. "overrides[0] (prs: others)".
func (o Override) name(i int) string {
	ids := make([]string, len(o.Match))
	for j, id := range o.Match {
		ids[j] = id.String()
	}
	return fmt.Sprintf("overrides[%d] (%s)", i, strings.Join(ids, " | "))
}

// Applied names the overrides that apply to a PR in repo, yours (own) or
// someone else's, in the order they apply.
func (c *Config) Applied(repo string, own bool) []string {
	var names []string
	for i, o := range c.Overrides {
		if o.matches(repo, own) {
			names = append(names, o.name(i))
		}
	}
	return names
}

// SandboxedBy names the overrides that set a read-only sandbox, for your own
// PRs (sandbox) or others' (others_prs.sandbox).
func (c *Config) SandboxedBy() []string {
	var names []string
	for i, o := range c.Overrides {
		var keys struct {
			Sandbox   *string `yaml:"sandbox"`
			OthersPRs struct {
				Sandbox *string `yaml:"sandbox"`
			} `yaml:"others_prs"`
		}
		if err := o.keys().Decode(&keys); err != nil {
			panic(err) // overrides were decoded when the config was loaded
		}
		for _, s := range []*string{keys.Sandbox, keys.OthersPRs.Sandbox} {
			if s != nil && *s == ReadOnly {
				names = append(names, o.name(i))
				break
			}
		}
	}
	return names
}

// Names names every override.
func (c *Config) Names() []string {
	names := make([]string, len(c.Overrides))
	for i, o := range c.Overrides {
		names[i] = o.name(i)
	}
	return names
}

// For is the config of a PR in repo, yours (own) or someone else's: this
// config with every matching override applied in file order. It has no
// overrides itself.
func (c *Config) For(repo string, own bool) Config {
	var layers []Override
	for _, o := range c.Overrides {
		if o.matches(repo, own) {
			layers = append(layers, o)
		}
	}
	return c.must(layers)
}

// Layers are the configs sessions can get: this one, this one with each
// override, and with every override that may match your own PRs, and
// others'. Startup checks (agents on PATH, sandbox support) run on each.
func (c *Config) Layers() []Config {
	out := []Config{c.must(nil)}
	for _, s := range c.stacks() {
		out = append(out, c.must(s.overrides))
	}
	return out
}

// Pin keeps keys at their value in this config: overrides no longer set
// them. Command-line flags pin what they set, so they beat overrides too.
func (c *Config) Pin(keys ...string) {
	for i, o := range c.Overrides {
		c.Overrides[i] = o.without(keys)
	}
}

func (c *Config) must(layers []Override) Config {
	out, err := c.with(layers)
	if err != nil {
		panic(err) // overrides were decoded when the config was loaded
	}
	return out
}

// with is this config with layers decoded onto a copy, in order. The copy is
// a YAML round trip, so the layers never write through the original's
// pointers and maps.
func (c *Config) with(layers []Override) (Config, error) {
	base := *c
	base.Overrides = nil
	if len(layers) == 0 {
		return base, nil
	}
	var node yaml.Node
	if err := node.Encode(&base); err != nil {
		return Config{}, fmt.Errorf("encode config: %w", err)
	}
	var out Config
	if err := node.Decode(&out); err != nil {
		return Config{}, fmt.Errorf("copy config: %w", err)
	}
	for _, o := range layers {
		if err := o.keys().Decode(&out); err != nil {
			return Config{}, fmt.Errorf("apply override: %w", err)
		}
	}
	return out, nil
}

// stack is a set of overrides that can apply together.
type stack struct {
	name      string
	overrides []Override
}

// stacks are each override alone, then every override that may match your
// own PRs, and others'.
func (c *Config) stacks() []stack {
	var out []stack
	for i, o := range c.Overrides {
		out = append(out, stack{o.name(i), []Override{o}})
	}
	for _, own := range []bool{true, false} {
		var all []Override
		for _, o := range c.Overrides {
			if o.mayMatch(own) {
				all = append(all, o)
			}
		}
		if len(all) > 1 {
			out = append(out, stack{map[bool]string{true: "overrides on your own PRs", false: "overrides on others' PRs"}[own], all})
		}
	}
	return out
}

// checkOverrides cross-checks the config with each stack of overrides applied.
func (c *Config) checkOverrides() []string {
	var errs []string
	for i, o := range c.Overrides {
		for _, id := range o.Match {
			if _, err := RepoGlobs([]string{id.Repo}); id.Repo != "" && err != nil {
				errs = append(errs, fmt.Sprintf("overrides[%d].match: %v", i, err))
			}
		}
	}
	seen := map[string]bool{}
	for _, s := range c.stacks() {
		cfg, err := c.with(s.overrides)
		if err != nil {
			errs = append(errs, s.name+": "+err.Error())
			continue
		}
		for _, e := range cfg.crossCheck() {
			if !seen[e] {
				seen[e] = true
				errs = append(errs, s.name+": "+e)
			}
		}
	}
	return errs
}
