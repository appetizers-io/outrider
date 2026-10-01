package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// docs describe the config sections; the first paragraph also heads the
// section in `config generate`.
var docs = map[reflect.Type]string{
	reflect.TypeFor[Config]():        "llm-review-agent configuration. Every key is optional; command-line flags\noverride this file.",
	reflect.TypeFor[OwnPRs]():        "Notifications on PRs you authored.",
	reflect.TypeFor[OnChange]():      "Relaunch when reviews or comments on an opted-in PR change.",
	reflect.TypeFor[OptIn]():         "Your reaction on someone else's PR opts it in; sessions cover the whole PR.",
	reflect.TypeFor[ReviewReplies](): "Someone replies in a review thread you took part in.",
	reflect.TypeFor[Mentions]():      "Someone @mentions your login on a PR that is neither yours nor opted in\n(those already relaunch for any new activity).",
	reflect.TypeFor[JevClassifier](): "Jev via jev-use: `<command> judge` for launch checks, `<command> hook\ngate` as the tool gate.",
	reflect.TypeFor[CommandClassifier](): "Any local classifier.\n\nlaunch_command gets a JSON request on stdin and prints\n" +
		"{\"launch\": bool} or {\"probability\": 0..1}, optionally with \"reason\".\n" +
		"hook_command is a Claude Code / Codex PreToolUse hook; the session policy\n" +
		"is in $LLM_REVIEW_AGENT_POLICY_FILE and $LLM_REVIEW_AGENT_GATE_TEXT.",
	reflect.TypeFor[LaunchCheck](): "Ask a classifier whether new activity is worth an agent session.",
	reflect.TypeFor[ToolGate]():    "Check every tool call in supervised agent sessions (PreToolUse hook).",
	reflect.TypeFor[OthersPRs]():   "Sessions on PRs someone else authored.",
}

// ordered is a JSON object that keeps its key order.
type ordered []struct {
	k string
	v any
}

func (o *ordered) set(k string, v any) {
	*o = append(*o, struct {
		k string
		v any
	}{k, v})
}

func (o ordered) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, kv := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, err := marshal(kv.k)
		if err != nil {
			return nil, err
		}
		v, err := marshal(kv.v)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encode %T: %w", v, err)
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// plain turns a config value into plain JSON data (its YAML form).
func plain(v any) any {
	out, err := yaml.Marshal(v)
	if err != nil {
		panic(err) // config values always marshal
	}
	var data any
	if err := yaml.Unmarshal(out, &data); err != nil {
		panic(err)
	}
	return data
}

func yamlName(f reflect.StructField) string { return strings.Split(f.Tag.Get("yaml"), ",")[0] }

