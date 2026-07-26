package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/wire"
	"github.com/gorilla/websocket"
)

const (
	stateChannelLimit           = 64
	stateReadLimit        int64 = 64 << 10
	stateHandshakeTimeout       = 10 * time.Second
	stateMaxQueuedFrames        = 512
)

type stateTopicHandler interface {
	Decode(json.RawMessage) (any, error)
	Open(context.Context, any, *stateChannel) error
}

type stateTopicError struct {
	message string
}

func (e *stateTopicError) Error() string { return e.message }

func stateTopicFailure(message string) error {
	return &stateTopicError{message: message}
}

type statePendingSnapshot struct {
	seq  uint64
	data json.RawMessage
}

type stateChannel struct {
	connection *stateConnection
	id         uint32
	ctx        context.Context
	cancel     context.CancelFunc
	resnap     chan struct{}

	pending      *statePendingSnapshot
	queued       bool
	lastPayload  []byte
	nextSeq      uint64
	forceNext    bool
	hasSnapshot  bool
	terminal     bool
	unsubscribed bool
}

func (c *stateChannel) Snapshot(value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.connection.queueSnapshot(c, payload)
}

func (c *stateChannel) Resnap() <-chan struct{} {
	return c.resnap
}

type stateOutbound struct {
	messageType int
	payload     []byte
	channel     *stateChannel
	snapshot    bool
	terminal    bool
	done        chan struct{}
}

type stateProtocolError struct {
	code   int
	reason string
}

func (e *stateProtocolError) Error() string { return e.reason }

type stateConnection struct {
	server           *Server
	socket           *websocket.Conn
	writer           *websocketWriter
	protectedOrigins []string
	ctx              context.Context
	cancel           context.CancelFunc

	mu       sync.Mutex
	channels map[uint32]*stateChannel
	lastID   uint32
	queue    []stateOutbound
	wake     chan struct{}

	channelWG sync.WaitGroup
}

func (s *Server) serveStateSocket(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		HandshakeTimeout: stateHandshakeTimeout,
		ReadBufferSize:   4 << 10,
		WriteBufferSize:  16 << 10,
		CheckOrigin:      s.browserOriginPolicy.allows,
	}
	socket, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer socket.Close()
	socket.SetReadLimit(stateReadLimit)

	writer := newWebSocketWriter(socket)
	_ = socket.SetReadDeadline(time.Now().Add(stateHandshakeTimeout))
	messageType, payload, err := socket.ReadMessage()
	if err != nil {
		return
	}
	if messageType != websocket.TextMessage {
		_ = writer.Close(websocket.CloseUnsupportedData, "state messages must be text")
		return
	}
	message, err := wire.DecodeClientMessage(payload)
	if err != nil || message.Type != wire.ClientOpen {
		_ = writer.Close(websocket.CloseProtocolError, "open must be the first state message")
		return
	}
	if message.Protocol != wire.ProtocolVersion {
		_ = writer.Close(websocket.CloseProtocolError, "unsupported state protocol")
		return
	}

	ready, err := wire.Encode(wire.ReadyMessage{
		Type:       wire.ServerReady,
		Protocol:   wire.ProtocolVersion,
		InstanceID: s.serverInstanceID(),
		ServerTime: time.Now().UTC(),
	})
	if err != nil || writer.Write(websocket.TextMessage, ready) != nil {
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	connection := &stateConnection{
		server:           s,
		socket:           socket,
		writer:           writer,
		protectedOrigins: browserRequestProtectedOrigins(r),
		ctx:              ctx,
		cancel:           cancel,
		channels:         make(map[uint32]*stateChannel),
		wake:             make(chan struct{}, 1),
	}
	_ = socket.SetReadDeadline(time.Now().Add(terminalPongTimeout))
	socket.SetPongHandler(func(string) error {
		return socket.SetReadDeadline(time.Now().Add(terminalPongTimeout))
	})

	writerDone := make(chan error, 1)
	go func() {
		err := connection.writeLoop()
		writerDone <- err
		if err != nil {
			_ = socket.Close()
		}
	}()

	readDone := make(chan error, 1)
	go func() { readDone <- connection.readLoop() }()

	var terminalErr error
	select {
	case terminalErr = <-readDone:
	case terminalErr = <-writerDone:
	}
	var protocolErr *stateProtocolError
	if errors.As(terminalErr, &protocolErr) {
		done := make(chan struct{})
		if connection.queueFrame(websocket.CloseMessage, websocket.FormatCloseMessage(protocolErr.code, protocolErr.reason), nil, false, done) {
			select {
			case <-done:
			case <-writerDone:
			case <-time.After(time.Second):
			}
		}
	}

	connection.shutdown()
	_ = socket.Close()
	connection.channelWG.Wait()
}

