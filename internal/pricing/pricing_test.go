package pricing

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPerCallCents(t *testing.T) {
	tbl := Default()
	cases := []struct {
		name, provider, model string
		want                  float64
	}{
		{"exact model", "openai", "gpt-4o", 1.0},
		{"dated model resolves to family", "openai", "gpt-4o-2024-08-06", 1.0},
		{"longest prefix wins", "openai", "gpt-4o-mini", 0.05},
		{"anthropic family", "anthropic", "claude-3-5-sonnet-latest", 1.5},
		{"unknown model falls back to provider", "openai", "some-future-model", 1.0},
		{"empty model uses provider", "gemini", "", 0.5},
		{"ollama is free", "ollama", "llama3", 0.0},
		{"unknown provider and model is zero", "mystery", "who-knows", 0.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tbl.PerCallCents(tc.provider, tc.model); got != tc.want {
				t.Errorf("PerCallCents(%q,%q) = %v, want %v", tc.provider, tc.model, got, tc.want)
			}
		})
	}
}

func TestLoadOverride(t *testing.T) {
	dir := t.TempDir()
	body := `{"models":{"gpt-4o":2.5,"brand-new":9},"providers":{"openai":1.25}}`
	if err := os.WriteFile(filepath.Join(dir, "pricing.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	tbl := Load(dir)
	if got := tbl.PerCallCents("openai", "gpt-4o"); got != 2.5 {
		t.Errorf("override model gpt-4o = %v, want 2.5", got)
	}
	if got := tbl.PerCallCents("openai", "brand-new"); got != 9 {
		t.Errorf("override new model = %v, want 9", got)
	}
	if got := tbl.PerCallCents("openai", "unknown"); got != 1.25 {
		t.Errorf("override provider fallback = %v, want 1.25", got)
	}
	// Untouched defaults still apply.
	if got := tbl.PerCallCents("anthropic", "claude-3-opus"); got != 4.0 {
		t.Errorf("default anthropic = %v, want 4.0", got)
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	tbl := Load(t.TempDir())
	if got := tbl.PerCallCents("openai", "gpt-4o"); got != 1.0 {
		t.Errorf("missing override should use defaults, got %v", got)
	}
}
