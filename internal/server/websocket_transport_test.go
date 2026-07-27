package server

import (
	"bytes"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type recordingWebSocketWriter struct {
	mu     sync.Mutex
	frames [][]byte
}

func (w *recordingWebSocketWriter) Write(_ int, payload []byte) error {
	w.mu.Lock()
	w.frames = append(w.frames, append([]byte(nil), payload...))
	w.mu.Unlock()
	return nil
}

func TestDecodeTerminalClientMessage(t *testing.T) {
	message, ok := decodeTerminalClientMessage(1, websocket.TextMessage, []byte(`{"type":"input","data":"hello"}`))
	if !ok || message.Type != "input" || message.Data != "hello" {
		t.Fatalf("decode terminal message = %#v, %t", message, ok)
	}
	if _, ok := decodeTerminalClientMessage(1, websocket.TextMessage, []byte(`{"type":`)); ok {
		t.Fatal("malformed terminal message was accepted")
	}
	if _, ok := decodeTerminalClientMessage(1, websocket.BinaryMessage, []byte("hello")); ok {
		t.Fatal("protocol v1 accepted binary terminal input")
	}
}

func TestDecodeTerminalClientMessageAcceptsProtocolV2BinaryInput(t *testing.T) {
	message, ok := decodeTerminalClientMessage(terminalProtocolV2, websocket.BinaryMessage, []byte("hello\x00世界"))
	if !ok || message.Type != "input" || message.Data != "hello\x00世界" {
		t.Fatalf("decode protocol v2 terminal input = %#v, %t", message, ok)
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

func TestPTYOutputCoalescesQueuedBulkReads(t *testing.T) {
	first := bytes.Repeat([]byte("a"), terminalOutputReadSize)
	second := bytes.Repeat([]byte("b"), terminalOutputReadSize)
	chunks := make(chan []byte, 2)
	chunks <- first
	chunks <- second
	close(chunks)
	readDone := make(chan error, 1)
	readDone <- io.EOF
	terminalDone := make(chan error, 1)
	writer := &recordingWebSocketWriter{}
	bridge := &ptyWebSocketBridge{stop: make(chan struct{})}

	bridge.writePTYOutput(writer, chunks, readDone, terminalDone, nil)
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.frames) != 1 {
		t.Fatalf("bulk output frames = %d, want 1", len(writer.frames))
	}
	want := append(append([]byte(nil), first...), second...)
	if !bytes.Equal(writer.frames[0], want) {
		t.Fatalf("coalesced frame length = %d, want %d", len(writer.frames[0]), len(want))
	}
	select {
	case err := <-terminalDone:
		if err != io.EOF {
			t.Fatalf("terminal completion error = %v, want EOF", err)
		}
	default:
		t.Fatal("terminal completion was not reported")
	}
}

func TestTerminalOutputCreditBlocksAndResumes(t *testing.T) {
	credit := newTerminalOutputCredit(8)
	stop := make(chan struct{})
	if !credit.reserve(8, stop) {
		t.Fatal("initial credit reservation failed")
	}

	reserved := make(chan bool, 1)
	go func() { reserved <- credit.reserve(1, stop) }()
	select {
	case <-reserved:
		t.Fatal("reservation exceeded the output credit window")
	case <-time.After(20 * time.Millisecond):
	}

	credit.acknowledge(4)
	select {
	case ok := <-reserved:
		if !ok {
			t.Fatal("reservation did not resume after acknowledgement")
		}
	case <-time.After(time.Second):
		t.Fatal("reservation stayed blocked after acknowledgement")
	}
	if got := credit.pending(); got != 5 {
		t.Fatalf("pending credit = %d, want 5", got)
	}
}

func TestTerminalOutputCreditStopUnblocksWaiter(t *testing.T) {
	credit := newTerminalOutputCredit(1)
	stop := make(chan struct{})
	if !credit.reserve(1, stop) {
		t.Fatal("initial credit reservation failed")
	}

	reserved := make(chan bool, 1)
	go func() { reserved <- credit.reserve(1, stop) }()
	close(stop)
	select {
	case ok := <-reserved:
		if ok {
			t.Fatal("stopped reservation unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("stopping did not unblock the credit waiter")
	}
}

func TestTerminalOutputCreditAcknowledgementCannotUnderflow(t *testing.T) {
	credit := newTerminalOutputCredit(8)
	if !credit.reserve(4, make(chan struct{})) {
		t.Fatal("credit reservation failed")
	}
	credit.acknowledge(100)
	if got := credit.pending(); got != 0 {
		t.Fatalf("pending credit after oversized acknowledgement = %d, want 0", got)
	}
}
