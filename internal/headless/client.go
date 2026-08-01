// Package headless exercises the Kiwi Code HTTP and WebSocket APIs without a
// browser.
package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
	"github.com/dire-kiwi/kiwi-code/internal/wire"
	"github.com/gorilla/websocket"
)

const (
	projectsTopic        = "projects"
	activityTopic        = "agentActivity"
	projectsChannelID    = uint32(1)
	activityChannelID    = uint32(2)
	defaultClientCount   = 3
	maxStateMessageBytes = 8 << 20
)

// Options configures a multi-client API check.
type Options struct {
	BaseURL         string
	Clients         int
	ProjectPath     string
	Output          io.Writer
	IsolationWindow time.Duration
	SkipTerminal    bool
}

// Run verifies global status fan-out and terminal session scoping against a
// running Kiwi Code server. The test project and its tmux sessions are removed
// before Run returns.
func Run(ctx context.Context, options Options) error {
	started := time.Now()
	if options.Clients == 0 {
		options.Clients = defaultClientCount
	}
	if options.Clients < 2 {
		return errors.New("at least two clients are required")
	}
	if options.IsolationWindow <= 0 {
		options.IsolationWindow = 300 * time.Millisecond
	}
	if options.Output == nil {
		options.Output = io.Discard
	}
	baseURL, err := parseBaseURL(options.BaseURL)
	if err != nil {
		return err
	}
	httpClient := &http.Client{}
	if err := requireHealth(ctx, httpClient, baseURL); err != nil {
		return err
	}

	projectPath := strings.TrimSpace(options.ProjectPath)
	removeProjectPath := func() {}
	if projectPath == "" {
		projectPath, err = os.MkdirTemp("", "kiwi-code-headless-")
		if err != nil {
			return fmt.Errorf("create test project directory: %w", err)
		}
		removeProjectPath = func() { _ = os.RemoveAll(projectPath) }
	} else if projectPath, err = filepath.Abs(projectPath); err != nil {
		return fmt.Errorf("resolve test project path: %w", err)
	}
	defer removeProjectPath()

	stateClients := make([]*stateClient, 0, options.Clients)
	terminalClients := make([]*terminalClient, 0, options.Clients+1)
	var createdProject *project.Project
	activeThreadIDs := make(map[string]struct{})
	defer func() {
		for _, client := range terminalClients {
			client.close()
		}
		for _, client := range stateClients {
			client.close()
		}
		if createdProject != nil {
			cleanupContext, cancelCleanup := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelCleanup()
			for threadID := range activeThreadIDs {
				_ = requestJSON(cleanupContext, httpClient, baseURL, http.MethodDelete,
					threadPath(createdProject.ID, threadID), nil, nil, http.StatusNoContent, http.StatusNotFound)
			}
			_ = requestJSON(cleanupContext, httpClient, baseURL, http.MethodDelete,
				"/api/projects/"+url.PathEscape(createdProject.ID), nil, nil, http.StatusNoContent, http.StatusNotFound)
		}
	}()

	for index := 0; index < options.Clients; index++ {
		client, err := openStateClient(ctx, baseURL)
		if err != nil {
			return fmt.Errorf("open state WebSocket client %d: %w", index+1, err)
		}
		stateClients = append(stateClients, client)
	}
	if err := waitForEveryClient(ctx, stateClients, func(client *stateClient) error {
		if _, err := client.waitFor(ctx, projectsTopic, nil); err != nil {
			return fmt.Errorf("read initial projects: %w", err)
		}
		if _, err := client.waitFor(ctx, activityTopic, nil); err != nil {
			return fmt.Errorf("read initial activity: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}
	pass(options.Output, "opened %d state WebSocket clients", len(stateClients))

	var item project.Project
	name := fmt.Sprintf("headless-%d", time.Now().UnixNano())
	if err := requestJSON(ctx, httpClient, baseURL, http.MethodPost, "/api/projects",
		map[string]string{"name": name, "path": projectPath}, &item, http.StatusCreated); err != nil {
		return fmt.Errorf("create test project: %w", err)
	}
	createdProject = &item
	if len(item.Threads) != 1 {
		return fmt.Errorf("created project has %d initial threads, want 1", len(item.Threads))
	}
	firstThread := item.Threads[0]
	activeThreadIDs[firstThread.ID] = struct{}{}
	if err := waitForEveryProjectSnapshot(ctx, stateClients, func(projects []project.Project) bool {
		return hasProject(projects, item.ID)
	}); err != nil {
		return fmt.Errorf("fan out project creation: %w", err)
	}
	pass(options.Output, "project creation reached every global client")

	var secondThread project.Thread
	if err := requestJSON(ctx, httpClient, baseURL, http.MethodPost,
		"/api/projects/"+url.PathEscape(item.ID)+"/threads",
		map[string]any{"title": "Headless isolation", "worktree": false}, &secondThread, http.StatusCreated); err != nil {
		return fmt.Errorf("create isolation thread: %w", err)
	}
	activeThreadIDs[secondThread.ID] = struct{}{}
	if err := waitForEveryProjectSnapshot(ctx, stateClients, func(projects []project.Project) bool {
		return hasThread(projects, item.ID, secondThread.ID, "Headless isolation")
	}); err != nil {
		return fmt.Errorf("fan out thread creation: %w", err)
	}
	pass(options.Output, "thread creation reached every global client")

	activityPath := threadPath(item.ID, firstThread.ID) + "/agents/pi/activity"
	if err := requestJSON(ctx, httpClient, baseURL, http.MethodPut, activityPath,
		map[string]string{"state": "working"}, nil, http.StatusOK); err != nil {
		return fmt.Errorf("set working activity: %w", err)
	}
	if err := waitForEveryActivitySnapshot(ctx, stateClients, item.ID, firstThread.ID, "working"); err != nil {
		return fmt.Errorf("fan out working activity: %w", err)
	}
	if err := requestJSON(ctx, httpClient, baseURL, http.MethodPut, activityPath,
		map[string]string{"state": "working"}, nil, http.StatusOK); err != nil {
		return fmt.Errorf("send working heartbeat: %w", err)
	}
	if err := waitForEveryActivitySnapshot(ctx, stateClients, item.ID, firstThread.ID, "working"); err != nil {
		return fmt.Errorf("fan out working heartbeat: %w", err)
	}
	if err := requestJSON(ctx, httpClient, baseURL, http.MethodPut, activityPath,
		map[string]string{"state": "finished"}, nil, http.StatusOK); err != nil {
		return fmt.Errorf("set finished activity: %w", err)
	}
	if err := waitForEveryActivitySnapshot(ctx, stateClients, item.ID, firstThread.ID, "finished"); err != nil {
		return fmt.Errorf("fan out finished activity: %w", err)
	}
	if err := requestJSON(ctx, httpClient, baseURL, http.MethodPut, activityPath,
		map[string]string{"state": "idle"}, nil, http.StatusNoContent); err != nil {
		return fmt.Errorf("set idle activity: %w", err)
	}
	if err := waitForEveryActivityCleared(ctx, stateClients, item.ID, firstThread.ID); err != nil {
		return fmt.Errorf("fan out idle activity: %w", err)
	}
	pass(options.Output, "working heartbeat and rapid status transitions reached every global client in order")

	if !options.SkipTerminal {
		for index := 0; index < options.Clients; index++ {
			client, err := openTerminalClient(ctx, baseURL, item.ID, firstThread.ID)
			if err != nil {
				return fmt.Errorf("open shared terminal client %d: %w", index+1, err)
			}
			terminalClients = append(terminalClients, client)
		}
		isolationClient, err := openTerminalClient(ctx, baseURL, item.ID, secondThread.ID)
		if err != nil {
			return fmt.Errorf("open isolated terminal client: %w", err)
		}
		terminalClients = append(terminalClients, isolationClient)

		sharedToken := fmt.Sprintf("shared-%d", time.Now().UnixNano())
		if err := terminalClients[0].writeInput("printf '\\n%s\\n' '" + sharedToken + "'\n"); err != nil {
			return fmt.Errorf("write shared terminal token: %w", err)
		}
		for index, client := range terminalClients[:options.Clients] {
			if err := client.waitFor(ctx, sharedToken); err != nil {
				return fmt.Errorf("shared terminal client %d: %w", index+1, err)
			}
		}
		if err := isolationClient.ensureAbsent(ctx, sharedToken, options.IsolationWindow); err != nil {
			return fmt.Errorf("terminal session isolation: %w", err)
		}

		isolationToken := fmt.Sprintf("isolated-%d", time.Now().UnixNano())
		if err := isolationClient.writeInput("printf '\\n%s\\n' '" + isolationToken + "'\n"); err != nil {
			return fmt.Errorf("write isolated terminal token: %w", err)
		}
		if err := isolationClient.waitFor(ctx, isolationToken); err != nil {
			return fmt.Errorf("isolated terminal output: %w", err)
		}
		for index, client := range terminalClients[:options.Clients] {
			if err := client.ensureAbsent(ctx, isolationToken, options.IsolationWindow); err != nil {
				return fmt.Errorf("shared terminal client %d isolation: %w", index+1, err)
			}
		}
		pass(options.Output, "tmux output reached attached clients and did not leak to another thread")
	} else {
		pass(options.Output, "terminal fan-out and isolation skipped")
	}

	if err := requestJSON(ctx, httpClient, baseURL, http.MethodPatch,
		threadPath(item.ID, secondThread.ID), map[string]any{"title": "Headless renamed"}, nil, http.StatusOK); err != nil {
		return fmt.Errorf("rename isolation thread: %w", err)
	}
	if err := waitForEveryProjectSnapshot(ctx, stateClients, func(projects []project.Project) bool {
		return hasThread(projects, item.ID, secondThread.ID, "Headless renamed")
	}); err != nil {
		return fmt.Errorf("fan out thread rename: %w", err)
	}
	if err := requestJSON(ctx, httpClient, baseURL, http.MethodDelete,
		threadPath(item.ID, secondThread.ID), nil, nil, http.StatusNoContent); err != nil {
		return fmt.Errorf("delete isolation thread: %w", err)
	}
	delete(activeThreadIDs, secondThread.ID)
	if err := waitForEveryProjectSnapshot(ctx, stateClients, func(projects []project.Project) bool {
		return !hasThread(projects, item.ID, secondThread.ID, "")
	}); err != nil {
		return fmt.Errorf("fan out thread deletion: %w", err)
	}
	if !options.SkipTerminal {
		if err := terminalClients[options.Clients].waitUntilClosed(ctx); err != nil {
			return fmt.Errorf("isolation terminal after thread deletion: %w", err)
		}
		pass(options.Output, "thread deletion closed its open terminal stream")
	} else {
		pass(options.Output, "thread rename and deletion reached every global client")
	}

	if err := requestJSON(ctx, httpClient, baseURL, http.MethodDelete,
		"/api/projects/"+url.PathEscape(item.ID), nil, nil, http.StatusNoContent); err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	createdProject = nil
	delete(activeThreadIDs, firstThread.ID)
	if err := waitForEveryProjectSnapshot(ctx, stateClients, func(projects []project.Project) bool {
		return !hasProject(projects, item.ID)
	}); err != nil {
		return fmt.Errorf("fan out project deletion: %w", err)
	}
	if !options.SkipTerminal {
		for index, client := range terminalClients[:options.Clients] {
			if err := client.waitUntilClosed(ctx); err != nil {
				return fmt.Errorf("shared terminal client %d after project deletion: %w", index+1, err)
			}
		}
		pass(options.Output, "project deletion reached every client and closed its terminal streams")
	} else {
		pass(options.Output, "project deletion reached every global client")
	}

	pass(options.Output, "multi-client API check passed in %s", time.Since(started).Round(time.Millisecond))
	return nil
}

func pass(output io.Writer, format string, arguments ...any) {
	_, _ = fmt.Fprintf(output, "PASS  "+format+"\n", arguments...)
}

func requireHealth(ctx context.Context, client *http.Client, base *url.URL) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint(base, "/api/health"), nil)
	if err != nil {
		return fmt.Errorf("create server health request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("server health check: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("server health check returned %s", response.Status)
	}
	return nil
}

func parseBaseURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		raw = "http://127.0.0.1:4000"
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse server URL: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("server URL must use http or https and include a host")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed, nil
}

func endpoint(base *url.URL, path string) string {
	copy := *base
	copy.Path = strings.TrimRight(base.Path, "/") + path
	copy.RawQuery = ""
	copy.Fragment = ""
	return copy.String()
}

func threadPath(projectID, threadID string) string {
	return "/api/projects/" + url.PathEscape(projectID) + "/threads/" + url.PathEscape(threadID)
}

func requestJSON(ctx context.Context, client *http.Client, base *url.URL, method, path string, input, output any, wantStatuses ...int) error {
	var body io.Reader
	if input != nil {
		contents, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(contents)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint(base, path), body)
	if err != nil {
		return err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	matched := false
	for _, status := range wantStatuses {
		if response.StatusCode == status {
			matched = true
			break
		}
	}
	if !matched {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("%s %s returned %d: %s", method, path, response.StatusCode, strings.TrimSpace(string(detail)))
	}
	if output != nil && response.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(response.Body).Decode(output); err != nil {
			return err
		}
	} else {
		_, _ = io.Copy(io.Discard, response.Body)
	}
	return nil
}

type stateTopic struct {
	Tag string `json:"tag"`
}

type stateClientMessage struct {
	Type     string      `json:"t"`
	Protocol int         `json:"protocol,omitempty"`
	Client   string      `json:"client,omitempty"`
	ID       uint32      `json:"id,omitempty"`
	Topic    *stateTopic `json:"topic,omitempty"`
}

type stateServerMessage struct {
	Type       string          `json:"t"`
	Protocol   int             `json:"protocol,omitempty"`
	InstanceID string          `json:"instanceId,omitempty"`
	ServerTime string          `json:"serverTime,omitempty"`
	ID         uint32          `json:"id,omitempty"`
	Sequence   uint64          `json:"seq,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
	Error      string          `json:"error,omitempty"`
	Reason     string          `json:"reason,omitempty"`
	Timestamp  int64           `json:"ts,omitempty"`
}

type stateSnapshot struct {
	topic string
	data  []byte
}

type stateClient struct {
	connection       *websocket.Conn
	cancel           context.CancelFunc
	stopContextClose func() bool
	topics           map[uint32]string
	snapshots        chan stateSnapshot
	errors           chan error
	pending          map[string]stateSnapshot
	closeOnce        sync.Once
}

func openStateClient(ctx context.Context, base *url.URL) (*stateClient, error) {
	websocketURL := *base
	if websocketURL.Scheme == "https" {
		websocketURL.Scheme = "wss"
	} else {
		websocketURL.Scheme = "ws"
	}
	websocketURL.Path = strings.TrimRight(base.Path, "/") + "/api/state"
	websocketURL.RawQuery = ""
	websocketURL.Fragment = ""

	connection, response, err := websocket.DefaultDialer.DialContext(ctx, websocketURL.String(), nil)
	if err != nil {
		if response != nil {
			defer response.Body.Close()
			detail, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
			return nil, fmt.Errorf("dial state WebSocket returned %d: %s", response.StatusCode, detail)
		}
		return nil, err
	}
	stopContextClose := context.AfterFunc(ctx, func() {
		_ = connection.Close()
	})
	closeWithError := func(err error) (*stateClient, error) {
		stopContextClose()
		_ = connection.Close()
		return nil, err
	}
	connection.SetReadLimit(maxStateMessageBytes)
	if err := connection.WriteJSON(stateClientMessage{
		Type:     wire.ClientOpen,
		Protocol: wire.ProtocolVersion,
		Client:   "kiwi-code-headless",
	}); err != nil {
		return closeWithError(fmt.Errorf("send state handshake: %w", err))
	}

	messageType, payload, err := connection.ReadMessage()
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return closeWithError(fmt.Errorf("read state handshake: %w", contextErr))
		}
		return closeWithError(fmt.Errorf("read state handshake: %w", err))
	}
	if messageType != websocket.TextMessage {
		return closeWithError(errors.New("state handshake was not a text frame"))
	}
	ready, err := decodeStateServerMessage(payload)
	if err != nil {
		return closeWithError(fmt.Errorf("decode state handshake: %w", err))
	}
	if ready.Type != wire.ServerReady || ready.Protocol != wire.ProtocolVersion ||
		ready.InstanceID == "" || ready.ServerTime == "" {
		return closeWithError(fmt.Errorf("invalid state handshake: %#v", ready))
	}

	topics := map[uint32]string{
		projectsChannelID: projectsTopic,
		activityChannelID: activityTopic,
	}
	for _, subscription := range []struct {
		id  uint32
		tag string
	}{
		{id: projectsChannelID, tag: projectsTopic},
		{id: activityChannelID, tag: activityTopic},
	} {
		topic := stateTopic{Tag: subscription.tag}
		if err := connection.WriteJSON(stateClientMessage{
			Type:  wire.ClientSub,
			ID:    subscription.id,
			Topic: &topic,
		}); err != nil {
			return closeWithError(fmt.Errorf("subscribe to %s: %w", subscription.tag, err))
		}
	}

	readContext, cancel := context.WithCancel(ctx)
	result := &stateClient{
		connection:       connection,
		cancel:           cancel,
		stopContextClose: stopContextClose,
		topics:           topics,
		snapshots:        make(chan stateSnapshot, 128),
		errors:           make(chan error, 1),
		pending:          make(map[string]stateSnapshot, len(topics)),
	}
	go result.read(readContext)
	return result, nil
}

func decodeStateServerMessage(payload []byte) (stateServerMessage, error) {
	if len(payload) > maxStateMessageBytes {
		return stateServerMessage{}, fmt.Errorf("state message exceeds %d bytes", maxStateMessageBytes)
	}
	var header struct {
		Type string `json:"t"`
	}
	if err := wire.Decode(payload, &header); err != nil {
		return stateServerMessage{}, err
	}
	switch header.Type {
	case wire.ServerReady:
		var value wire.ReadyMessage
		if err := wire.DecodeDisallowUnknown(payload, &value); err != nil {
			return stateServerMessage{}, err
		}
		if value.Protocol == 0 || value.InstanceID == "" || value.ServerTime.IsZero() {
			return stateServerMessage{}, errors.New("ready message is incomplete")
		}
		return stateServerMessage{
			Type:       value.Type,
			Protocol:   value.Protocol,
			InstanceID: value.InstanceID,
			ServerTime: value.ServerTime.Format(time.RFC3339Nano),
		}, nil
	case wire.ServerSnap:
		var value wire.SnapshotMessage
		if err := wire.DecodeDisallowUnknown(payload, &value); err != nil {
			return stateServerMessage{}, err
		}
		if value.ID == 0 || value.Seq == 0 || len(value.Data) == 0 {
			return stateServerMessage{}, errors.New("snapshot message is incomplete")
		}
		return stateServerMessage{
			Type: value.Type, ID: value.ID, Sequence: value.Seq, Data: value.Data,
		}, nil
	case wire.ServerSuberr:
		var value wire.SubscribeErrorMessage
		if err := wire.DecodeDisallowUnknown(payload, &value); err != nil {
			return stateServerMessage{}, err
		}
		if value.ID == 0 || value.Error == "" {
			return stateServerMessage{}, errors.New("subscription error message is incomplete")
		}
		return stateServerMessage{Type: value.Type, ID: value.ID, Error: value.Error}, nil
	case wire.ServerSubend:
		var value wire.SubscribeEndMessage
		if err := wire.DecodeDisallowUnknown(payload, &value); err != nil {
			return stateServerMessage{}, err
		}
		if value.ID == 0 || value.Reason == "" {
			return stateServerMessage{}, errors.New("subscription end message is incomplete")
		}
		return stateServerMessage{Type: value.Type, ID: value.ID, Reason: value.Reason}, nil
	case wire.ServerPong:
		var value wire.PongMessage
		if err := wire.DecodeDisallowUnknown(payload, &value); err != nil {
			return stateServerMessage{}, err
		}
		return stateServerMessage{Type: value.Type, Timestamp: value.Timestamp}, nil
	default:
		return stateServerMessage{}, fmt.Errorf("unknown state message type %q", header.Type)
	}
}

func (c *stateClient) read(ctx context.Context) {
	defer close(c.snapshots)
	sequences := make(map[uint32]uint64, len(c.topics))
	for {
		messageType, payload, err := c.connection.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				c.reportError(fmt.Errorf("read state WebSocket: %w", err))
			}
			return
		}
		if messageType != websocket.TextMessage {
			c.reportError(errors.New("state WebSocket received a non-text frame"))
			return
		}
		message, err := decodeStateServerMessage(payload)
		if err != nil {
			c.reportError(fmt.Errorf("decode state message: %w", err))
			return
		}
		switch message.Type {
		case wire.ServerSnap:
			topic, exists := c.topics[message.ID]
			if !exists {
				c.reportError(fmt.Errorf("snapshot used unknown channel %d", message.ID))
				return
			}
			if message.Sequence <= sequences[message.ID] {
				c.reportError(fmt.Errorf("invalid snapshot sequence for channel %d: %d", message.ID, message.Sequence))
				return
			}
			sequences[message.ID] = message.Sequence
			snapshot := stateSnapshot{topic: topic, data: append([]byte(nil), message.Data...)}
			select {
			case c.snapshots <- snapshot:
			case <-ctx.Done():
				return
			}
		case wire.ServerSuberr:
			c.reportError(fmt.Errorf("state subscription %d failed: %s", message.ID, message.Error))
			return
		case wire.ServerSubend:
			c.reportError(fmt.Errorf("state subscription %d ended: %s", message.ID, message.Reason))
			return
		case wire.ServerPong:
			continue
		default:
			c.reportError(fmt.Errorf("unexpected state message type %q", message.Type))
			return
		}
	}
}

func (c *stateClient) reportError(err error) {
	select {
	case c.errors <- err:
	default:
	}
}

func (c *stateClient) waitFor(ctx context.Context, topic string, predicate func([]byte) bool) (stateSnapshot, error) {
	for {
		if snapshot, exists := c.pending[topic]; exists {
			delete(c.pending, topic)
			if predicate == nil || predicate(snapshot.data) {
				return snapshot, nil
			}
		}
		select {
		case snapshot, ok := <-c.snapshots:
			if !ok {
				select {
				case err := <-c.errors:
					return stateSnapshot{}, err
				default:
					return stateSnapshot{}, io.EOF
				}
			}
			if snapshot.topic == topic && (predicate == nil || predicate(snapshot.data)) {
				return snapshot, nil
			}
			if snapshot.topic != topic {
				c.pending[snapshot.topic] = snapshot
			}
		case err := <-c.errors:
			return stateSnapshot{}, err
		case <-ctx.Done():
			return stateSnapshot{}, ctx.Err()
		}
	}
}

func (c *stateClient) close() {
	c.closeOnce.Do(func() {
		if c.stopContextClose != nil {
			c.stopContextClose()
		}
		c.cancel()
		_ = c.connection.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "headless check complete"), time.Now().Add(time.Second))
		_ = c.connection.Close()
	})
}

func waitForEveryProjectSnapshot(ctx context.Context, clients []*stateClient, predicate func([]project.Project) bool) error {
	return waitForEveryClient(ctx, clients, func(client *stateClient) error {
		_, err := client.waitFor(ctx, projectsTopic, func(data []byte) bool {
			var projects []project.Project
			return json.Unmarshal(data, &projects) == nil && predicate(projects)
		})
		return err
	})
}

func waitForEveryActivitySnapshot(ctx context.Context, clients []*stateClient, projectID, threadID, state string) error {
	return waitForEveryClient(ctx, clients, func(client *stateClient) error {
		_, err := client.waitFor(ctx, activityTopic, func(data []byte) bool {
			return activitySnapshotContains(data, projectID, threadID, state)
		})
		return err
	})
}

func waitForEveryActivityCleared(ctx context.Context, clients []*stateClient, projectID, threadID string) error {
	return waitForEveryClient(ctx, clients, func(client *stateClient) error {
		_, err := client.waitFor(ctx, activityTopic, func(data []byte) bool {
			activities, ok := decodeActivitySnapshot(data)
			if !ok {
				return false
			}
			return !activitiesContain(activities, projectID, threadID, "")
		})
		return err
	})
}

type activityStatus struct {
	ProjectID string `json:"projectId"`
	ThreadID  string `json:"threadId"`
	State     string `json:"state"`
}

func decodeActivitySnapshot(data []byte) ([]activityStatus, bool) {
	var activities []activityStatus
	if json.Unmarshal(data, &activities) != nil {
		return nil, false
	}
	return activities, true
}

func activitySnapshotContains(data []byte, projectID, threadID, state string) bool {
	activities, ok := decodeActivitySnapshot(data)
	return ok && activitiesContain(activities, projectID, threadID, state)
}

func activitiesContain(activities []activityStatus, projectID, threadID, state string) bool {
	for _, activity := range activities {
		if activity.ProjectID == projectID && activity.ThreadID == threadID && (state == "" || activity.State == state) {
			return true
		}
	}
	return false
}

func waitForEveryClient(ctx context.Context, clients []*stateClient, wait func(*stateClient) error) error {
	type result struct {
		index int
		err   error
	}
	results := make(chan result, len(clients))
	for index, client := range clients {
		go func(index int, client *stateClient) {
			results <- result{index: index, err: wait(client)}
		}(index, client)
	}
	var firstError error
	for range clients {
		result := <-results
		if result.err != nil && firstError == nil {
			firstError = fmt.Errorf("client %d: %w", result.index+1, result.err)
		}
	}
	return firstError
}

func hasProject(projects []project.Project, projectID string) bool {
	for _, item := range projects {
		if item.ID == projectID {
			return true
		}
	}
	return false
}

func hasThread(projects []project.Project, projectID, threadID, title string) bool {
	for _, item := range projects {
		if item.ID != projectID {
			continue
		}
		for _, thread := range item.Threads {
			if thread.ID == threadID && (title == "" || thread.Title == title) {
				return true
			}
		}
	}
	return false
}

type terminalClient struct {
	connection *websocket.Conn
	mu         sync.Mutex
	output     strings.Builder
	updates    chan struct{}
	done       chan error
	closeOnce  sync.Once
}

func openTerminalClient(ctx context.Context, base *url.URL, projectID, threadID string) (*terminalClient, error) {
	websocketURL := *base
	if websocketURL.Scheme == "https" {
		websocketURL.Scheme = "wss"
	} else {
		websocketURL.Scheme = "ws"
	}
	websocketURL.Path = strings.TrimRight(base.Path, "/") + threadPath(projectID, threadID) + "/terminal"
	websocketURL.RawQuery = url.Values{"tool": {"terminal"}, "cols": {"80"}, "rows": {"24"}}.Encode()
	connection, response, err := websocket.DefaultDialer.DialContext(ctx, websocketURL.String(), nil)
	if err != nil {
		if response != nil {
			defer response.Body.Close()
			detail, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
			return nil, fmt.Errorf("dial terminal returned %d: %s", response.StatusCode, detail)
		}
		return nil, err
	}
	client := &terminalClient{
		connection: connection,
		updates:    make(chan struct{}, 1),
		done:       make(chan error, 1),
	}
	go client.read()
	return client, nil
}

func (c *terminalClient) read() {
	for {
		_, message, err := c.connection.ReadMessage()
		if err != nil {
			c.done <- err
			return
		}
		c.mu.Lock()
		c.output.Write(message)
		c.mu.Unlock()
		select {
		case c.updates <- struct{}{}:
		default:
		}
	}
}

func (c *terminalClient) writeInput(data string) error {
	payload, err := json.Marshal(map[string]string{"type": "input", "data": data})
	if err != nil {
		return err
	}
	return c.connection.WriteMessage(websocket.TextMessage, payload)
}

func (c *terminalClient) waitFor(ctx context.Context, token string) error {
	for {
		if c.contains(token) {
			return nil
		}
		select {
		case <-c.updates:
		case err := <-c.done:
			return fmt.Errorf("terminal closed before %q: %w", token, err)
		case <-ctx.Done():
			return fmt.Errorf("waiting for %q: %w", token, ctx.Err())
		}
	}
}

func (c *terminalClient) ensureAbsent(ctx context.Context, token string, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	for {
		if c.contains(token) {
			return fmt.Errorf("received output from another session: %q", token)
		}
		select {
		case <-c.updates:
		case err := <-c.done:
			return fmt.Errorf("terminal closed during isolation check: %w", err)
		case <-timer.C:
			if c.contains(token) {
				return fmt.Errorf("received output from another session: %q", token)
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *terminalClient) waitUntilClosed(ctx context.Context) error {
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("terminal remained open: %w", ctx.Err())
	}
}

func (c *terminalClient) contains(token string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Contains(c.output.String(), token)
}

func (c *terminalClient) close() {
	c.closeOnce.Do(func() {
		_ = c.connection.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "headless check complete"), time.Now().Add(time.Second))
		_ = c.connection.Close()
	})
}
