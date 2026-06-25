package credentials

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadClear(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PROMPTVCR_HOME", home)

	// Loading with no file yet returns a zero value and no error.
	if c, err := Load(); err != nil || c.Token != "" {
		t.Fatalf("Load() on empty = (%+v, %v), want zero/no error", c, err)
	}

	want := Credentials{
		URL:    "https://ref.supabase.co",
		APIKey: "anon-key",
		Token:  "pvcr_deadbeef",
	}
	if err := Save(want); err != nil {
		t.Fatalf("Save() = %v", err)
	}

	if Path() != filepath.Join(home, "credentials.json") {
		t.Errorf("Path() = %q, want under PROMPTVCR_HOME", Path())
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if got != want {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}

	// The file should be created with 0600 perms (skip on Windows where the
	// permission bits are not faithfully represented).
	if info, err := os.Stat(Path()); err == nil && os.PathSeparator == '/' {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("file perm = %o, want 600", perm)
		}
	}

	if err := Clear(); err != nil {
		t.Fatalf("Clear() = %v", err)
	}
	if _, err := os.Stat(Path()); !os.IsNotExist(err) {
		t.Errorf("after Clear(), file still exists: %v", err)
	}

	// Clear() on an already-absent file is a no-op.
	if err := Clear(); err != nil {
		t.Errorf("Clear() on missing file = %v, want nil", err)
	}
}
