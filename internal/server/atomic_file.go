package server

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

type serverAtomicFileOptions struct {
	Mode          fs.FileMode
	SyncFile      bool
	SyncDirectory bool
}

func writeFileAtomically(path string, contents []byte, options serverAtomicFileOptions) error {
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, ".dire-mux-atomic-*")
	if err != nil {
		return err
	}
	temporaryPath := file.Name()
	defer os.Remove(temporaryPath)
	if err := file.Chmod(options.Mode); err != nil {
		_ = file.Close()
		return err
	}
	written, err := file.Write(contents)
	if err != nil {
		_ = file.Close()
		return err
	}
	if written != len(contents) {
		_ = file.Close()
		return io.ErrShortWrite
	}
	if options.SyncFile {
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return err
		}
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	if options.SyncDirectory {
		directoryFile, err := os.Open(directory)
		if err != nil {
			return err
		}
		syncErr := directoryFile.Sync()
		closeErr := directoryFile.Close()
		return errors.Join(syncErr, closeErr)
	}
	return nil
}
