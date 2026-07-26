package wire

import (
	"encoding/json"
	"time"
)

// ProtocolFixture is the language-neutral compatibility artifact consumed by
// the web Effect schemas. The server package supplies topic snapshots from its
// actual domain structs; this package supplies the transport envelopes.
type ProtocolFixture struct {
	ProtocolVersion int                        `json:"protocolVersion"`
	ServerMessages  []json.RawMessage          `json:"serverMessages"`
	Topics          map[string]json.RawMessage `json:"topics"`
}

func BuildServerMessageFixtures() ([]json.RawMessage, error) {
	values := []any{
		ReadyMessage{
			Type:       ServerReady,
			Protocol:   ProtocolVersion,
			InstanceID: "fixture-instance",
			ServerTime: time.Date(2026, time.July, 26, 12, 34, 56, 0, time.UTC),
		},
		SnapshotMessage{
			Type: ServerSnap,
			ID:   1,
			Seq:  1,
			Data: json.RawMessage(`[]`),
		},
		EventMessage{
			Type: ServerEvent,
			ID:   1,
			Seq:  2,
			Data: json.RawMessage(`{"reserved":true}`),
		},
		SubscribeErrorMessage{
			Type:  ServerSuberr,
			ID:    2,
			Error: "Fixture subscription was rejected.",
		},
		SubscribeEndMessage{
			Type:   ServerSubend,
			ID:     3,
			Reason: "Fixture subscription ended.",
		},
		PongMessage{
			Type:      ServerPong,
			Timestamp: 1721997296000,
		},
	}
	messages := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		payload, err := Encode(value)
		if err != nil {
			return nil, err
		}
		messages = append(messages, json.RawMessage(payload))
	}
	return messages, nil
}

func BuildProtocolFixture(topics map[string]json.RawMessage) (ProtocolFixture, error) {
	messages, err := BuildServerMessageFixtures()
	if err != nil {
		return ProtocolFixture{}, err
	}
	clonedTopics := make(map[string]json.RawMessage, len(topics))
	for tag, snapshot := range topics {
		clonedTopics[tag] = append(json.RawMessage(nil), snapshot...)
	}
	return ProtocolFixture{
		ProtocolVersion: ProtocolVersion,
		ServerMessages:  messages,
		Topics:          clonedTopics,
	}, nil
}

func EncodeProtocolFixture(topics map[string]json.RawMessage) ([]byte, error) {
	fixture, err := BuildProtocolFixture(topics)
	if err != nil {
		return nil, err
	}
	payload, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(payload, '\n'), nil
}
