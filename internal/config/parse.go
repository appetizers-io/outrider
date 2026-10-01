package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// Parse validates YAML config data; source names it in errors.
func Parse(data []byte, source string) (Config, error) {
	cfg := Default()
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return Config{}, &Error{fmt.Sprintf("config %s is not valid YAML:\n%v", source, err)}
	}
	if len(doc.Content) == 0 || doc.Content[0].Tag == "!!null" {
		return cfg, nil // empty file: the defaults
	}
	invalid := func(errs []string) error {
		return &Error{source + " is invalid:\n  " + strings.Join(errs, "\n  ")}
	}
	// YAML is lenient (3 becomes "3", yes becomes true); the config is not.
	if errs := checkNode(doc.Content[0], reflect.TypeFor[Config](), ""); len(errs) > 0 {
		return Config{}, invalid(errs)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, invalid([]string{err.Error()})
	}
	errs := validate(reflect.ValueOf(cfg), "")
	if len(errs) == 0 {
		errs = cfg.crossCheck()
	}
	if len(errs) > 0 {
		return Config{}, invalid(errs)
	}
	return cfg, nil
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func at(path, msg string) string {
	if path == "" {
		return msg
	}
	return path + ": " + msg
}

// yaml11Bools are booleans in YAML 1.1, which the Python version read as such.
var yaml11Bools = []string{"yes", "Yes", "YES", "no", "No", "NO", "on", "On", "ON", "off", "Off", "OFF"}

func isBool(n *yaml.Node) bool {
	return n.Tag == "!!bool" || (n.Tag == "!!str" && n.Style == 0 && slices.Contains(yaml11Bools, n.Value))
}

// fieldByKey maps YAML keys to struct fields.
func fieldByKey(t reflect.Type, key string) (reflect.StructField, bool) {
	for f := range t.Fields() {
		if strings.Split(f.Tag.Get("yaml"), ",")[0] == key {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

// checkNode reports values whose YAML type does not match the field type,
// and unknown keys.
func checkNode(n *yaml.Node, t reflect.Type, path string) []string {
	if n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	if t.Kind() == reflect.Pointer {
		if n.Tag == "!!null" {
			return nil
		}
		t = t.Elem()
	}
	switch t {
	case reflect.TypeFor[JevEnabled]():
		if isBool(n) || (n.Tag == "!!str" && n.Value == "auto") {
			return nil
		}
		return []string{at(path, "Input should be 'auto', True or False")}
	case reflect.TypeFor[Terminal]():
		if n.Kind == yaml.SequenceNode {
			return checkNode(n, reflect.TypeFor[[]string](), path)
		}
		return checkNode(n, reflect.TypeFor[string](), path)
	case reflect.TypeFor[Classifier]():
		if n.Kind != yaml.MappingNode {
			return []string{at(path, "Input should be a valid dictionary")}
		}
		switch nodeKind(n) {
		case KindJev:
			return checkNode(n, reflect.TypeFor[JevClassifier](), path)
		case KindCommand:
			return checkNode(n, reflect.TypeFor[CommandClassifier](), path)
		}
		return []string{at(path, "kind must be 'jev' or 'command'")}
	}
	switch t.Kind() {
	case reflect.Struct:
		if n.Kind != yaml.MappingNode {
			return []string{at(path, "Input should be a valid dictionary")}
		}
		var errs []string
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i].Value
			f, ok := fieldByKey(t, key)
			if !ok {
				errs = append(errs, at(join(path, key), "Extra inputs are not permitted"))
				continue
			}
			errs = append(errs, checkNode(n.Content[i+1], f.Type, join(path, key))...)
		}
		return errs
	case reflect.Map:
		if n.Kind != yaml.MappingNode {
			return []string{at(path, "Input should be a valid dictionary")}
		}
		var errs []string
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i].Value
			if n.Content[i].Tag != "!!str" || key == "" {
				errs = append(errs, at(join(path, key), "Keys should be non-empty strings"))
				continue
			}
			errs = append(errs, checkNode(n.Content[i+1], t.Elem(), join(path, key))...)
		}
		return errs
	case reflect.Slice:
		if n.Kind != yaml.SequenceNode {
			return []string{at(path, "Input should be a valid list")}
		}
		var errs []string
		for i, item := range n.Content {
			errs = append(errs, checkNode(item, t.Elem(), join(path, strconv.Itoa(i)))...)
		}
		return errs
	case reflect.String:
		if n.Kind == yaml.ScalarNode && n.Tag == "!!str" && !isBool(n) {
			return nil
		}
		return []string{at(path, "Input should be a valid string")}
	case reflect.Bool:
		if n.Kind == yaml.ScalarNode && isBool(n) {
			return nil
		}
		return []string{at(path, "Input should be a valid boolean")}
	case reflect.Int:
		if n.Kind == yaml.ScalarNode && n.Tag == "!!int" {
			return nil
		}
		return []string{at(path, "Input should be a valid integer")}
	case reflect.Float64:
		if n.Kind == yaml.ScalarNode && (n.Tag == "!!int" || n.Tag == "!!float") {
			return nil
		}
		return []string{at(path, "Input should be a valid number")}
	}
	return []string{at(path, "unsupported value")}
}

