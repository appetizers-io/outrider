package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"strings"
	"sync"

	"github.com/invopop/jsonschema"
	validator "github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

// Schema is the JSON Schema of the config file, generated from the structs.
func Schema() *jsonschema.Schema {
	r := &jsonschema.Reflector{FieldNameTag: "yaml", RequiredFromJSONSchemaTags: true, ExpandedStruct: true}
	s := r.Reflect(&Config{})
	s.ID = SchemaID
	s.Title = "outrider configuration"
	s.Description = "Every key is optional; command-line flags override this file."
	// Classifier.JSONSchema refers to the two kinds
	for name, kind := range map[string]any{"JevClassifier": &JevClassifier{}, "CommandClassifier": &CommandClassifier{}} {
		d := r.Reflect(kind)
		maps.Copy(s.Definitions, d.Definitions)
		d.Version, d.ID, d.Definitions = "", "", nil
		s.Definitions[name] = d
	}
	return s
}

// SchemaText is Schema as indented JSON.
func SchemaText() string {
	b, err := json.MarshalIndent(Schema(), "", "  ")
	if err != nil {
		panic(err) // the schema always encodes
	}
	return string(b) + "\n"
}

// compiled is the schema the loader validates with.
var compiled = sync.OnceValue(func() *validator.Schema {
	doc, err := validator.UnmarshalJSON(strings.NewReader(SchemaText()))
	if err != nil {
		panic(err)
	}
	c := validator.NewCompiler()
	if err := c.AddResource(SchemaID, doc); err != nil {
		panic(err)
	}
	return c.MustCompile(SchemaID)
})

// Parse validates YAML config data against the schema and decodes it onto
// the defaults; source names it in errors.
func Parse(data []byte, source string) (Config, error) {
	invalid := func(msg string) error { return &Error{source + " is invalid:\n" + msg} }
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return Config{}, &Error{fmt.Sprintf("config %s is not valid YAML:\n%v", source, err)}
	}
	if len(doc.Content) == 0 || doc.Content[0].Tag == "!!null" {
		return Default(), nil // empty file: the defaults
	}

	var data0 any
	if err := doc.Decode(&data0); err != nil {
		return Config{}, invalid(err.Error())
	}
	raw, err := json.Marshal(data0)
	if err != nil {
		return Config{}, invalid(err.Error())
	}
	value, err := validator.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return Config{}, invalid(err.Error())
	}
	if err := compiled().Validate(value); err != nil {
		var ve *validator.ValidationError
		if errors.As(err, &ve) {
			// drop the first line, which only names the schema
			_, detail, _ := strings.Cut(ve.Error(), "\n")
			return Config{}, invalid(detail)
		}
		return Config{}, invalid(err.Error())
	}

	normalized, err := yaml.Marshal(&doc)
	if err != nil {
		return Config{}, invalid(err.Error())
	}
	cfg := Default()
	dec := yaml.NewDecoder(bytes.NewReader(normalized))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, invalid(err.Error())
	}
	if errs := cfg.crossCheck(); len(errs) > 0 {
		return Config{}, invalid("- " + strings.Join(errs, "\n- "))
	}
	return cfg, nil
}

// Validate checks an already built config (e.g. with flags applied) against
// the schema, the same way a file is checked.
func Validate(c Config, source string) (Config, error) {
	data, err := yaml.Marshal(c)
	if err != nil {
		return Config{}, fmt.Errorf("encode config: %w", err)
	}
	return Parse(data, source)
}
