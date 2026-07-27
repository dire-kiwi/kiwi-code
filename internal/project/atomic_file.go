package project

import (
	"github.com/dire-kiwi/kiwi-code/internal/atomicfile"
)

type atomicFileOptions = atomicfile.Options

// writeAtomicFile reports published=true when rename completed, even if the
// subsequent directory sync failed. Callers that roll back in-memory state use
// that distinction to avoid diverging from already-visible persisted data.
func writeAtomicFile(path string, contents []byte, options atomicFileOptions) (published bool, err error) {
	return atomicfile.Write(path, contents, options)
}
