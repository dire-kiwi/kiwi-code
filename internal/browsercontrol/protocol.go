package browsercontrol

import (
	"context"
	"encoding/json"
	"io"
)

const (
	BackendHeadless = "headless"
	BackendElectron = "electron"
)

// Provider is the implementation boundary for Kiwi Code's per-thread browser.
// Implementations must preserve the public action/result contract.
type Provider interface {
	Action(context.Context, Request) (json.RawMessage, error)
	OpenRecordingRange(context.Context, string, string, string, string) (Recording, error)
	Close(context.Context) error
}

// Recording is an authenticated provider stream. The caller must close Body.
type Recording struct {
	Body      io.ReadCloser
	Size      int64
	TotalSize int64
	Start     int64
	End       int64
	Partial   bool
	MIMEType  string
	Title     string
}

// Close is a no-op for the desktop client because Electron owns its lifecycle.
func (c *Client) Close(context.Context) error {
	c.httpClient.CloseIdleConnections()
	return nil
}
