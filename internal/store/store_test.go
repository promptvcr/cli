package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func sample(key, provider string) *Record {
	return &Record{
		Key:      key,
		Provider: provider,
		Model:    "gpt-5.2",
		Request: Request{
			Method: "POST",
			Host:   "api.openai.com",
			Path:   "/v1/chat/completions",
			Body:   json.RawMessage(`{"model":"gpt-5.2"}`),
		},
		Response: Response{
			Status:  200,
			Headers: map[string][]string{"Content-Type": {"text/event-stream"}},
			Stream:  []Chunk{{OffsetMs: 100, Data: `{"choices":[]}`}},
		},
		TTFTMs:     100,
		RecordedAt: "2026-06-24T00:00:00Z",
	}
}

func TestPutGetRoundtrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	rec := sample("abc123", "openai")
	if err := s.Put(rec); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok := s.Get("openai", "abc123")
	if !ok {
		t.Fatal("Get returned not found after Put")
	}
	if got.Key != rec.Key || got.Provider != rec.Provider || got.Response.Status != 200 {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
	if len(got.Response.Stream) != 1 || got.Response.Stream[0].OffsetMs != 100 {
		t.Errorf("stream not preserved: %+v", got.Response.Stream)
	}
}

func TestGetMissing(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, ok := s.Get("openai", "nope"); ok {
		t.Error("Get returned ok for a missing key")
	}
}

func TestPutIsAtomicNoTempLeft(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Put(sample("k", "openai")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// No .tmp file should remain after an atomic rename.
	if _, err := os.Stat(filepath.Join(dir, "openai", "k.json.tmp")); !os.IsNotExist(err) {
		t.Error("temp file left behind after Put")
	}
}

func TestList(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Put(sample("k1", "openai")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Put(sample("k2", "anthropic")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	recs, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(recs) != 2 {
		t.Errorf("List returned %d records, want 2", len(recs))
	}
}
