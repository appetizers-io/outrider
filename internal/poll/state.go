package poll

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Candidate is a non-owned PR that may still be opted in.
type Candidate struct {
	PR     int     `json:"pr"`
	Repo   string  `json:"repo"`
	SeenAt float64 `json:"seen_at"` // unix seconds; older state files may lack it
}

// Watched is an opted-in PR, relaunched when its reviews and comments change.
type Watched struct {
	Fingerprint *string `json:"fingerprint"`
	PR          int     `json:"pr"`
	Repo        string  `json:"repo"`
}

// State is kept between polls. Keys are owner/repo#n; fields are sorted like
// the Python version wrote them.
type State struct {
	Candidates  map[string]Candidate `json:"candidates"`
	Handled     map[string]string    `json:"handled"` // when activity was last judged
	Initialized bool                 `json:"initialized"`
	Mentions    map[string][]string  `json:"mentions"` // handled "kind:id"
	Replies     map[string][]int64   `json:"replies"`  // handled reply ids
	Seen        map[string]string    `json:"seen"`     // notification id -> updated_at
	Watched     map[string]Watched   `json:"watched"`
}

func (s *State) fill() {
	if s.Candidates == nil {
		s.Candidates = map[string]Candidate{}
	}
	if s.Handled == nil {
		s.Handled = map[string]string{}
	}
	if s.Mentions == nil {
		s.Mentions = map[string][]string{}
	}
	if s.Replies == nil {
		s.Replies = map[string][]int64{}
	}
	if s.Seen == nil {
		s.Seen = map[string]string{}
	}
	if s.Watched == nil {
		s.Watched = map[string]Watched{}
	}
}

// LoadState reads the state; a missing file is an empty state.
func LoadState(path string) (*State, error) {
	s := &State{}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.fill()
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("state %s: %w", path, err)
	}
	if _, ok := probe["seen"]; !ok {
		// migrate older prototype state: a flat id -> updated_at map
		old := raw
		if n, ok := probe["notifications"]; ok {
			old = n
		}
		if err := json.Unmarshal(old, &s.Seen); err != nil {
			return nil, fmt.Errorf("migrate state %s: %w", path, err)
		}
		s.fill()
		return s, nil
	}
	if err := json.Unmarshal(raw, s); err != nil {
		return nil, fmt.Errorf("state %s: %w", path, err)
	}
	s.fill()
	return s, nil
}

// Save writes the state atomically.
func (s *State) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("state dir: %w", err)
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	tmp := path[:len(path)-len(filepath.Ext(path))] + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	return nil
}
