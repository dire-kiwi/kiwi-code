package project

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

type atomicFileOptions struct {
	Mode          os.FileMode
	TempPattern   string
	SyncFile      bool
	SyncDirectory bool
}

// writeAtomicFile reports published=true when rename completed, even if the
// subsequent directory sync failed. Callers that roll back in-memory state use
// that distinction to avoid diverging from already-visible persisted data.
func writeAtomicFile(path string, contents []byte, options atomicFileOptions) (published bool, err error) {
	directory := filepath.Dir(path)
	pattern := options.TempPattern
	if pattern == "" {
		pattern = "." + filepath.Base(path) + "-*.tmp"
	}
	temporary, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return false, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	mode := options.Mode
	if mode == 0 {
		mode = 0o600
	}
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return false, err
	}
	written, err := temporary.Write(contents)
	if err != nil {
		_ = temporary.Close()
		return false, err
	}
	if written != len(contents) {
		_ = temporary.Close()
		return false, io.ErrShortWrite
	}
	if options.SyncFile {
		if err := temporary.Sync(); err != nil {
			_ = temporary.Close()
			return false, err
		}
	}
	if err := temporary.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return false, err
	}
	if options.SyncDirectory {
		if err := syncProjectDirectory(directory); err != nil {
			return true, err
		}
	}
	return true, nil
}

func syncProjectDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}
