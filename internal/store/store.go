// Package store persists and retrieves recorded interactions ("cassettes").
//
// Each interaction is stored as a single JSON file named by its cache key under
// fixtures/<provider>/<key>.json. Secrets are redacted before anything is written.
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Chunk is one recorded SSE/NDJSON event with its offset from the first byte.
type Chunk struct {
	OffsetMs int64  `json:"offsetMs"`
	Data     string `json:"data"`
}

// Response is a recorded response (streaming or not).
type Response struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    json.RawMessage     `json:"body,omitempty"`
	Stream  []Chunk             `json:"stream,omitempty"`
}

// Request is the redacted, recorded request.
type Request struct {
	Method string          `json:"method"`
	Host   string          `json:"host"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body,omitempty"`
}

// Record is a full recorded interaction.
type Record struct {
	Key        string   `json:"key"`
	Provider   string   `json:"provider"`
	Model      string   `json:"model,omitempty"`
	Request    Request  `json:"request"`
	Response   Response `json:"response"`
	TTFTMs     int64    `json:"ttftMs,omitempty"`
	RecordedAt string   `json:"recordedAt"`
}

// Store reads/writes cassettes under a root directory.
type Store struct {
	root        string
	IgnorePaths []string
	mu          sync.Mutex
}

// Open returns a Store rooted at dir, creating it if necessary.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{root: dir}, nil
}

func (s *Store) pathFor(provider, key string) string {
	return filepath.Join(s.root, provider, key+".json")
}

// Get returns the recorded interaction for a key, if present.
func (s *Store) Get(provider, key string) (*Record, bool) {
	b, err := os.ReadFile(s.pathFor(provider, key))
	if err != nil {
		return nil, false
	}
	var rec Record
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, false
	}
	return &rec, true
}

// Put writes a recorded interaction atomically.
func (s *Store) Put(rec *Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p := s.pathFor(rec.Provider, rec.Key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// List returns all records across providers (used by `ls` and `push`).
func (s *Store) List() ([]*Record, error) {
	var out []*Record
	err := filepath.Walk(s.root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var rec Record
		if json.Unmarshal(b, &rec) == nil {
			out = append(out, &rec)
		}
		return nil
	})
	return out, err
}