// title is pydantic's field title: "owner_name" -> "Owner Name".
func title(name string) string {
	words := strings.Split(name, "_")
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

type schemaBuilder struct{ defs map[string]any }

// Schema is the JSON Schema of the config file.
func Schema() map[string]any {
	b := schemaBuilder{defs: map[string]any{}}
	root := b.object(reflect.TypeFor[Config](), reflect.ValueOf(Default()))
	root["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	root["$id"] = SchemaID
	root["title"] = "llm-review-agent configuration"
	root["$defs"] = b.defs
	return root
}

// SchemaText is Schema as indented JSON.
func SchemaText() string {
	raw, err := marshal(Schema())
	if err != nil {
		panic(err)
	}
	var b bytes.Buffer
	if err := json.Indent(&b, raw, "", "  "); err != nil {
		panic(err)
	}
	return b.String() + "\n"
}

func (b schemaBuilder) object(t reflect.Type, def reflect.Value) map[string]any {
	props := ordered{}
	for i := range t.NumField() {
		f := t.Field(i)
		props.set(yamlName(f), b.property(f, def.Field(i)))
	}
	s := map[string]any{"additionalProperties": false, "properties": props, "title": t.Name(), "type": "object"}
	if doc := docs[t]; doc != "" {
		s["description"] = doc
	}
	if t == reflect.TypeFor[CommandClassifier]() {
		s["required"] = []string{"kind"}
	}
	return s
}

func (b schemaBuilder) ref(t reflect.Type, def reflect.Value) map[string]any {
	if _, ok := b.defs[t.Name()]; !ok {
		b.defs[t.Name()] = nil // recursion guard
		b.defs[t.Name()] = b.object(t, def)
	}
	return map[string]any{"$ref": "#/$defs/" + t.Name()}
}

func (b schemaBuilder) property(f reflect.StructField, def reflect.Value) map[string]any {
	s := b.valueSchema(f.Type, f.Tag, def)
	if f.Type.Kind() == reflect.Pointer {
		s = map[string]any{"anyOf": []any{s, map[string]any{"type": "null"}}}
	}
	if _, isRef := s["$ref"]; !isRef {
		s["title"] = title(yamlName(f))
	}
	if d := f.Tag.Get("desc"); d != "" {
		s["description"] = d
	}
	if f.Tag.Get("enum") == KindCommand {
		return s // kind: command is required, it has no default
	}
	s["default"] = plain(def.Interface())
	return s
}

func (b schemaBuilder) valueSchema(t reflect.Type, tag reflect.StructTag, def reflect.Value) map[string]any {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t {
	case reflect.TypeFor[JevEnabled]():
		return map[string]any{"enum": []any{"auto", true, false}}
	case reflect.TypeFor[Terminal]():
		return map[string]any{"anyOf": []any{
			map[string]any{"enum": TerminalNames, "type": "string"},
			map[string]any{
				"type": "array", "minItems": 1,
				"items":    map[string]any{"minLength": 1, "type": "string"},
				"contains": map[string]any{"pattern": `\{cmd\}`, "type": "string"},
			},
		}}
	case reflect.TypeFor[Classifiers]():
		jev := b.ref(reflect.TypeFor[JevClassifier](), reflect.ValueOf(DefaultJev().Jev))
		cmd := b.ref(reflect.TypeFor[CommandClassifier](), reflect.ValueOf(defaultCommand()))
		return map[string]any{
			"type":          "object",
			"propertyNames": map[string]any{"minLength": 1},
			"additionalProperties": map[string]any{
				"oneOf": []any{jev, cmd},
			},
		}
	}
	s := map[string]any{}
	switch t.Kind() {
	case reflect.Struct:
		return b.ref(t, def)
	case reflect.String:
		s["type"] = "string"
		if enum := strings.Split(tag.Get("enum"), ","); tag.Get("enum") != "" {
			if len(enum) == 1 {
				s["const"] = enum[0]
			} else {
				s["enum"] = enum
			}
		}
		if n := tag.Get("minlen"); n != "" {
			s["minLength"] = atoi(n)
		}
	case reflect.Bool:
		s["type"] = "boolean"
	case reflect.Int, reflect.Float64:
		s["type"] = "number"
		if t.Kind() == reflect.Int {
			s["type"] = "integer"
		}
		for _, k := range [][2]string{{"min", "minimum"}, {"xmin", "exclusiveMinimum"}, {"max", "maximum"}} {
			if n := tag.Get(k[0]); n != "" {
				s[k[1]] = atoi(n)
			}
		}
	case reflect.Slice:
		item := map[string]any{"type": "string"}
		if enum := tag.Get("enum"); enum != "" {
			item["enum"] = strings.Split(enum, ",")
		}
		if n := tag.Get("itemminlen"); n != "" {
			item["minLength"] = atoi(n)
		}
		s["type"] = "array"
		s["items"] = item
		if n := tag.Get("minlen"); n != "" {
			s["minItems"] = atoi(n)
		}
		if tag.Get("unique") == "true" {
			s["uniqueItems"] = true
		}
	}
	return s
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		panic(fmt.Sprintf("bad numeric tag %q", s))
	}
	return n
}
