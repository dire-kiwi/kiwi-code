package terminal

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
	message, ok := DecodeClientMessage(1, websocket.TextMessage, []byte(`{"type":"input","data":"hello"}`))
	if !ok || message.Type != "input" || message.Data != "hello" {
		t.Fatalf("decode terminal message = %#v, %t", message, ok)
	}
	if _, ok := DecodeClientMessage(1, websocket.TextMessage, []byte(`{"type":`)); ok {
		t.Fatal("malformed terminal message was accepted")
	}
	if _, ok := DecodeClientMessage(1, websocket.BinaryMessage, []byte("hello")); ok {
		t.Fatal("protocol v1 accepted binary terminal input")
	}
}

func TestDecodeTerminalClientMessageAcceptsProtocolV2BinaryInput(t *testing.T) {
	message, ok := DecodeClientMessage(ProtocolV2, websocket.BinaryMessage, []byte("hello\x00世界"))
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

	bridge := &PTYBridge{ptmx: writer}
	if err := bridge.Handle(ClientMessage{Type: "input", Data: "hello"}); err != nil {
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
	first := bytes.Repeat([]byte("a"), outputReadSize)
	second := bytes.Repeat([]byte("b"), outputReadSize)
	chunks := make(chan []byte, 2)
	chunks <- first
	chunks <- second
	close(chunks)
	readDone := make(chan error, 1)
	readDone <- io.EOF
	Done := make(chan error, 1)
	writer := &recordingWebSocketWriter{}
	bridge := &PTYBridge{stop: make(chan struct{})}

	bridge.writePTYOutput(writer, chunks, readDone, Done, nil)
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
	case err := <-Done:
		if err != io.EOF {
			t.Fatalf("terminal completion error = %v, want EOF", err)
		}
	default:
		t.Fatal("terminal completion was not reported")
	}
}

func TestTerminalOutputCreditBlocksAndResumes(t *testing.T) {
	credit := NewOutputCredit(8)
	stop := make(chan struct{})
	if !credit.Reserve(8, stop) {
		t.Fatal("initial credit reservation failed")
	}

	reserved := make(chan bool, 1)
	go func() { reserved <- credit.Reserve(1, stop) }()
	select {
	case <-reserved:
		t.Fatal("reservation exceeded the output credit window")
	case <-time.After(20 * time.Millisecond):
	}

	credit.Acknowledge(4)
	select {
	case ok := <-reserved:
		if !ok {
			t.Fatal("reservation did not resume after acknowledgement")
		}
	case <-time.After(time.Second):
		t.Fatal("reservation stayed blocked after acknowledgement")
	}
	if got := credit.Pending(); got != 5 {
		t.Fatalf("pending credit = %d, want 5", got)
	}
}

func TestTerminalOutputCreditStopUnblocksWaiter(t *testing.T) {
	credit := NewOutputCredit(1)
	stop := make(chan struct{})
	if !credit.Reserve(1, stop) {
		t.Fatal("initial credit reservation failed")
	}

	reserved := make(chan bool, 1)
	go func() { reserved <- credit.Reserve(1, stop) }()
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
	credit := NewOutputCredit(8)
	if !credit.Reserve(4, make(chan struct{})) {
		t.Fatal("credit reservation failed")
	}
	credit.Acknowledge(100)
	if got := credit.Pending(); got != 0 {
		t.Fatalf("pending credit after oversized acknowledgement = %d, want 0", got)
	}
}

func TestWebSocketPeerReportsStalledInputQueue(t *testing.T) {
	client, server := openWebSocketTransportTestPair(t)
	peer := StartPeer(server, NewWebSocketWriter(server), RawMessage, "test input stalled")
	defer peer.Stop()

	for index := 0; index < 17; index++ {
		if err := client.WriteMessage(websocket.TextMessage, []byte("message")); err != nil {
			t.Fatalf("write message %d: %v", index, err)
		}
	}

	select {
	case err := <-peer.Done:
		if err == nil || err.Error() != "test input stalled" {
			t.Fatalf("stalled peer error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stalled peer input queue was not reported")
	}
}

func TestPTYWebSocketBridgeReportsOutputWriteFailure(t *testing.T) {
	client, server := openWebSocketTransportTestPair(t)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})
	bridge := StartPTYBridge(server, NewWebSocketWriter(server), reader, 1, nil)
	defer bridge.Stop()

	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(writer, "terminal output"); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-bridge.Done:
		if err == nil {
			t.Fatal("closed WebSocket accepted PTY output")
		}
	case <-time.After(time.Second):
		t.Fatal("PTY output write failure was not reported")
	}
	_ = client.Close()
}

func openWebSocketTransportTestPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	serverConnection := make(chan *websocket.Conn, 1)
	release := make(chan struct{})
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverConnection <- connection
		<-release
		_ = connection.Close()
	}))
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	if err != nil {
		httpServer.Close()
		t.Fatal(err)
	}
	server := <-serverConnection
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
		close(release)
		httpServer.Close()
	})
	return client, server
}
