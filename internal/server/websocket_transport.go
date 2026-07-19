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
	decode func([]byte) (T, bool),
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
			_, payload, err := connection.ReadMessage()
			if err != nil {
				done <- err
				return
			}
			message, ok := decode(payload)
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

type ptyWebSocketBridge struct {
	peer         *websocketPeer[clientMessage]
	terminalDone <-chan error
	ptmx         *os.File
}

func startPTYWebSocketBridge(
	connection *websocket.Conn,
	writer *websocketWriter,
	ptmx *os.File,
) *ptyWebSocketBridge {
	peer := startWebSocketPeer(connection, writer, decodeTerminalClientMessage, "terminal input stalled")
	terminalDone := make(chan error, 1)
	go func() {
		buffer := make([]byte, 32*1024)
		for {
			count, readErr := ptmx.Read(buffer)
			if count > 0 {
				if err := writer.Write(websocket.BinaryMessage, buffer[:count]); err != nil {
					terminalDone <- err
					return
				}
			}
			if readErr != nil {
				terminalDone <- readErr
				return
			}
		}
	}()
	return &ptyWebSocketBridge{peer: peer, terminalDone: terminalDone, ptmx: ptmx}
}

func (b *ptyWebSocketBridge) Stop() {
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
	}
	return nil
}

func decodeTerminalClientMessage(payload []byte) (clientMessage, bool) {
	var message clientMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		return clientMessage{}, false
	}
	return message, true
}

func rawWebSocketMessage(payload []byte) ([]byte, bool) {
	return payload, true
}
