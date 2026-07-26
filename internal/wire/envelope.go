package wire

import (
	"encoding/json"
	"time"
)

const ProtocolVersion = 1

const (
	ClientOpen   = "open"
	ClientSub    = "sub"
	ClientUnsub  = "unsub"
	ClientResnap = "resnap"
	ClientPing   = "ping"

	ServerReady  = "ready"
	ServerSnap   = "snap"
	ServerEvent  = "event"
	ServerSuberr = "suberr"
	ServerSubend = "subend"
	ServerPong   = "pong"
)

// ClientMessage is the decoded client envelope. Fields not belonging to Type
// remain at their zero value. DecodeClientMessage enforces the tagged shape.
type ClientMessage struct {
	Type      string
	Protocol  int
	Client    string
	ID        uint32
	Topic     json.RawMessage
	Timestamp int64
}

type ReadyMessage struct {
	Type       string    `json:"t"`
	Protocol   int       `json:"protocol"`
	InstanceID string    `json:"instanceId"`
	ServerTime time.Time `json:"serverTime"`
}

type SnapshotMessage struct {
	Type string          `json:"t"`
	ID   uint32          `json:"id"`
	Seq  uint64          `json:"seq"`
	Data json.RawMessage `json:"data"`
}

// EventMessage reserves the reliable delta envelope for a future topic.
// Every protocol-v1 topic is snapshot-only and therefore never emits it.
type EventMessage struct {
	Type string          `json:"t"`
	ID   uint32          `json:"id"`
	Seq  uint64          `json:"seq"`
	Data json.RawMessage `json:"data"`
}

type SubscribeErrorMessage struct {
	Type  string `json:"t"`
	ID    uint32 `json:"id"`
	Error string `json:"error"`
}

type SubscribeEndMessage struct {
	Type   string `json:"t"`
	ID     uint32 `json:"id"`
	Reason string `json:"reason"`
}

type PongMessage struct {
	Type      string `json:"t"`
	Timestamp int64  `json:"ts"`
}