func (c *stateConnection) readLoop() error {
	for {
		messageType, payload, err := c.socket.ReadMessage()
		if err != nil {
			return err
		}
		if messageType != websocket.TextMessage {
			return &stateProtocolError{code: websocket.CloseUnsupportedData, reason: "state messages must be text"}
		}
		message, err := wire.DecodeClientMessage(payload)
		if err != nil {
			return &stateProtocolError{code: websocket.CloseProtocolError, reason: "invalid state message"}
		}
		switch message.Type {
		case wire.ClientOpen:
			return &stateProtocolError{code: websocket.CloseProtocolError, reason: "state connection is already open"}
		case wire.ClientSub:
			if !c.subscribe(message.ID, message.Topic) {
				return &stateProtocolError{code: websocket.ClosePolicyViolation, reason: "state output queue is full"}
			}
		case wire.ClientUnsub:
			c.unsubscribe(message.ID)
		case wire.ClientResnap:
			c.requestResnap(message.ID)
		case wire.ClientPing:
			payload, encodeErr := wire.Encode(wire.PongMessage{Type: wire.ServerPong, Timestamp: message.Timestamp})
			if encodeErr != nil || !c.queueFrame(websocket.TextMessage, payload, nil, false, nil) {
				return errors.New("state output queue is full")
			}
		}
	}
}

func (c *stateConnection) subscribe(id uint32, rawTopic json.RawMessage) bool {
	c.mu.Lock()
	if id <= c.lastID {
		c.mu.Unlock()
		return c.queueSubscribeError(id, "Subscription ids must be nonzero and never reused.")
	}
	c.lastID = id
	if len(c.channels) >= stateChannelLimit {
		c.mu.Unlock()
		return c.queueSubscribeError(id, fmt.Sprintf("A state connection may open at most %d channels.", stateChannelLimit))
	}
	c.mu.Unlock()

	handler, params, err := c.server.decodeStateTopic(rawTopic, c.protectedOrigins)
	if err != nil {
		return c.queueSubscribeError(id, err.Error())
	}

	channelContext, cancel := context.WithCancel(c.ctx)
	channel := &stateChannel{
		connection: c,
		id:         id,
		ctx:        channelContext,
		cancel:     cancel,
		resnap:     make(chan struct{}, 1),
	}
	c.mu.Lock()
	if c.ctx.Err() != nil {
		c.mu.Unlock()
		cancel()
		return false
	}
	c.channelWG.Add(1)
	c.channels[id] = channel
	c.mu.Unlock()

	go func() {
		defer c.channelWG.Done()
		err := handler.Open(channelContext, params, channel)
		c.finishChannel(channel, err)
	}()
	return true
}

func (c *stateConnection) queueSubscribeError(id uint32, message string) bool {
	payload, err := wire.Encode(wire.SubscribeErrorMessage{
		Type: wire.ServerSuberr, ID: id, Error: message,
	})
	return err == nil && c.queueFrame(websocket.TextMessage, payload, nil, false, nil)
}

func (c *stateConnection) unsubscribe(id uint32) {
	c.mu.Lock()
	channel := c.channels[id]
	if channel == nil || channel.unsubscribed {
		c.mu.Unlock()
		return
	}
	channel.unsubscribed = true
	channel.pending = nil
	delete(c.channels, id)
	c.mu.Unlock()
	channel.cancel()
}

func (c *stateConnection) requestResnap(id uint32) {
	c.mu.Lock()
	channel := c.channels[id]
	if channel == nil || channel.unsubscribed || channel.terminal {
		c.mu.Unlock()
		return
	}
	channel.forceNext = true
	c.mu.Unlock()
	select {
	case channel.resnap <- struct{}{}:
	default:
	}
}

func (c *stateConnection) queueSnapshot(channel *stateChannel, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx.Err() != nil || channel.ctx.Err() != nil || channel.unsubscribed || channel.terminal || c.channels[channel.id] != channel {
		return context.Canceled
	}
	if !channel.forceNext && bytes.Equal(channel.lastPayload, payload) {
		return nil
	}
	if !channel.queued && len(c.queue) >= stateMaxQueuedFrames {
		return errors.New("state output queue is full")
	}
	channel.forceNext = false
	channel.lastPayload = append(channel.lastPayload[:0], payload...)
	channel.nextSeq++
	channel.hasSnapshot = true
	channel.pending = &statePendingSnapshot{
		seq:  channel.nextSeq,
		data: append(json.RawMessage(nil), payload...),
	}
	if !channel.queued {
		channel.queued = true
		c.queue = append(c.queue, stateOutbound{channel: channel, snapshot: true})
		c.signalWriterLocked()
	}
	return nil
}

