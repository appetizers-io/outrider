package config

import (
	"reflect"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// commandExample is the commented-out local classifier in `config generate`.
func commandExample() CommandClassifier {
	c := defaultCommand()
	c.LaunchCommand = new("~/bin/pr-classifier launch")
	c.HookCommand = new("~/bin/pr-classifier hook")
	return c
}

// Generate is a complete config: every key at its default, documented in comments.
func Generate() string {
	header := []string{
		"# yaml-language-server: $schema=./config.schema.json",
		"#",
		"# llm-review-agent configuration, generated with every default and option.",
		"# Every key is optional: delete what you don't change. Command-line flags",
		"# override this file. Validate with `llm-review-agent config check`.",
		"#",
		"# Location: --config, $" + EnvVar + ", or " + DefaultPath(),
		"",
	}
	lines := slices.Concat(header, emit(reflect.ValueOf(Default()), ""))
	return strings.Join(lines, "\n") + "\n"
}

// wrap is Python's textwrap.wrap: whitespace collapsed, greedy lines.
func wrap(text string, width int) []string {
	var lines []string
	line := ""
	for _, w := range strings.Fields(text) {
		switch {
		case line == "":
			line = w
		case len(line)+1+len(w) <= width:
			line += " " + w
		default:
			lines = append(lines, line)
			line = w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func comments(text, pad string) []string {
	var out []string
	for _, l := range wrap(text, max(40, 78-len(pad))) {
		out = append(out, pad+"# "+l)
	}
	return out
}

func firstParagraph(s string) string { return strings.Split(s, "\n\n")[0] }

// allowed describes the values a field takes, e.g. "one of: ask, never, allow, null".
func allowed(f reflect.StructField) string {
	null := f.Type.Kind() == reflect.Pointer
	t := f.Type
	if null {
		t = t.Elem()
	}
	enum := f.Tag.Get("enum")
	switch {
	case t == reflect.TypeFor[JevEnabled]():
		return "one of: auto, true, false"
	case t == reflect.TypeFor[Terminal]():
		return "one of: " + strings.Join(TerminalNames, ", ") + "; or a command list with " + Placeholder
	case enum != "" && t.Kind() == reflect.Slice:
		return "list of: " + strings.ReplaceAll(enum, ",", ", ")
	case enum != "":
		s := "one of: " + strings.ReplaceAll(enum, ",", ", ")
		if null {
			s += ", null"
		}
		return s
	case null:
		return "or null"
	}
	return ""
}

func limits(f reflect.StructField) string {
	var parts []string
	for _, l := range [][2]string{{"min", ">="}, {"xmin", ">"}, {"max", "<="}} {
		if n := f.Tag.Get(l[0]); n != "" {
			parts = append(parts, l[1]+" "+n)
		}
	}
	if n := f.Tag.Get("minlen"); n != "" && f.Type.Kind() == reflect.Slice {
		parts = append(parts, "at least "+n+" item(s)")
	}
	return strings.Join(parts, ", ")
}

// scalar is a value on one line, quoted exactly when YAML needs it.
func scalar(v any) string {
	out, err := yaml.Marshal(v)
	if err != nil {
		panic(err)
	}
	s := strings.TrimSpace(string(out))
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Slice {
		items := make([]string, rv.Len())
		for i := range rv.Len() {
			items[i] = scalar(rv.Index(i).Interface())
		}
		return "[" + strings.Join(items, ", ") + "]"
	}
	return s
}

func isSection(t reflect.Type) bool {
	return t.Kind() == reflect.Struct && t != reflect.TypeFor[Terminal]()
}

func emit(v reflect.Value, pad string) []string {
	var out []string
	t := v.Type()
	for i := range t.NumField() {
		f, fv := t.Field(i), v.Field(i)
		name := yamlName(f)
		section := isSection(f.Type)
		doc := f.Tag.Get("desc")
		if doc == "" && section {
			doc = firstParagraph(docs[f.Type])
		}
		if pad == "" && len(out) > 0 {
			out = append(out, "")
		}
		if doc != "" {
			out = append(out, comments(doc, pad)...)
		}
		var hints []string
		for _, h := range []string{allowed(f), limits(f)} {
			if h != "" {
				hints = append(hints, h)
			}
		}
		if len(hints) > 0 && !section {
			out = append(out, pad+"# ("+strings.Join(hints, "; ")+")")
		}
		switch {
		case f.Type == reflect.TypeFor[Classifiers]():
			out = append(out, pad+name+":")
			cs := fv.Interface().(Classifiers)
			for _, key := range cs.Names() {
				c := cs[key]
				kind := reflect.ValueOf(c.Jev)
				if c.Kind == KindCommand {
					kind = reflect.ValueOf(c.Command)
				}
				out = append(out, comments(firstParagraph(docs[kind.Type()]), pad+"  ")...)
				out = append(out, pad+"  "+key+":")
				out = append(out, emit(kind, pad+"    ")...)
			}
			out = append(out, comments(docs[reflect.TypeFor[CommandClassifier]()], pad+"  ")...)
			out = append(out, pad+"  # local:")
			for _, line := range emit(reflect.ValueOf(commandExample()), pad+"    ") {
				// comment out the example, keeping its nesting readable
				out = append(out, pad+"  # "+line[len(pad)+2:])
			}
		case section:
			out = append(out, pad+name+":")
			out = append(out, emit(fv, pad+"  ")...)
		default:
			val := fv.Interface()
			if fv.Kind() == reflect.Pointer {
				val = nil
				if !fv.IsNil() {
					val = fv.Elem().Interface()
				}
			}
			out = append(out, pad+name+": "+scalar(val))
		}
	}
	return out
}
