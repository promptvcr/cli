// Package credentials persists the cloud login (Supabase URL, anon/publishable
// key, and PromptVCR access token) under ~/.promptvcr/credentials.json so the
// cloud commands (push/pull/cloud-ls) can authenticate without env vars.
package credentials

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/promptvcr/cli/internal/config"
)

// Credentials is the on-disk schema for credentials.json.
type Credentials struct {
	URL    string `json:"url"`
	APIKey string `json:"apiKey"`
	Token  string `json:"token"`
}

// Path returns the location of the credentials file (~/.promptvcr/credentials.json).
func Path() string {
	return filepath.Join(config.Dir(), "credentials.json")
}

// Load reads stored credentials. A missing file returns a zero-value
// Credentials with no error so callers can detect "not logged in" via fields.
func Load() (Credentials, error) {
	var c Credentials
	b, err := os.ReadFile(Path())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return c, nil
		}
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	return c, nil
}

// Save writes credentials to disk, creating the config directory (0700) and the
// file (0600) so the token is not world-readable.
func Save(c Credentials) error {
	dir := config.Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(), b, 0o600)
}

// Clear removes the credentials file. A missing file is not an error.
func Clear() error {
	if err := os.Remove(Path()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
