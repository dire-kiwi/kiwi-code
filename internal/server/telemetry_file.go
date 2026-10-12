package server

import (
	"errors"
	"fmt"
	"log"
	"os"
	"time"
)

// corruptTelemetryFileError marks a telemetry file whose contents cannot be
// decoded, as opposed to a file that could not be read at all.
type corruptTelemetryFileError struct {
	err error
}

func (e *corruptTelemetryFileError) Error() string {
	return e.err.Error()
}

func (e *corruptTelemetryFileError) Unwrap() error {
	return e.err
}

// quarantineCorruptTelemetryFile moves an undecodable telemetry file aside to
// <name>.corrupt-<timestamp> so the server can start with fresh state while the
// original bytes stay available for inspection. Only best-effort telemetry may
// use it; projects, settings, and profiles must keep failing closed.
func quarantineCorruptTelemetryFile(path string, cause error) error {
	quarantinePath := path + ".corrupt-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if err := os.Rename(path, quarantinePath); err != nil {
		return fmt.Errorf("quarantine corrupt telemetry file: %w", errors.Join(cause, err))
	}
	log.Printf("quarantined corrupt telemetry file: path=%q quarantine=%q error=%v", path, quarantinePath, cause)
	return nil
}
