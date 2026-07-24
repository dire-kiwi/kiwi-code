package browserhost

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dire-kiwi/kiwi-code/internal/browsercontrol"
)

func TestProviderNoSessionOperationsDoNotStartHost(t *testing.T) {
	provider := New(Options{})
	for _, operation := range []string{"session.status", "session.disconnect", "session.stop"} {
		result, err := provider.Action(context.Background(), browsercontrol.Request{
			ProjectID: "project",
			ThreadID:  "thread",
			Operation: operation,
		})
		if err != nil {
			t.Fatalf("%s: %v", operation, err)
		}
		var status struct {
			Backend      string `json:"backend"`
			Presentation string `json:"presentation"`
			Running      bool   `json:"running"`
			Capabilities struct {
				NativeView        bool `json:"nativeView"`
				InteractiveStream bool `json:"interactiveStream"`
			} `json:"capabilities"`
		}
		if err := json.Unmarshal(result, &status); err != nil {
			t.Fatal(err)
		}
		if status.Backend != "headless-chrome" || status.Presentation != "stream" || status.Running ||
			status.Capabilities.NativeView || !status.Capabilities.InteractiveStream {
			t.Fatalf("%s status = %#v", operation, status)
		}
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.cmd != nil || provider.tempDir != "" {
		t.Fatal("no-session operation started the browser host")
	}
}

func TestProviderPreviewWithoutSessionIsUnavailable(t *testing.T) {
	provider := New(Options{})
	_, err := provider.Action(context.Background(), browsercontrol.Request{Operation: "preview"})
	if !errors.Is(err, browsercontrol.ErrFrameUnavailable) {
		t.Fatalf("preview error = %v", err)
	}
}

func TestMinimalEnvironmentDoesNotForwardSecrets(t *testing.T) {
	t.Setenv("KIWI_CODE_AGENT_TOKEN", "secret-token")
	t.Setenv("PATH", "/bin")
	environment := minimalEnvironment("/chrome", `[]`, "/tmp/recordings")
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "secret-token") || strings.Contains(joined, "KIWI_CODE_AGENT_TOKEN") {
		t.Fatalf("minimal environment exposed a secret: %s", joined)
	}
	for _, expected := range []string{"KIWI_CODE_CHROME_BIN=/chrome", "KIWI_CODE_PROTECTED_ORIGINS=[]", "KIWI_CODE_BROWSER_RECORDINGS_DIR=/tmp/recordings"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("minimal environment missing %q: %s", expected, joined)
		}
	}
}
