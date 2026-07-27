package server

import (
	"io/fs"

	"github.com/dire-kiwi/kiwi-code/internal/atomicfile"
)

type serverAtomicFileOptions struct {
	Mode          fs.FileMode
	SyncFile      bool
	SyncDirectory bool
}

func writeFileAtomically(path string, contents []byte, options serverAtomicFileOptions) error {
	_, err := atomicfile.Write(path, contents, atomicfile.Options{
		Mode:          options.Mode,
		TempPattern:   ".kiwi-code-atomic-*",
		SyncFile:      options.SyncFile,
		SyncDirectory: options.SyncDirectory,
	})
	return err
}
