// Package pricing provides rough per-call cost estimates used to show how much
// money replaying cassettes saves. The number of replayed calls is exact; the
// dollar figure is an ESTIMATE: a flat average cost per call per model, since we
// deliberately do not parse token counts out of request/response bodies.
//
// Users can override or extend the table with ~/.promptvcr/pricing.json:
//
//	{
//	  "models":    { "gpt-4o": 1.2, "claude-3-5-sonnet": 1.5 },
//	  "providers": { "openai": 1.0 }
//	}
//
// Values are cents per call.
package pricing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Table holds cost estimates. It is read-only after construction and safe for
// concurrent use.
type Table struct {
	models    map[string]float64
	providers map[string]float64
}

// defaultModelCents are estimated average costs per call (in US cents) for a
// typical short/medium request. Keys are matched by longest-prefix so that
// dated model ids (e.g. "gpt-4o-2024-08-06") resolve to their family.
var defaultModelCents = map[string]float64{
	// OpenAI
	"gpt-4o-mini":   0.05,
	"gpt-4o":        1.0,
	"gpt-4-turbo":   3.0,
	"gpt-4":         4.0,
	"gpt-3.5-turbo": 0.1,
	"o1-mini":       1.5,
	"o1":            6.0,
	"o3-mini":       1.5,
	// Anthropic
	"claude-3-5-haiku":  0.1,
	"claude-3-5-sonnet": 1.5,
	"claude-3-haiku":    0.1,
	"claude-3-sonnet":   1.5,
	"claude-3-opus":     4.0,
	// Google Gemini
	"gemini-1.5-flash": 0.05,
	"gemini-1.5-pro":   1.0,
	"gemini-2.0-flash": 0.05,
	"gemini-pro":       0.5,
}

// defaultProviderCents is the fallback when the model is unknown or absent.
// Ollama is local and effectively free.
var defaultProviderCents = map[string]float64{
	"openai":     1.0,
	"anthropic":  1.5,
	"gemini":     0.5,
	"openrouter": 1.0,
	"ollama":     0.0,
}

// Default returns the built-in estimate table.
func Default() *Table {
	m := make(map[string]float64, len(defaultModelCents))
	for k, v := range defaultModelCents {
		m[k] = v
	}
	p := make(map[string]float64, len(defaultProviderCents))
	for k, v := range defaultProviderCents {
		p[k] = v
	}
	return &Table{models: m, providers: p}
}

type overrideFile struct {
	Models    map[string]float64 `json:"models"`
	Providers map[string]float64 `json:"providers"`
}

// Load returns the default table with any overrides from dir/pricing.json
// applied. A missing or malformed file is ignored (defaults are used).
func Load(dir string) *Table {
	t := Default()
	b, err := os.ReadFile(filepath.Join(dir, "pricing.json"))
	if err != nil {
		return t
	}
	var o overrideFile
	if json.Unmarshal(b, &o) != nil {
		return t
	}
	for k, v := range o.Models {
		t.models[strings.ToLower(k)] = v
	}
	for k, v := range o.Providers {
		t.providers[strings.ToLower(k)] = v
	}
	return t
}

// PerCallCents estimates the cost of a single call. It prefers a longest-prefix
// model match, then falls back to the provider, then 0.
func (t *Table) PerCallCents(provider, model string) float64 {
	model = strings.ToLower(strings.TrimSpace(model))
	if model != "" {
		bestLen := -1
		best := 0.0
		for k, v := range t.models {
			if strings.HasPrefix(model, k) && len(k) > bestLen {
				bestLen, best = len(k), v
			}
		}
		if bestLen >= 0 {
			return best
		}
	}
	if v, ok := t.providers[strings.ToLower(provider)]; ok {
		return v
	}
	return 0
}
