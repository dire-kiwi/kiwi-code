package server

import (
	"bytes"
	"errors"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTerminalDiagnosticsIdentifyAgentWithoutLoggingPrompt(t *testing.T) {
	t.Setenv("KIWI_CODE_TERMINAL_DIAGNOSTICS", "")
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	request := httptest.NewRequest("GET", "/terminal?prompt=private-prompt&token=private-token", nil)
	diagnostics := newTerminalConnectionDiagnostics(request, "project", "thread", "pi", "codex", "kcv-test")
	diagnostics.mark("ensuring-agent-pane")
	diagnostics.failure("Could not start the coding agent", errors.New("profile is not managed by Kiwi Code"))
	diagnostics.finish()
	diagnostics.mark("after-close")
	got := output.String()
	for _, expected := range []string{`agent="codex"`, `socket="kcv-test"`, `phase="ensuring-agent-pane"`, "profile is not managed by Kiwi Code", `phase="closed"`} {
		if !strings.Contains(got, expected) {
			t.Errorf("missing %q in %s", expected, got)
		}
	}
	for _, secret := range []string{"private-prompt", "private-token", "after-close"} {
		if strings.Contains(got, secret) {
			t.Errorf("unexpected %q in logs", secret)
		}
	}
}

func TestTerminalSetupFailureLoggedWithoutVerboseDiagnostics(t *testing.T) {
	t.Setenv("KIWI_CODE_TERMINAL_DIAGNOSTICS", "")
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	diagnostics := newTerminalConnectionDiagnostics(httptest.NewRequest("GET", "/terminal", nil), "project", "thread", "terminal", "", "kcv-test")
	diagnostics.mark("accepted")
	diagnostics.failure("Could not attach to the terminal session", errors.New("PTY unavailable"))
	if got := output.String(); !strings.Contains(got, "PTY unavailable") || strings.Contains(got, "terminal timing:") {
		t.Fatalf("unexpected diagnostics: %s", got)
	}
}