// oneOf formats allowed values like pydantic: 'a', 'b' or 'c'.
func oneOf(values []string) string {
	q := make([]string, len(values))
	for i, v := range values {
		q[i] = "'" + v + "'"
	}
	if len(q) == 1 {
		return q[0]
	}
	return strings.Join(q[:len(q)-1], ", ") + " or " + q[len(q)-1]
}

// validate checks the tag constraints of every field.
func validate(v reflect.Value, path string) []string {
	if v.Type() == reflect.TypeFor[Classifiers]() {
		var errs []string
		for _, name := range v.Interface().(Classifiers).Names() {
			c := v.Interface().(Classifiers)[name]
			sub := reflect.ValueOf(c.Jev)
			if c.Kind == KindCommand {
				sub = reflect.ValueOf(c.Command)
			}
			errs = append(errs, validate(sub, join(path, name))...)
		}
		return errs
	}
	var errs []string
	for i := range v.NumField() {
		f := v.Type().Field(i)
		fv := v.Field(i)
		p := join(path, strings.Split(f.Tag.Get("yaml"), ",")[0])
		if fv.Kind() == reflect.Pointer {
			if fv.IsNil() {
				continue
			}
			fv = fv.Elem()
		}
		if fv.Kind() == reflect.Struct && f.Type != reflect.TypeFor[Terminal]() || fv.Type() == reflect.TypeFor[Classifiers]() {
			errs = append(errs, validate(fv, p)...)
			continue
		}
		errs = append(errs, checkField(f.Tag, fv, p)...)
	}
	return errs
}

func checkField(tag reflect.StructTag, v reflect.Value, path string) []string {
	var errs []string
	if enum := tag.Get("enum"); enum != "" {
		allowed := strings.Split(enum, ",")
		values := []string{}
		if v.Kind() == reflect.Slice {
			for i := range v.Len() {
				values = append(values, v.Index(i).String())
			}
		} else {
			values = append(values, v.String())
		}
		for i, s := range values {
			if !slices.Contains(allowed, s) {
				p := path
				if v.Kind() == reflect.Slice {
					p = join(path, strconv.Itoa(i))
				}
				errs = append(errs, at(p, "Input should be "+oneOf(allowed)))
			}
		}
	}
	var num float64
	switch v.Kind() {
	case reflect.Int:
		num = float64(v.Int())
	case reflect.Float64:
		num = v.Float()
	}
	for _, b := range []struct{ key, msg string }{
		{"min", "greater than or equal to"},
		{"xmin", "greater than"},
		{"max", "less than or equal to"},
	} {
		key, msg := b.key, b.msg
		limit := tag.Get(key)
		if limit == "" {
			continue
		}
		l, _ := strconv.ParseFloat(limit, 64)
		if (key == "min" && num < l) || (key == "xmin" && num <= l) || (key == "max" && num > l) {
			errs = append(errs, at(path, "Input should be "+msg+" "+limit))
		}
	}
	if minlen := tag.Get("minlen"); minlen != "" {
		l, _ := strconv.Atoi(minlen)
		switch v.Kind() {
		case reflect.String:
			if utf8.RuneCountInString(v.String()) < l {
				errs = append(errs, at(path, fmt.Sprintf("String should have at least %d character", l)))
			}
		case reflect.Slice:
			if v.Len() < l {
				errs = append(errs, at(path, fmt.Sprintf("List should have at least %d item after validation, not %d", l, v.Len())))
			}
		}
	}
	if itemMin := tag.Get("itemminlen"); itemMin != "" {
		l, _ := strconv.Atoi(itemMin)
		for i := range v.Len() {
			if utf8.RuneCountInString(v.Index(i).String()) < l {
				errs = append(errs, at(join(path, strconv.Itoa(i)), fmt.Sprintf("String should have at least %d character", l)))
			}
		}
	}
	if tag.Get("unique") == "true" {
		seen := map[string]bool{}
		for i := range v.Len() {
			s := v.Index(i).String()
			if seen[s] {
				errs = append(errs, at(path, "entries must be unique"))
				break
			}
			seen[s] = true
		}
	}
	return errs
}
