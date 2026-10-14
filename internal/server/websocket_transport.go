package server

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
	terminalProtocolV2          = 2
	terminalOutputReadSize      = 32 * 1024
	terminalOutputFrameSize     = 64 * 1024
	terminalInteractiveFlush    = 4 * 1024
	terminalOutputCreditBytes   = 384 * 1024
	terminalOutputBatchDelay    = 750 * time.Microsecond
	terminalOutputQueueCapacity = 16
)

type webSocketMessageWriter interface {
	Write(messageType int, payload []byte) error
}

type websocketWriter struct {
	connection *websocket.Conn
	mu         sync.Mutex
}

func newWebSocketWriter(connection *websocket.Conn) *websocketWriter {
	return &websocketWriter{connection: connection}
}

func (w *websocketWriter) Write(messageType int, payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.connection.SetWriteDeadline(time.Now().Add(terminalWriteTimeout))
	return w.connection.WriteMessage(messageType, payload)
}

func (w *websocketWriter) Close(code int, reason string) error {
	return w.Write(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason))
}

type websocketPeer[T any] struct {
	writer   *websocketWriter
	messages <-chan T
	done     <-chan error
	ping     *time.Ticker
}

func startWebSocketPeer[T any](
	connection *websocket.Conn,
	writer *websocketWriter,
	decode func(int, []byte) (T, bool),
	stalledMessage string,
) *websocketPeer[T] {
	_ = connection.SetReadDeadline(time.Now().Add(terminalPongTimeout))
	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(terminalPongTimeout))
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

	return &websocketPeer[T]{
		writer:   writer,
		messages: messages,
		done:     done,
		ping:     time.NewTicker(terminalPingInterval),
	}
}

func (p *websocketPeer[T]) Stop() {
	p.ping.Stop()
}

func (p *websocketPeer[T]) WritePing() error {
	return p.writer.Write(websocket.PingMessage, nil)
}

// terminalOutputCredit bounds bytes accepted by the browser WebSocket but not
// yet parsed by xterm. Network-level backpressure alone is insufficient because
// browsers can queue WebSocket messages much faster than xterm can render them.
type terminalOutputCredit struct {
	mu          sync.Mutex
	outstanding int
	limit       int
	wake        chan struct{}
}

func newTerminalOutputCredit(limit int) *terminalOutputCredit {
	return &terminalOutputCredit{limit: limit, wake: make(chan struct{}, 1)}
}

func (c *terminalOutputCredit) reserve(count int, stop <-chan struct{}) bool {
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

func (c *terminalOutputCredit) acknowledge(count int) {
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

func (c *terminalOutputCredit) pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.outstanding
}

type ptyWebSocketBridge struct {
	peer         *websocketPeer[clientMessage]
	terminalDone <-chan error
	ptmx         *os.File
	protocol     int
	credit       *terminalOutputCredit
	stop         chan struct{}
	stopOnce     sync.Once
}

func startPTYWebSocketBridge(
	connection *websocket.Conn,
	writer *websocketWriter,
	ptmx *os.File,
	protocol int,
	onFirstOutput func(),
) *ptyWebSocketBridge {
	decode := func(messageType int, payload []byte) (clientMessage, bool) {
		return decodeTerminalClientMessage(protocol, messageType, payload)
	}
	peer := startWebSocketPeer(connection, writer, decode, "terminal input stalled")
	terminalDone := make(chan error, 1)
	stop := make(chan struct{})
	bridge := &ptyWebSocketBridge{
		peer:         peer,
		terminalDone: terminalDone,
		ptmx:         ptmx,
		protocol:     protocol,
		stop:         stop,
	}
	if protocol >= terminalProtocolV2 {
		bridge.credit = newTerminalOutputCredit(terminalOutputCreditBytes)
	}

	chunks := make(chan []byte, terminalOutputQueueCapacity)
	readDone := make(chan error, 1)
	go func() {
		defer close(chunks)
		buffer := make([]byte, terminalOutputReadSize)
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

	go bridge.writePTYOutput(writer, chunks, readDone, terminalDone, onFirstOutput)
	return bridge
}

func (b *ptyWebSocketBridge) writePTYOutput(
	writer webSocketMessageWriter,
	chunks <-chan []byte,
	readDone <-chan error,
	terminalDone chan<- error,
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
			timer = time.NewTimer(terminalOutputBatchDelay)
		} else {
			timer.Reset(terminalOutputBatchDelay)
		}
		timerC = timer.C
	}
	reportDone := func(err error) {
		select {
		case terminalDone <- err:
		default:
		}
	}
	flush := func() bool {
		if len(pending) == 0 {
			return true
		}
		stopTimer()
		if b.credit != nil && !b.credit.reserve(len(pending), b.stop) {
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
			space := terminalOutputFrameSize - len(pending)
			if space > len(chunk) {
				space = len(chunk)
			}
			pending = append(pending, chunk[:space]...)
			chunk = chunk[space:]
			if len(pending) == terminalOutputFrameSize && !flush() {
				return false
			}
		}
		if len(pending) > 0 && len(pending) <= terminalInteractiveFlush {
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

func (b *ptyWebSocketBridge) Stop() {
	b.stopOnce.Do(func() { close(b.stop) })
	b.peer.Stop()
}

func (b *ptyWebSocketBridge) Handle(message clientMessage) error {
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
			b.credit.acknowledge(int(message.Bytes))
		}
	}
	return nil
}

func decodeTerminalClientMessage(protocol, messageType int, payload []byte) (clientMessage, bool) {
	if protocol >= terminalProtocolV2 && messageType == websocket.BinaryMessage {
		return clientMessage{Type: "input", Data: string(payload)}, true
	}
	if messageType != websocket.TextMessage {
		return clientMessage{}, false
	}
	var message clientMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		return clientMessage{}, false
	}
	return message, true
}

func rawWebSocketMessage(_ int, payload []byte) ([]byte, bool) {
	return payload, true
}
