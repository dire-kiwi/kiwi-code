// Package assets materializes embedded agent asset bundles (plugins,
// extensions, skills) into the data directory: idempotent, atomic, and
// byte-for-byte reproducible so live agent sessions referencing these paths
// keep working across backend restarts and upgrades.
package assets

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dire-kiwi/kiwi-code/internal/atomicfile"
)

// EnsureFile writes contents at root/relativePath, creating parent
// directories (0700) and replacing the file atomically (0600, fsynced) only
// when its bytes differ. label names the asset kind in error messages.
// It returns the absolute path of the materialized file.
func EnsureFile(root, relativePath string, contents []byte, label string) (string, error) {
	path := filepath.Join(root, relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create %s directory: %w", label, err)
	}
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, contents) {
		return path, nil
	}
	if _, err := atomicfile.Write(path, contents, atomicfile.Options{
		Mode:        0o600,
		TempPattern: ".kiwi-code-atomic-*",
		SyncFile:    true,
	}); err != nil {
		return "", fmt.Errorf("write %s: %w", label, err)
	}
	return path, nil
}

// RemoveObsolete deletes retired asset paths left behind by earlier
// releases. Missing paths are not errors.
func RemoveObsolete(label string, paths ...string) error {
	for _, path := range paths {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove obsolete %s %q: %w", label, filepath.Base(path), err)
		}
	}
	return nil
}
