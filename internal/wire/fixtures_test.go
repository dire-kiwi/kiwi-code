package wire

import (
	"encoding/json"
	"testing"
)

func TestServerMessageFixturesCoverEveryEnvelope(t *testing.T) {
	messages, err := BuildServerMessageFixtures()
	if err != nil {
		t.Fatal(err)
	}
	messageTypes := make([]string, 0, len(messages))
	for _, payload := range messages {
		var header struct {
			Type string `json:"t"`
		}
		if err := json.Unmarshal(payload, &header); err != nil {
			t.Fatal(err)
		}
		messageTypes = append(messageTypes, header.Type)
	}
	want := []string{
		ServerReady,
		ServerSnap,
		ServerEvent,
		ServerSuberr,
		ServerSubend,
		ServerPong,
	}
	if len(messageTypes) != len(want) {
		t.Fatalf("server fixture messages = %v, want %v", messageTypes, want)
	}
	for index := range want {
		if messageTypes[index] != want[index] {
			t.Fatalf("server fixture messages = %v, want %v", messageTypes, want)
		}
	}
}
