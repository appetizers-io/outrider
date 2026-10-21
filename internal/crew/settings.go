package crew

import (
	"errors"
	"fmt"

	"go.yaml.in/yaml/v3"

	"github.com/appetizers-io/outrider/internal/config"
)

// ConvertSettings validates repository task settings and returns a normalized draft.
// It never loads host configuration or writes settings.
func ConvertSettings(raw []byte) (map[string]any, error) {
	if len(raw) > 131072 {
		return nil, errors.New("repository settings exceed 128 KiB")
	}
	var settings map[string]any
	if err := yaml.Unmarshal(raw, &settings); err != nil {
		return nil, err
	}
	if settings == nil {
		settings = map[string]any{}
	}
	allowed := map[string]bool{"triggers": true, "ignore_authors": true, "launch_check": true, "prompts": true, "workflows": true, "overrides": true, "others_prs": true, "push": true, "github_writes": true}
	if err := validateCrewTaskPolicy(settings); err != nil {
		return nil, err
	}
	for key := range settings {
		if !allowed[key] {
			return nil, fmt.Errorf("unsupported repository setting %q; session settings belong to Crew", key)
		}
	}
	if entries, exists := settings["overrides"]; exists {
		overrides, ok := entries.([]any)
		if !ok {
			return nil, errors.New("overrides must be a list")
		}
		for _, entry := range overrides {
			layer, ok := entry.(map[string]any)
			if !ok {
				return nil, errors.New("override must be a mapping")
			}
			if err := validateCrewTaskPolicy(layer); err != nil {
				return nil, err
			}
			for key := range layer {
				if key != "match" && (!allowed[key] || key == "overrides") {
					return nil, fmt.Errorf("unsupported ownership setting %q", key)
				}
			}
			matches, ok := layer["match"].([]any)
			if !ok || len(matches) == 0 {
				return nil, errors.New("ownership override needs a match")
			}
			for _, entry := range matches {
				match, ok := entry.(map[string]any)
				if !ok || len(match) != 1 || (match["prs"] != "own" && match["prs"] != "others") {
					return nil, errors.New("repository ownership overrides match only prs: own or others")
				}
			}
		}
	}
	encoded, err := yaml.Marshal(settings)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Parse(encoded, "repository settings")
	if err != nil {
		return nil, err
	}
	// Show the same effective trigger values as the assisted form.
	triggerData, err := yaml.Marshal(cfg.Triggers)
	if err != nil {
		return nil, err
	}
	var triggers map[string]any
	if err := yaml.Unmarshal(triggerData, &triggers); err != nil {
		return nil, err
	}
	settings["triggers"] = triggers
	encoded, err = yaml.Marshal(settings)
	if err != nil {
		return nil, err
	}
	return map[string]any{"settings": settings, "yaml": string(encoded)}, nil
}

func validateCrewTaskPolicy(layer map[string]any) error {
	if value, exists := layer["others_prs"]; exists {
		others, ok := value.(map[string]any)
		if !ok {
			return errors.New("others_prs must be a mapping")
		}
		for key := range others {
			if key != "allow_push" && key != "review_forks" {
				return fmt.Errorf("unsupported Crew others_prs setting %q", key)
			}
		}
	}
	return nil
}
