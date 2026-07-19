package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAtomicFilePublishesContentAndMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create directory: %v", err)
	}
	published, err := writeAtomicFile(path, []byte("saved"), atomicFileOptions{
		Mode:          0o600,
		SyncFile:      true,
		SyncDirectory: true,
	})
	if err != nil || !published {
		t.Fatalf("write atomic file: published=%t err=%v", published, err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if got := string(contents); got != "saved" {
		t.Fatalf("contents = %q", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
}
