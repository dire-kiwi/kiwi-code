package browsercontrol

import (
	"context"
	"encoding/json"
)

const (
	BackendHeadless = "headless"
	BackendElectron = "electron"
)

// Provider is the implementation boundary for Kiwi Code's per-thread browser.
// Implementations must preserve the public action/result contract.
type Provider interface {
	Action(context.Context, Request) (json.RawMessage, error)
	Close(context.Context) error
}

// Close is a no-op for the desktop client because Electron owns its lifecycle.
func (c *Client) Close(context.Context) error {
	c.httpClient.CloseIdleConnections()
	return nil
}
