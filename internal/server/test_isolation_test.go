package server

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"os/exec"
	"testing"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func newIsolatedServerHandler(t *testing.T, store *project.Store) (http.Handler, error) {
	t.Helper()
	return newIsolatedServerHandlerWithOptions(t, store, Options{})
}

func newIsolatedServerHandlerWithOptions(t *testing.T, store *project.Store, options Options) (http.Handler, error) {
	t.Helper()
	var socketID [8]byte
	if _, err := rand.Read(socketID[:]); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("dtest-%x", socketID)
	t.Cleanup(func() {
		tmuxPath, err := exec.LookPath("tmux")
		if err != nil {
			return
		}
		command := exec.Command(tmuxPath, "-L", socket, "kill-server")
		command.Env = tmuxEnvironment()
		_ = command.Run()
	})
	options.TmuxSocketName = socket
	options.DisableCleanup = true
	return NewWithOptions(store, options)
}
