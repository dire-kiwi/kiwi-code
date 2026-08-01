package tmux_test

import (
	"strings"
	"testing"

	"github.com/dire-kiwi/kiwi-code/internal/tmux"
	"github.com/dire-kiwi/kiwi-code/internal/tmux/tmuxtest"
)

// TestClientAgainstIsolatedServer exercises the exec layer against a real
// tmux server on an isolated socket: session creation with a parsed window
// target, exact-target matching (a decoy session with a shared prefix must
// not satisfy an exact query), and error shaping from tmux output.
func TestClientAgainstIsolatedServer(t *testing.T) {
	client := tmuxtest.IsolatedClient(t)

	output, err := client.Command(
		"new-session", "-d", "-P",
		"-F", "#{window_index}\t#{window_id}\t#{pid}",
		"-s", "target", tmux.ShellCommand("/bin/sh", []string{"-c", "sleep 30"}),
	).CombinedOutput()
	if err != nil {
		t.Fatalf("new-session: %v: %s", err, output)
	}
	target, err := tmux.ParseWindowTarget(output)
	if err != nil {
		t.Fatal(err)
	}
	if target.ID == "" || target.ServerPID == "" {
		t.Fatalf("window target = %#v", target)
	}

	decoyOutput, err := client.Command(
		"new-session", "-d", "-s", "target-decoy",
		tmux.ShellCommand("/bin/sh", []string{"-c", "sleep 30"}),
	).CombinedOutput()
	if err != nil {
		t.Fatalf("new-session decoy: %v: %s", err, decoyOutput)
	}

	if out, err := client.Command("has-session", "-t", tmux.ExactSessionTarget("target")).CombinedOutput(); err != nil {
		t.Fatalf("has-session exact: %v: %s", err, out)
	}
	if out, err := client.Command("kill-session", "-t", tmux.ExactSessionTarget("target")).CombinedOutput(); err != nil {
		t.Fatalf("kill-session exact: %v: %s", err, out)
	}
	// The exact-name session is gone; only the decoy remains. A prefix match
	// would wrongly resolve "target" to "target-decoy" without "=".
	out, err := client.Command("has-session", "-t", tmux.ExactSessionTarget("target")).CombinedOutput()
	if err == nil {
		t.Fatalf("has-session after kill succeeded against decoy: %s", out)
	}
	shaped := tmux.CommandError("check session", out, err)
	if !strings.Contains(shaped.Error(), "check session:") {
		t.Fatalf("shaped error = %v", shaped)
	}
}
