package server

import "github.com/dire-kiwi/kiwi-code/internal/durable"

// Aliases keeping terminal code readable while the durable coordination
// primitives live in internal/durable. New code should use the durable
// package directly; these are deleted once the terminal code moves out of
// this package.
type (
	terminalStopManager   = durable.StopManager
	terminalStopLease     = durable.StopLease
	terminalStopMarker    = durable.StopMarker
	terminalStopMarkerRef = durable.StopMarkerRef
	terminalStopScope     = durable.StopScope

	terminalMutationManager = durable.MutationManager
	terminalMutationLease   = durable.MutationLease
)

const (
	terminalStopScopeProject = durable.StopScopeProject
	terminalStopScopeThread  = durable.StopScopeThread
)

func newTerminalStopManager(dataDirectory string) *terminalStopManager {
	return durable.NewStopManager(dataDirectory)
}

func newTerminalMutationManager(dataDirectory string) *terminalMutationManager {
	return durable.NewMutationManager(dataDirectory)
}
