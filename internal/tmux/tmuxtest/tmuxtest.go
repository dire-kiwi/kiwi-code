// Package tmuxtest provides an isolated tmux server for tests. Every test
// gets its own randomly named socket so tests can never touch the user's
// production `kiwi-code` tmux server; the server is killed on cleanup.
package tmuxtest

import (
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/dire-kiwi/kiwi-code/internal/tmux"
)

// Isolated returns the tmux binary path and a fresh random socket name after
// probing that tmux can actually run in this environment (skipping the test
// otherwise). The socket's server is killed during test cleanup. The socket
// name is always short and never "kiwi-code".
func Isolated(t *testing.T) (path, socket string) {
	t.Helper()
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed")
	}

	t.Setenv("SHELL", "/bin/sh")
	// Keep the platform-limited tmux socket path short and verify tmux can run
	// before treating a capability restriction as a product test failure.
	t.Setenv("TMUX_TMPDIR", os.TempDir())
	var socketID [8]byte
	if _, err := rand.Read(socketID[:]); err != nil {
		t.Fatal(err)
	}
	socketName := fmt.Sprintf("d%x", socketID)
	if socketName == "" || socketName == "kiwi-code" {
		t.Fatalf("generated tmux socket name %q is not isolated", socketName)
	}
	client := tmux.NewClient(tmuxPath, socketName, nil)
	probe := client.Command(
		"new-session", "-d",
		"-s", "capability-probe",
		tmux.ShellCommand("/bin/sh", []string{"-c", "sleep 30"}),
	)
	t.Cleanup(func() {
		_ = client.Command("kill-server").Run()
	})
	output, probeErr := probe.CombinedOutput()
	if message := strings.TrimSpace(string(output)); probeErr != nil || message != "" {
		t.Skipf("tmux cannot start cleanly in this test environment: %v: %s", probeErr, message)
	}
	verify := client.Command("has-session", "-t", tmux.ExactSessionTarget("capability-probe"))
	output, verifyErr := verify.CombinedOutput()
	if message := strings.TrimSpace(string(output)); verifyErr != nil || message != "" {
		t.Skipf("tmux capability probe did not establish its exact session: %v: %s", verifyErr, message)
	}
	return tmuxPath, socketName
}

// IsolatedClient is Isolated wrapped in a ready-to-use client.
func IsolatedClient(t *testing.T) *tmux.Client {
	t.Helper()
	path, socket := Isolated(t)
	return tmux.NewClient(path, socket, nil)
}
