package server

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

const codexModelTestScript = `#!/bin/sh
[ "$1" = "app-server" ] || exit 9
IFS= read -r line
case "$line" in *'"method":"initialize"'*) ;; *) exit 10 ;; esac
printf '%s\n' '{"id":1,"result":{}}'
IFS= read -r line
case "$line" in *'"method":"initialized"'*) ;; *) exit 11 ;; esac
IFS= read -r line
case "$line" in *'"method":"model/list"'*) ;; *) exit 12 ;; esac
printf '%s\n' '{"method":"notification"}'
printf '%s\n' '{"id":2,"result":{"data":[{"id":"display-id","model":"codex-test","displayName":"Codex Test","supportedReasoningEfforts":[{"reasoningEffort":"low"},{"reasoningEffort":"high"}]},{"model":"hidden-test","hidden":true},{"model":"bad model"}],"nextCursor":"page-two"}}'
IFS= read -r line
case "$line" in *'"cursor":"page-two"'*) ;; *) exit 13 ;; esac
printf '%s\n' '{"id":3,"result":{"data":[{"model":"codex-test"},{"model":"other-test"}],"nextCursor":null}}'
while IFS= read -r line; do :; done
`

func TestDiscoverCodexModels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(path, []byte(codexModelTestScript), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := discoverCodexModelsAtPath(ctx, path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := []codingAgentChoice{
		{ID: "codex-test", Label: "Codex Test", ReasoningLevels: []string{"low", "high"}},
		{ID: "other-test", Label: "other-test"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("models = %#v, want %#v", got, want)
	}
}

func TestDiscoverCodexModelsFailures(t *testing.T) {
	for _, scenario := range []struct{ name, script string }{
		{"rpc error", "printf '%s\\n' '{\"id\":1,\"error\":{\"message\":\"unavailable\"}}'"},
		{"invalid json", "printf '%s\\n' 'invalid json'"},
		{"early exit", "exit 1"},
		{"timeout", "while IFS= read -r line; do :; done"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "codex")
			if err := os.WriteFile(path, []byte("#!/bin/sh\n"+scenario.script+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			if _, err := discoverCodexModelsAtPath(ctx, path, ""); err == nil {
				t.Fatal("expected discovery error")
			}
		})
	}
}

func TestCodingAgentConfigsCodexUnavailable(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"codex", "pi", "grok"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", directory)
	handler := &terminalHandler{}
	configs, err := handler.codingAgentConfigs(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, config := range configs {
		if config.ID == codingAgentCodex {
			if !reflect.DeepEqual(config.Models, []codingAgentChoice{{ID: "", Label: "Use Codex default"}}) {
				t.Fatalf("Codex fallback = %#v", config.Models)
			}
			return
		}
	}
	t.Fatal("missing Codex configuration")
}
