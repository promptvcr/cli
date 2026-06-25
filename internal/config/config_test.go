package config

import (
	"path/filepath"
	"testing"
)

func TestProviderFor(t *testing.T) {
	cases := []struct {
		host string
		want string
	}{
		{"api.openai.com", "openai"},
		{"API.OpenAI.com", "openai"},
		{"api.openai.com:443", "openai"},
		{"api.anthropic.com", "anthropic"},
		{"generativelanguage.googleapis.com", "gemini"},
		{"openrouter.ai", "openrouter"},
		{"localhost:11434", "ollama"},
		{"127.0.0.1:11434", "ollama"},
		{"example.com", ""},
		{"api.notllm.com:443", ""},
	}
	for _, c := range cases {
		if got := ProviderFor(c.host); got != c.want {
			t.Errorf("ProviderFor(%q) = %q, want %q", c.host, got, c.want)
		}
	}
}

func TestDirHonorsEnv(t *testing.T) {
	t.Setenv("PROMPTVCR_HOME", filepath.Join("tmp", "pvcr"))
	if got := Dir(); got != filepath.Join("tmp", "pvcr") {
		t.Errorf("Dir() = %q, want override", got)
	}
}

func TestFixturesDir(t *testing.T) {
	t.Setenv("PROMPTVCR_FIXTURES", "")
	if got := FixturesDir(); got != "fixtures" {
		t.Errorf("FixturesDir() default = %q, want %q", got, "fixtures")
	}
	t.Setenv("PROMPTVCR_FIXTURES", "custom/fx")
	if got := FixturesDir(); got != "custom/fx" {
		t.Errorf("FixturesDir() = %q, want override", got)
	}
}
