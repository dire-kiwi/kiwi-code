package terminal

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

const (
	ProtocolV2          = 2
	outputReadSize      = 32 * 1024
	outputFrameSize     = 64 * 1024
	interactiveFlush    = 4 * 1024
	OutputCreditBytes   = 384 * 1024
	outputBatchDelay    = 750 * time.Microsecond
	outputQueueCapacity = 16
)

// Keepalive and write deadlines shared by every websocket the backend serves.
const (
	WriteTimeout = 10 * time.Second
	PongTimeout  = 45 * time.Second
	PingInterval = 15 * time.Second
)

// ClientMessage is a client-to-server terminal frame (v1 JSON protocol; v2
// wraps raw binary input in the same struct).
type ClientMessage struct {
	Type  string `json:"type"`
	Data  string `json:"data,omitempty"`
	Cols  uint16 `json:"cols,omitempty"`
	Rows  uint16 `json:"rows,omitempty"`
	Bytes uint32 `json:"bytes,omitempty"`
}

type MessageWriter interface {
	Write(messageType int, payload []byte) error
}

type WebSocketWriter struct {
	connection *websocket.Conn
	mu         sync.Mutex
}

func NewWebSocketWriter(connection *websocket.Conn) *WebSocketWriter {
	return &WebSocketWriter{connection: connection}
}

func (w *WebSocketWriter) Write(messageType int, payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.connection.SetWriteDeadline(time.Now().Add(WriteTimeout))
	return w.connection.WriteMessage(messageType, payload)
}

func (w *WebSocketWriter) Close(code int, reason string) error {
	return w.Write(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason))
}

type Peer[T any] struct {
	writer   *WebSocketWriter
	Messages <-chan T
	Done     <-chan error
	Ping     *time.Ticker
}

func StartPeer[T any](
	connection *websocket.Conn,
	writer *WebSocketWriter,
	decode func(int, []byte) (T, bool),
	stalledMessage string,
) *Peer[T] {
	_ = connection.SetReadDeadline(time.Now().Add(PongTimeout))
	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(PongTimeout))
	})

	messages := make(chan T, 16)
	done := make(chan error, 1)
	go func() {
		for {
			messageType, payload, err := connection.ReadMessage()
			if err != nil {
				done <- err
				return
			}
			message, ok := decode(messageType, payload)
			if !ok {
				continue
			}
			select {
			case messages <- message:
			case <-time.After(time.Second):
				done <- errors.New(stalledMessage)
				return
			}
		}
	}()

	return &Peer[T]{
		writer:   writer,
		Messages: messages,
		Done:     done,
		Ping:     time.NewTicker(PingInterval),
	}
}

func (p *Peer[T]) Stop() {
	p.Ping.Stop()
}

func (p *Peer[T]) WritePing() error {
	return p.writer.Write(websocket.PingMessage, nil)
}

// OutputCredit bounds bytes accepted by the browser WebSocket but not
// yet parsed by xterm. Network-level backpressure alone is insufficient because
// browsers can queue WebSocket messages much faster than xterm can render them.
type OutputCredit struct {
	mu          sync.Mutex
	outstanding int
	limit       int
	wake        chan struct{}
}

func NewOutputCredit(limit int) *OutputCredit {
	return &OutputCredit{limit: limit, wake: make(chan struct{}, 1)}
}

func (c *OutputCredit) Reserve(count int, stop <-chan struct{}) bool {
	if count <= 0 {
		return true
	}
	for {
		c.mu.Lock()
		if c.outstanding+count <= c.limit {
			c.outstanding += count
			c.mu.Unlock()
			return true
		}
		c.mu.Unlock()

		select {
		case <-c.wake:
		case <-stop:
			return false
		}
	}
}

func (c *OutputCredit) Acknowledge(count int) {
	if count <= 0 {
		return
	}
	c.mu.Lock()
	if count >= c.outstanding {
		c.outstanding = 0
	} else {
		c.outstanding -= count
	}
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *OutputCredit) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.outstanding
}

type PTYBridge struct {
	Peer     *Peer[ClientMessage]
	Done     <-chan error
	ptmx     *os.File
	protocol int
	credit   *OutputCredit
	stop     chan struct{}
	stopOnce sync.Once
}

