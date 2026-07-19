package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentTokenPersistsPrivately(t *testing.T) {
	directory := t.TempDir()
	first, err := loadOrCreateAgentToken(directory)
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadOrCreateAgentToken(directory)
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first != second || !validAgentToken(first) {
		t.Fatalf("agent tokens = %q and %q", first, second)
	}
	info, err := os.Stat(filepath.Join(directory, agentTokenFileName))
	if err != nil {
		t.Fatal(err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("agent token permissions = %#o, want 0600", permissions)
	}
}
