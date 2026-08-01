package server

import (
	"os"

	terminalio "github.com/dire-kiwi/kiwi-code/internal/terminal"
	"github.com/gorilla/websocket"
)

// Aliases while the serve flow migrates to the terminal transport package.
type (
	clientMessage        = terminalio.ClientMessage
	websocketWriter      = terminalio.WebSocketWriter
	terminalOutputCredit = terminalio.OutputCredit
	ptyWebSocketBridge   = terminalio.PTYBridge
)

const (
	terminalProtocolV2        = terminalio.ProtocolV2
	terminalOutputCreditBytes = terminalio.OutputCreditBytes
	terminalWriteTimeout      = terminalio.WriteTimeout
	terminalPongTimeout       = terminalio.PongTimeout
	terminalPingInterval      = terminalio.PingInterval
)

func newWebSocketWriter(connection *websocket.Conn) *websocketWriter {
	return terminalio.NewWebSocketWriter(connection)
}

func startWebSocketPeer[T any](
	connection *websocket.Conn,
	writer *websocketWriter,
	decode func(messageType int, payload []byte) (T, bool),
	stallReason string,
) *terminalio.Peer[T] {
	return terminalio.StartPeer(connection, writer, decode, stallReason)
}

func startPTYWebSocketBridge(
	connection *websocket.Conn,
	writer *websocketWriter,
	ptmx *os.File,
	protocol int,
	onFirstOutput func(),
) *ptyWebSocketBridge {
	return terminalio.StartPTYBridge(connection, writer, ptmx, protocol, onFirstOutput)
}

func decodeTerminalClientMessage(protocol, messageType int, payload []byte) (clientMessage, bool) {
	return terminalio.DecodeClientMessage(protocol, messageType, payload)
}

func rawWebSocketMessage(messageType int, payload []byte) ([]byte, bool) {
	return terminalio.RawMessage(messageType, payload)
}
