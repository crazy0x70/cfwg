package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStore_SaveCreatesSecureStateFile(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := store.Save(State{SchemaVersion: 1}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600, got %o", info.Mode().Perm())
	}
}

func TestStore_SaveTightensExistingStateFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewStore(dir)
	if err := store.Save(State{SchemaVersion: 2, DeviceID: "device-1"}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected tightened permissions 0600, got %o", info.Mode().Perm())
	}
}
