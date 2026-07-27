package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWritePublishesContentAndMode(t *testing.T) {
	tests := []struct {
		name     string
		mode     os.FileMode
		wantMode os.FileMode
	}{
		{name: "default mode", wantMode: 0o600},
		{name: "explicit mode", mode: 0o640, wantMode: 0o640},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			published, err := Write(path, []byte("saved"), Options{
				Mode:          test.mode,
				SyncFile:      true,
				SyncDirectory: true,
			})
			if err != nil || !published {
				t.Fatalf("Write() = published %t, error %v", published, err)
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read published file: %v", err)
			}
			if got := string(contents); got != "saved" {
				t.Fatalf("published contents = %q, want %q", got, "saved")
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat published file: %v", err)
			}
			if got := info.Mode().Perm(); got != test.wantMode {
				t.Fatalf("published mode = %o, want %o", got, test.wantMode)
			}
		})
	}
}

func TestWriteUsesCustomPatternAndCleansUpAfterRenameFailure(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "occupied")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatalf("create destination directory: %v", err)
	}

	published, err := Write(destination, []byte("saved"), Options{
		TempPattern: ".custom-atomic-*.tmp",
	})
	if err == nil {
		t.Fatal("Write() error = nil, want rename failure")
	}
	if published {
		t.Fatal("Write() published = true before failed rename")
	}

	var linkError *os.LinkError
	if !errors.As(err, &linkError) {
		t.Fatalf("Write() error = %T, want *os.LinkError", err)
	}
	temporaryName := filepath.Base(linkError.Old)
	if !strings.HasPrefix(temporaryName, ".custom-atomic-") || !strings.HasSuffix(temporaryName, ".tmp") {
		t.Fatalf("temporary name = %q, want custom pattern", temporaryName)
	}
	if _, statErr := os.Stat(linkError.Old); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("temporary file remains after failed rename: %v", statErr)
	}
	matches, globErr := filepath.Glob(filepath.Join(directory, ".custom-atomic-*.tmp"))
	if globErr != nil {
		t.Fatalf("glob temporary files: %v", globErr)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files remain after failed rename: %v", matches)
	}
}

func TestWriteReportsUnpublishedWhenTemporaryCannotBeCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "state.json")

	published, err := Write(path, []byte("saved"), Options{})
	if err == nil {
		t.Fatal("Write() error = nil, want path failure")
	}
	if published {
		t.Fatal("Write() published = true before temporary file creation")
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination exists after failed write: %v", statErr)
	}
}
