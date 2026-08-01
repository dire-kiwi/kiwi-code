// Package tmux is a product-agnostic client for driving one tmux server
// identified by a socket name. It owns process spawning (prepending the
// socket, stripping the inherited TMUX variable so a nested parent session
// cannot leak in), target formatting, output parsing, and error shaping.
// It knows nothing about Kiwi Code naming conventions, projects, or agents;
// those conventions live in higher layers.
package tmux

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// Client runs tmux commands against a single socket. The zero value is not
// usable; construct with NewClient. Client is a small immutable value and is
// safe for concurrent use.
type Client struct {
	path    string
	socket  string
	environ func() []string
}

// NewClient returns a client for the tmux binary at path, talking to the
// server on the named socket. environ supplies the base environment for
// spawned commands (nil means os.Environ); the TMUX variable is always
// removed so commands behave identically inside and outside a tmux session.
func NewClient(path, socket string, environ func() []string) *Client {
	if environ == nil {
		environ = os.Environ
	}
	return &Client{path: path, socket: socket, environ: environ}
}

func (c *Client) Path() string   { return c.path }
func (c *Client) Socket() string { return c.socket }

// Command builds an *exec.Cmd for `tmux -L <socket> <args...>` with the
// client's environment. Callers run and parse it themselves; this is the
// escape hatch that typed helpers are built on.
func (c *Client) Command(args ...string) *exec.Cmd {
	command := exec.Command(c.path, c.Arguments(args...)...)
	command.Env = c.Environment()
	return command
}

// CommandContext is Command with a context attached to the process.
func (c *Client) CommandContext(ctx context.Context, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, c.path, c.Arguments(args...)...)
	command.Env = c.Environment()
	return command
}

// Arguments returns args prefixed with the socket selection flags.
func (c *Client) Arguments(args ...string) []string {
	arguments := make([]string, 0, len(args)+2)
	arguments = append(arguments, "-L", c.socket)
	return append(arguments, args...)
}

// Environment returns the spawn environment: the base environment with any
// TMUX entry removed.
func (c *Client) Environment() []string {
	environment := c.environ()
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if key != "TMUX" {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
