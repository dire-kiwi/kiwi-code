package server

import (
	"errors"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

// withTerminalThreadMutation runs action while holding the cross-process
// mutation lease for one thread. It releases the in-process session lock while
// the action runs so unrelated terminal attaches are not blocked by process
// startup, then fences the result after releasing the lease. Cleanup runs only
// after both locks are released so it can safely call manager methods that
// acquire their own locks.
func withTerminalThreadMutation[T any](
	h *terminalHandler,
	item project.Project,
	thread project.Thread,
	action func() (T, error),
	cleanupOnError func(T),
) (T, error) {
	var zero T

	h.sessionMu.Lock()
	mutation, err := h.lockTerminalMutationLocked(item.ID, thread.ID)
	if err != nil {
		h.sessionMu.Unlock()
		return zero, err
	}
	if err := h.ensureTerminalThreadActiveLocked(item.ID, thread.ID); err != nil {
		releaseErr := mutation.Release()
		h.sessionMu.Unlock()
		return zero, errors.Join(err, releaseErr)
	}

	h.sessionMu.Unlock()
	result, actionErr := action()

	// Release the per-thread lease before reacquiring sessionMu so cleanup code
	// following the canonical lock order cannot deadlock with this fence.
	releaseErr := mutation.Release()
	h.sessionMu.Lock()
	fenceErr := h.finishTerminalThreadMutationLocked(item, thread)
	h.sessionMu.Unlock()

	if err := errors.Join(actionErr, fenceErr, releaseErr); err != nil {
		if cleanupOnError != nil {
			cleanupOnError(result)
		}
		return zero, err
	}
	return result, nil
}