func StartPTYBridge(
	connection *websocket.Conn,
	writer *WebSocketWriter,
	ptmx *os.File,
	protocol int,
	onFirstOutput func(),
) *PTYBridge {
	decode := func(messageType int, payload []byte) (ClientMessage, bool) {
		return DecodeClientMessage(protocol, messageType, payload)
	}
	peer := StartPeer(connection, writer, decode, "terminal input stalled")
	Done := make(chan error, 1)
	stop := make(chan struct{})
	bridge := &PTYBridge{
		Peer:     peer,
		Done:     Done,
		ptmx:     ptmx,
		protocol: protocol,
		stop:     stop,
	}
	if protocol >= ProtocolV2 {
		bridge.credit = NewOutputCredit(OutputCreditBytes)
	}

	chunks := make(chan []byte, outputQueueCapacity)
	readDone := make(chan error, 1)
	go func() {
		defer close(chunks)
		buffer := make([]byte, outputReadSize)
		for {
			count, readErr := ptmx.Read(buffer)
			if count > 0 {
				payload := append([]byte(nil), buffer[:count]...)
				select {
				case chunks <- payload:
				case <-stop:
					return
				}
			}
			if readErr != nil {
				readDone <- readErr
				return
			}
		}
	}()

	go bridge.writePTYOutput(writer, chunks, readDone, Done, onFirstOutput)
	return bridge
}

func (b *PTYBridge) writePTYOutput(
	writer MessageWriter,
	chunks <-chan []byte,
	readDone <-chan error,
	Done chan<- error,
	onFirstOutput func(),
) {
	var pending []byte
	var timer *time.Timer
	var timerC <-chan time.Time
	firstOutput := true

	stopTimer := func() {
		if timer == nil {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timerC = nil
	}
	armTimer := func() {
		if timer == nil {
			timer = time.NewTimer(outputBatchDelay)
		} else {
			timer.Reset(outputBatchDelay)
		}
		timerC = timer.C
	}
	reportDone := func(err error) {
		select {
		case Done <- err:
		default:
		}
	}
	flush := func() bool {
		if len(pending) == 0 {
			return true
		}
		stopTimer()
		if b.credit != nil && !b.credit.Reserve(len(pending), b.stop) {
			return false
		}
		if err := writer.Write(websocket.BinaryMessage, pending); err != nil {
			reportDone(err)
			return false
		}
		if firstOutput {
			firstOutput = false
			if onFirstOutput != nil {
				onFirstOutput()
			}
		}
		pending = pending[:0]
		return true
	}
	appendChunk := func(chunk []byte) bool {
		for len(chunk) > 0 {
			space := outputFrameSize - len(pending)
			if space > len(chunk) {
				space = len(chunk)
			}
			pending = append(pending, chunk[:space]...)
			chunk = chunk[space:]
			if len(pending) == outputFrameSize && !flush() {
				return false
			}
		}
		if len(pending) > 0 && len(pending) <= interactiveFlush {
			return flush()
		}
		if len(pending) > 0 && timerC == nil {
			armTimer()
		}
		return true
	}

	defer stopTimer()
	for {
		select {
		case <-b.stop:
			return
		case chunk, open := <-chunks:
			if !open {
				if !flush() {
					return
				}
				select {
				case err := <-readDone:
					reportDone(err)
				default:
					reportDone(io.EOF)
				}
				return
			}
			if !appendChunk(chunk) {
				return
			}
		case <-timerC:
			timerC = nil
			if !flush() {
				return
			}
		}
	}
}

func (b *PTYBridge) Stop() {
	b.stopOnce.Do(func() { close(b.stop) })
	b.Peer.Stop()
}

func (b *PTYBridge) Handle(message ClientMessage) error {
	switch message.Type {
	case "input":
		_, err := io.WriteString(b.ptmx, message.Data)
		return err
	case "resize":
		if message.Cols > 1 && message.Rows > 1 {
			_ = pty.Setsize(b.ptmx, &pty.Winsize{Cols: message.Cols, Rows: message.Rows})
		}
	case "ack":
		if b.credit != nil {
			b.credit.Acknowledge(int(message.Bytes))
		}
	}
	return nil
}

func DecodeClientMessage(protocol, messageType int, payload []byte) (ClientMessage, bool) {
	if protocol >= ProtocolV2 && messageType == websocket.BinaryMessage {
		return ClientMessage{Type: "input", Data: string(payload)}, true
	}
	if messageType != websocket.TextMessage {
		return ClientMessage{}, false
	}
	var message ClientMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		return ClientMessage{}, false
	}
	return message, true
}

func RawMessage(_ int, payload []byte) ([]byte, bool) {
	return payload, true
}
