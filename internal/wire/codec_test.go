package wire

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestDecodeClientMessage(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    ClientMessage
	}{
		{
			name:    "open",
			payload: `{"t":"open","protocol":1,"client":"kiwi-code-web"}`,
			want:    ClientMessage{Type: ClientOpen, Protocol: 1, Client: "kiwi-code-web"},
		},
		{
			name:    "subscribe",
			payload: `{"t":"sub","id":4294967295,"topic":{"tag":"projects"}}`,
			want: ClientMessage{
				Type:  ClientSub,
				ID:    math.MaxUint32,
				Topic: json.RawMessage(`{"tag":"projects"}`),
			},
		},
		{
			name:    "unsubscribe",
			payload: `{"t":"unsub","id":7}`,
			want:    ClientMessage{Type: ClientUnsub, ID: 7},
		},
		{
			name:    "resnapshot",
			payload: `{"t":"resnap","id":9}`,
			want:    ClientMessage{Type: ClientResnap, ID: 9},
		},
		{
			name:    "ping",
			payload: `{"t":"ping","ts":1723456789012}`,
			want:    ClientMessage{Type: ClientPing, Timestamp: 1723456789012},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := DecodeClientMessage([]byte(test.payload))
			if err != nil {
				t.Fatal(err)
			}
			if got.Type != test.want.Type || got.Protocol != test.want.Protocol ||
				got.Client != test.want.Client || got.ID != test.want.ID ||
				got.Timestamp != test.want.Timestamp || string(got.Topic) != string(test.want.Topic) {
				t.Fatalf("DecodeClientMessage() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestDecodeClientMessageRejectsInvalidShapes(t *testing.T) {
	tooLongClient := strings.Repeat("x", MaxClientNameRunes+1)
	tests := []string{
		``,
		`null`,
		`[]`,
		`{"t":"unknown"}`,
		`{"t":"open","protocol":1,"client":"web","extra":true}`,
		`{"T":"open","protocol":1,"client":"web"}`,
		`{"t":"open","t":"open","protocol":1,"client":"web"}`,
		`{"t":"open","protocol":1,"Protocol":1,"client":"web"}`,
		`{"t":"open","client":"web"}`,
		`{"t":"open","protocol":1,"client":""}`,
		`{"t":"open","protocol":1,"client":"bad\nclient"}`,
		`{"t":"open","protocol":1,"client":"` + tooLongClient + `"}`,
		`{"t":"sub","id":0,"topic":{"tag":"projects"}}`,
		`{"t":"sub","id":1,"topic":null}`,
		`{"t":"sub","id":1,"topic":[]}`,
		`{"t":"sub","ID":1,"topic":{"tag":"projects"}}`,
		`{"t":"sub","id":4294967296,"topic":{"tag":"projects"}}`,
		`{"t":"unsub","id":0}`,
		`{"t":"resnap","id":-1}`,
		`{"t":"ping","ts":"now"}`,
		`{"t":"ping"}`,
		`{"t":"ping","ts":1,"unknown":true}`,
		`{"t":"ping","ts":1} {}`,
	}
	for _, payload := range tests {
		t.Run(payload, func(t *testing.T) {
			if _, err := DecodeClientMessage([]byte(payload)); err == nil {
				t.Fatalf("DecodeClientMessage(%q) succeeded", payload)
			}
		})
	}
}

func TestEncodeServerEnvelope(t *testing.T) {
	payload, err := Encode(SnapshotMessage{
		Type: ServerSnap,
		ID:   4,
		Seq:  2,
		Data: json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(payload), `{"t":"snap","id":4,"seq":2,"data":{"ok":true}}`; got != want {
		t.Fatalf("Encode() = %s, want %s", got, want)
	}
}
