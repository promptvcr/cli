// Package config holds CLI configuration and provider host detection.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// DefaultStaleDays is the cassette age (in days) past which `ls`/`stats` flag a
// cassette as stale, unless overridden by config or a flag.
const DefaultStaleDays = 30

// RedactRules configures value masking before cassettes are written.
type RedactRules struct {
	JSONPaths   []string `json:"jsonPaths,omitempty"`
	Patterns    []string `json:"patterns,omitempty"`
	ReplaceWith string   `json:"replaceWith,omitempty"`
}

// File is the on-disk schema for .promptvcr.json / ~/.promptvcr/config.json.
type File struct {
	IgnorePaths []string    `json:"ignorePaths,omitempty"`
	StaleDays   int         `json:"staleDays,omitempty"`
	Redact      RedactRules `json:"redact,omitempty"`
}

// Load reads configuration, applying ~/.promptvcr/config.json first and then
// ./.promptvcr.json on top (project settings win, field by field). Missing or
// malformed files are ignored. StaleDays defaults to DefaultStaleDays.
func Load() File {
	cfg := File{StaleDays: DefaultStaleDays}
	apply := func(path string) {
		b, err := os.ReadFile(path)
		if err != nil {
			return
		}
		_ = json.Unmarshal(b, &cfg) // unmarshal overlays present fields onto cfg
	}
	apply(filepath.Join(Dir(), "config.json"))
	apply(".promptvcr.json")
	if cfg.StaleDays <= 0 {
		cfg.StaleDays = DefaultStaleDays
	}
	return cfg
}

// Mode controls proxy behavior.
type Mode string

const (
	ModeRecord Mode = "record" // always hit live + record
	ModeReplay Mode = "replay" // replay only; a miss is an error (CI default)
	ModeAuto   Mode = "auto"   // replay on hit, record on miss (local dev default)
)

// llmHosts maps known provider API hosts to a provider id.
var llmHosts = map[string]string{
	"api.openai.com":                    "openai",
	"api.anthropic.com":                 "anthropic",
	"generativelanguage.googleapis.com": "gemini",
	"openrouter.ai":                     "openrouter",
	"localhost:11434":                   "ollama",
	"127.0.0.1:11434":                   "ollama",
}

// ProviderFor returns the provider id for a host, or "" if not an LLM host.
func ProviderFor(host string) string {
	host = strings.ToLower(host)
	if p, ok := llmHosts[host]; ok {
		return p
	}
	// Strip port for matching (e.g. api.openai.com:443).
	if i := strings.IndexByte(host, ':'); i >= 0 {
		if p, ok := llmHosts[host[:i]]; ok {
			return p
		}
	}
	return ""
}

// Dir is the PromptVCR config/state directory (~/.promptvcr).
func Dir() string {
	if d := os.Getenv("PROMPTVCR_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".promptvcr"
	}
	return filepath.Join(home, ".promptvcr")
}

// FixturesDir is where cassettes are written (defaults to ./fixtures).
func FixturesDir() string {
	if d := os.Getenv("PROMPTVCR_FIXTURES"); d != "" {
		return d
	}
	return "fixtures"
}
