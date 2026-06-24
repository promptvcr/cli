// Package config holds CLI configuration and provider host detection.
package config

import (
	"os"
	"path/filepath"
	"strings"
)

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
