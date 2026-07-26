package headless

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/wire"
)

func TestStateClientMessageShapes(t *testing.T) {
	handshake, err := json.Marshal(stateClientMessage{
		Type:     wire.ClientOpen,
		Protocol: wire.ProtocolVersion,
		Client:   "kiwi-code-headless",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(handshake), `{"t":"open","protocol":1,"client":"kiwi-code-headless"}`; got != want {
		t.Fatalf("state handshake = %s, want %s", got, want)
	}

	topic := stateTopic{Tag: projectsTopic}
	subscription, err := json.Marshal(stateClientMessage{
		Type:  "sub",
		ID:    projectsChannelID,
		Topic: &topic,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(subscription), `{"t":"sub","id":1,"topic":{"tag":"projects"}}`; got != want {
		t.Fatalf("state subscription = %s, want %s", got, want)
	}
}

func TestDecodeStateServerMessage(t *testing.T) {
	message, err := decodeStateServerMessage([]byte(
		`{"t":"snap","id":1,"seq":2,"data":[{"id":"one"}]}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if message.Type != "snap" || message.ID != projectsChannelID || message.Sequence != 2 ||
		string(message.Data) != `[{"id":"one"}]` {
		t.Fatalf("decoded state message = %#v", message)
	}

	for _, payload := range []string{
		`{"t":"snap","id":1,"seq":2,"data":[],"unexpected":true}`,
		`{"t":"snap","id":1,"seq":2,"data":[]}{}`,
		`{"id":1}`,
		`{"t":`,
	} {
		if _, err := decodeStateServerMessage([]byte(payload)); err == nil {
			t.Fatalf("malformed state message was accepted: %s", payload)
		}
	}
}

func TestStateClientRetainsAnOutOfOrderTopicSnapshot(t *testing.T) {
	client := &stateClient{
		snapshots: make(chan stateSnapshot, 2),
		errors:    make(chan error, 1),
		pending:   make(map[string]stateSnapshot),
	}
	client.snapshots <- stateSnapshot{topic: activityTopic, data: []byte(`[]`)}
	client.snapshots <- stateSnapshot{topic: projectsTopic, data: []byte(`[]`)}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.waitFor(ctx, projectsTopic, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := client.waitFor(ctx, activityTopic, nil); err != nil {
		t.Fatal(err)
	}
}

func TestActivitySnapshotContainsRejectsMalformedJSON(t *testing.T) {
	if activitySnapshotContains([]byte("not-json"), "project", "thread", "") {
		t.Fatal("malformed activity snapshot matched a cleared status")
	}
}

func TestParseBaseURLRejectsNonHTTPServer(t *testing.T) {
	if _, err := parseBaseURL("ws://127.0.0.1:4000"); err == nil || !strings.Contains(err.Error(), "http or https") {
		t.Fatalf("parseBaseURL error = %v", err)
	}
}
