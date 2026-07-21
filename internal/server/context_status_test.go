package server

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/broadcast"
	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func TestContextStatusAPITracksEachPresentation(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread := item.Threads[0]
	application := &Server{
		projects:            store,
		contextStatuses:     newContextStatusTracker(),
		threadStatusChanges: broadcast.NewBroker[threadStatusKey](broadcast.DefaultMaxPending),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/projects/{id}/threads/{threadId}/context/status", application.updateContextStatus)

	path := "/api/projects/" + item.ID + "/threads/" + thread.ID + "/context/status"
	for _, body := range []string{
		`{"source":"pi-terminal","tokens":60000,"contextWindow":200000,"percent":30,"model":"openai-codex/gpt-test"}`,
		`{"source":"pi-native","tokens":null,"contextWindow":272000,"percent":null,"model":"openai/gpt-test"}`,
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodPut, path, bytes.NewBufferString(body)))
		if response.Code != http.StatusOK {
			t.Fatalf("context update status = %d: %s", response.Code, response.Body.String())
		}
		var status agentContextStatus
		if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
			t.Fatal(err)
		}
		if status.UpdatedAt.IsZero() {
			t.Fatalf("context update has no timestamp: %#v", status)
		}
	}

	statuses := application.contextStatuses.forThread(item.ID, thread.ID)
	if len(statuses) != 2 {
		t.Fatalf("context statuses = %#v, want two presentations", statuses)
	}
	terminal := statuses[contextStatusSourcePiTerminal]
	if terminal.Tokens == nil || *terminal.Tokens != 60000 || terminal.Percent == nil || *terminal.Percent != 30 {
		t.Fatalf("terminal context status = %#v", terminal)
	}
	native := statuses[contextStatusSourcePiNative]
	if native.Tokens != nil || native.Percent != nil || native.ContextWindow != 272000 {
		t.Fatalf("native context status = %#v", native)
	}

	terminal.UpdatedAt = terminal.UpdatedAt.Add(time.Minute)
	if _, changed := application.contextStatuses.update(item.ID, thread.ID, terminal); changed {
		t.Fatal("an unchanged periodic context report was treated as a status change")
	}
	nextPercent := 31.0
	terminal.Percent = &nextPercent
	if _, changed := application.contextStatuses.update(item.ID, thread.ID, terminal); !changed {
		t.Fatal("a changed context report was not detected")
	}

	application.contextStatuses.removeThread(item.ID, thread.ID)
	if statuses := application.contextStatuses.forThread(item.ID, thread.ID); len(statuses) != 0 {
		t.Fatalf("removed thread context statuses = %#v", statuses)
	}
}

func TestNormalizeAgentContextStatusRejectsInvalidValues(t *testing.T) {
	now := time.Date(2026, time.July, 22, 10, 0, 0, 0, time.FixedZone("test", 3600))
	tokens := int64(1000)
	percent := 10.0
	valid := agentContextStatusUpdate{
		Source:        contextStatusSourcePiTerminal,
		Tokens:        &tokens,
		ContextWindow: 10000,
		Percent:       &percent,
		Model:         "  provider/model  ",
	}
	status, ok := normalizeAgentContextStatus(valid, now)
	if !ok {
		t.Fatal("valid context status was rejected")
	}
	if status.Model != "provider/model" || !status.UpdatedAt.Equal(now.UTC()) {
		t.Fatalf("normalized context status = %#v", status)
	}

	overflowTokens := int64(11000)
	overflowPercent := 110.0
	overflow := valid
	overflow.Tokens = &overflowTokens
	overflow.Percent = &overflowPercent
	if _, ok := normalizeAgentContextStatus(overflow, now); !ok {
		t.Fatal("temporary context overflow above 100 percent was rejected")
	}

	negativeTokens := int64(-1)
	nan := math.NaN()
	tests := []agentContextStatusUpdate{
		{Source: "unknown", Tokens: &tokens, ContextWindow: 10000, Percent: &percent},
		{Source: contextStatusSourcePiTerminal, Tokens: &tokens, ContextWindow: 0, Percent: &percent},
		{Source: contextStatusSourcePiTerminal, Tokens: &negativeTokens, ContextWindow: 10000, Percent: &percent},
		{Source: contextStatusSourcePiTerminal, Tokens: &tokens, ContextWindow: 10000, Percent: nil},
		{Source: contextStatusSourcePiTerminal, Tokens: nil, ContextWindow: 10000, Percent: &percent},
		{Source: contextStatusSourcePiTerminal, Tokens: &tokens, ContextWindow: 10000, Percent: &nan},
		{Source: contextStatusSourcePiTerminal, Tokens: &tokens, ContextWindow: 10000, Percent: &percent, Model: "bad\nmodel"},
	}
	for index, input := range tests {
		if status, ok := normalizeAgentContextStatus(input, now); ok {
			t.Fatalf("invalid context status %d was accepted: %#v", index, status)
		}
	}
}
