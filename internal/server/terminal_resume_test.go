package server

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func TestTerminalResumeUsesExactPerThreadConversation(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := &terminalHandler{projects: store}
	for _, tool := range []string{codingAgentCodex, codingAgentClaude, codingAgentPi, codingAgentGrok} {
		t.Run(tool, func(t *testing.T) {
			thread := item.Threads[0]
			directory := filepath.Join(store.DataDirectory(), "terminal-agent-sessions", item.ID, thread.ID, tool)
			if err := os.MkdirAll(directory, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "session"), []byte("exact-session\n"), 0600); err != nil {
				t.Fatal(err)
			}
			args, env, err := handler.terminalAgentResume(item, thread, tool, []string{"--model", "test"})
			if err != nil {
				t.Fatal(err)
			}
			expected := []string{"--model", "test", "--resume", "exact-session"}
			if tool == codingAgentCodex {
				expected = []string{"resume", "exact-session", "--model", "test"}
			}
			if tool == codingAgentPi {
				expected = []string{"--model", "test", "--session", "exact-session"}
			}
			if !reflect.DeepEqual(args, expected) || len(env) != 1 {
				t.Fatalf("resume args=%q env=%q", args, env)
			}
			other := thread
			other.ID = "other"
			args, _, err = handler.terminalAgentResume(item, other, tool, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, arg := range args {
				if arg == "exact-session" {
					t.Fatal("resumed another thread")
				}
			}
		})
	}
}

func TestOlderTerminalResumeUsesPickerRatherThanLastConversation(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread := item.Threads[0]
	now := time.Now()
	thread.UnsettledAt = &now
	thread.LastPromptAt = &now
	h := &terminalHandler{projects: store}
	for _, tool := range []string{codingAgentPi, codingAgentClaude, codingAgentCodex} {
		args, _, err := h.terminalAgentResume(item, thread, tool, nil)
		if err != nil {
			t.Fatal(err)
		}
		expected := []string{"--resume"}
		if tool == codingAgentCodex {
			expected = []string{"resume"}
		}
		if !reflect.DeepEqual(args, expected) {
			t.Fatalf("legacy %s args=%q", tool, args)
		}
	}
}
