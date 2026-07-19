package server

import (
	"io"
	"os"
	"testing"
)

func TestDecodeTerminalClientMessage(t *testing.T) {
	message, ok := decodeTerminalClientMessage([]byte(`{"type":"input","data":"hello"}`))
	if !ok || message.Type != "input" || message.Data != "hello" {
		t.Fatalf("decode terminal message = %#v, %t", message, ok)
	}
	if _, ok := decodeTerminalClientMessage([]byte(`{"type":`)); ok {
		t.Fatal("malformed terminal message was accepted")
	}
}

func TestPTYWebSocketBridgeHandlesInput(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	defer reader.Close()
	defer writer.Close()

	bridge := &ptyWebSocketBridge{ptmx: writer}
	if err := bridge.Handle(clientMessage{Type: "input", Data: "hello"}); err != nil {
		t.Fatalf("handle input: %v", err)
	}
	buffer := make([]byte, 5)
	if _, err := io.ReadFull(reader, buffer); err != nil {
		t.Fatalf("read input: %v", err)
	}
	if got := string(buffer); got != "hello" {
		t.Fatalf("input = %q, want hello", got)
	}
}