func (c *stateConnection) finishChannel(channel *stateChannel, openErr error) {
	c.mu.Lock()
	if channel.unsubscribed || c.ctx.Err() != nil || c.channels[channel.id] != channel {
		c.mu.Unlock()
		return
	}
	channel.terminal = true
	channel.cancel()
	hasSnapshot := channel.hasSnapshot
	message := "Subscription ended."
	var topicErr *stateTopicError
	if errors.As(openErr, &topicErr) {
		message = topicErr.message
	} else if openErr != nil && !errors.Is(openErr, context.Canceled) {
		message = "Subscription failed."
	}
	c.mu.Unlock()

	var payload []byte
	var err error
	if hasSnapshot {
		payload, err = wire.Encode(wire.SubscribeEndMessage{
			Type: wire.ServerSubend, ID: channel.id, Reason: message,
		})
	} else {
		payload, err = wire.Encode(wire.SubscribeErrorMessage{
			Type: wire.ServerSuberr, ID: channel.id, Error: message,
		})
	}
	if err != nil {
		return
	}
	if !c.queueFrame(websocket.TextMessage, payload, channel, true, nil) {
		c.unsubscribe(channel.id)
	}
}

func (c *stateConnection) queueFrame(messageType int, payload []byte, channel *stateChannel, terminal bool, done chan struct{}) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx.Err() != nil {
		return false
	}
	if len(c.queue) >= stateMaxQueuedFrames {
		return false
	}
	c.queue = append(c.queue, stateOutbound{
		messageType: messageType,
		payload:     append([]byte(nil), payload...),
		channel:     channel,
		terminal:    terminal,
		done:        done,
	})
	c.signalWriterLocked()
	return true
}

func (c *stateConnection) signalWriterLocked() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *stateConnection) writeLoop() error {
	ping := time.NewTicker(terminalPingInterval)
	defer ping.Stop()
	for {
		outbound, ok := c.nextOutbound()
		if ok {
			if outbound.snapshot {
				var pending *statePendingSnapshot
				c.mu.Lock()
				channel := outbound.channel
				if channel != nil {
					channel.queued = false
					if !channel.unsubscribed && c.channels[channel.id] == channel {
						pending = channel.pending
						channel.pending = nil
					}
				}
				c.mu.Unlock()
				if pending == nil {
					continue
				}
				payload, err := wire.Encode(wire.SnapshotMessage{
					Type: wire.ServerSnap, ID: outbound.channel.id, Seq: pending.seq, Data: pending.data,
				})
				if err != nil {
					return err
				}
				if err := c.writer.Write(websocket.TextMessage, payload); err != nil {
					return err
				}
				continue
			}

			if outbound.channel != nil {
				c.mu.Lock()
				skip := outbound.channel.unsubscribed
				c.mu.Unlock()
				if skip {
					if outbound.done != nil {
						close(outbound.done)
					}
					continue
				}
			}
			if err := c.writer.Write(outbound.messageType, outbound.payload); err != nil {
				return err
			}
			if outbound.terminal && outbound.channel != nil {
				c.mu.Lock()
				if c.channels[outbound.channel.id] == outbound.channel {
					delete(c.channels, outbound.channel.id)
				}
				c.mu.Unlock()
			}
			if outbound.done != nil {
				close(outbound.done)
			}
			continue
		}

		select {
		case <-c.ctx.Done():
			return nil
		case <-c.wake:
		case <-ping.C:
			if err := c.writer.Write(websocket.PingMessage, nil); err != nil {
				return err
			}
		}
	}
}

func (c *stateConnection) nextOutbound() (stateOutbound, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.queue) == 0 {
		return stateOutbound{}, false
	}
	outbound := c.queue[0]
	copy(c.queue, c.queue[1:])
	c.queue = c.queue[:len(c.queue)-1]
	return outbound, true
}

func (c *stateConnection) shutdown() {
	c.cancel()
	c.mu.Lock()
	channels := make([]*stateChannel, 0, len(c.channels))
	for _, channel := range c.channels {
		channel.unsubscribed = true
		channel.pending = nil
		channels = append(channels, channel)
	}
	c.channels = make(map[uint32]*stateChannel)
	c.queue = nil
	c.mu.Unlock()
	for _, channel := range channels {
		channel.cancel()
	}
}
