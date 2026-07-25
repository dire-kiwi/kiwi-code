// Package browserhost supervises the private Node/Chrome process used by the
// server-managed headless browser backend.
package browserhost

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/browsercontrol"
)

//go:embed assets/browser-host.cjs assets/browser-host.cjs.LEGAL.txt
var assets embed.FS

const (
	maxRPCLineBytes = 25 << 20
	requestTimeout  = 75 * time.Second
	shutdownTimeout = 3 * time.Second
)

type Options struct {
	ChromeBinary        string
	ProtectedOrigins    []string
	RecordingsDirectory string
}

type Provider struct {
	options Options

	startMu       sync.Mutex
	writeMu       sync.Mutex
	mu            sync.Mutex
	cmd           *exec.Cmd
	stdin         io.WriteCloser
	pending       map[int64]chan rpcResponse
	active        map[string]struct{}
	done          chan struct{}
	tempDir       string
	recordingsDir string
	nextID        atomic.Int64
	closed        bool
}

type wireRequest struct {
	ID     int64      `json:"id"`
	Action wireAction `json:"action"`
}

type wireAction struct {
	ProjectID        string          `json:"projectId"`
	ThreadID         string          `json:"threadId"`
	Operation        string          `json:"operation"`
	Params           json.RawMessage `json:"params"`
	ProtectedOrigins []string        `json:"protectedOrigins,omitempty"`
}

type rpcResponse struct {
	ID     int64           `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  struct {
		Code string `json:"code"`
	} `json:"error"`
}

func New(options Options) *Provider {
	return &Provider{
		options:       options,
		pending:       make(map[int64]chan rpcResponse),
		active:        make(map[string]struct{}),
		recordingsDir: options.RecordingsDirectory,
	}
}

func (p *Provider) Action(ctx context.Context, request browsercontrol.Request) (json.RawMessage, error) {
	key := request.ProjectID + "\x00" + request.ThreadID
	p.mu.Lock()
	_, active := p.active[key]
	started := p.cmd != nil
	p.mu.Unlock()
	if !started && !active {
		switch request.Operation {
		case "session.status":
			if !p.hasCompletedRecordings() {
				return noSessionResult(request.Operation), nil
			}
		case "session.disconnect", "session.stop":
			return noSessionResult(request.Operation), nil
		case "preview":
			return nil, browsercontrol.ErrFrameUnavailable
		case "stream.input":
			return nil, browsercontrol.ErrSessionNotFound
		}
	}
	requestContext, cancelRequest := context.WithTimeout(ctx, requestTimeout)
	defer cancelRequest()
	if len(request.Params) == 0 {
		request.Params = json.RawMessage(`{}`)
	}
	if len(request.Params) > browsercontrol.MaxRequestBytes {
		return nil, browsercontrol.ErrRequestTooLarge
	}
	if err := p.ensureStarted(); err != nil {
		return nil, fmt.Errorf("%w: headless browser host could not start", browsercontrol.ErrUnavailable)
	}

	id := p.nextID.Add(1)
	response := make(chan rpcResponse, 1)
	p.mu.Lock()
	if p.closed || p.stdin == nil {
		p.mu.Unlock()
		return nil, browsercontrol.ErrUnavailable
	}
	p.pending[id] = response
	p.mu.Unlock()

	encoded, err := json.Marshal(wireRequest{ID: id, Action: wireAction{
		ProjectID:        request.ProjectID,
		ThreadID:         request.ThreadID,
		Operation:        request.Operation,
		Params:           request.Params,
		ProtectedOrigins: append([]string(nil), request.ProtectedOrigins...),
	}})
	if err != nil || len(encoded) > browsercontrol.MaxRequestBytes {
		p.removePending(id)
		if err != nil {
			return nil, browsercontrol.ErrUnavailable
		}
		return nil, browsercontrol.ErrRequestTooLarge
	}

	p.writeMu.Lock()
	p.mu.Lock()
	stdin := p.stdin
	p.mu.Unlock()
	if stdin != nil {
		_, err = stdin.Write(append(encoded, '\n'))
	} else {
		err = io.ErrClosedPipe
	}
	p.writeMu.Unlock()
	if err != nil {
		p.removePending(id)
		return nil, browsercontrol.ErrUnavailable
	}

	select {
	case value, open := <-response:
		if !open {
			return nil, browsercontrol.ErrUnavailable
		}
		result, responseErr := classifyResponse(value)
		if responseErr == nil {
			p.mu.Lock()
			if request.Operation == "session.stop" {
				delete(p.active, key)
			} else if request.Operation != "session.status" && request.Operation != "session.disconnect" && request.Operation != "preview" && request.Operation != "stream.input" && request.Operation != "recording.status" && request.Operation != "recording.delete" {
				p.active[key] = struct{}{}
			}
			p.mu.Unlock()
		}
		return result, responseErr
	case <-requestContext.Done():
		p.removePending(id)
		return nil, fmt.Errorf("%w: request cancelled", browsercontrol.ErrUnavailable)
	}
}

func (p *Provider) hasCompletedRecordings() bool {
	if p.recordingsDir == "" {
		return false
	}
	matches, err := filepath.Glob(filepath.Join(p.recordingsDir, "rec-*.json"))
	return err == nil && len(matches) > 0
}

func noSessionResult(operation string) json.RawMessage {
	message := "Headless Chrome browser session is not running."
	if operation == "session.disconnect" {
		message = "No headless browser connection was active."
	} else if operation == "session.stop" {
		message = "No headless Chrome browser session was running."
	}
	value := map[string]any{
		"message": message,
		"status": map[string]any{
			"endpoint": "", "reachable": true, "product": "Headless Chrome", "protocolVersion": "1.3",
			"pages": 0, "currentTargetId": nil, "owned": true, "presentation": "stream",
			"capabilities": map[string]bool{"nativeView": false, "interactiveStream": true, "preview": true, "recording": true},
		},
		"backend": "headless-chrome", "presentation": "stream",
		"capabilities": map[string]bool{"nativeView": false, "interactiveStream": true, "preview": true, "recording": true},
		"running":      false, "pages": []any{}, "pageList": []any{}, "currentTargetId": nil,
		"recording": nil, "recordings": []any{},
	}
	if operation == "session.stop" {
		value["stopped"] = false
	}
	encoded, _ := json.Marshal(value)
	return encoded
}

func classifyResponse(response rpcResponse) (json.RawMessage, error) {
	if response.OK {
		if len(response.Result) == 0 || !json.Valid(response.Result) || int64(len(response.Result)) > browsercontrol.MaxResponseBytes {
			return nil, browsercontrol.ErrInvalidResponse
		}
		return append(json.RawMessage(nil), response.Result...), nil
	}
	switch response.Error.Code {
	case "session_not_found":
		return nil, browsercontrol.ErrSessionNotFound
	case "frame_unavailable", "preview_not_ready":
		return nil, browsercontrol.ErrFrameUnavailable
	case "chrome_unavailable":
		return nil, browsercontrol.ErrUnavailable
	}
	if _, ok := browsercontrol.SanitizedOperationError(response.Error.Code); ok {
		return nil, &browsercontrol.OperationError{Code: response.Error.Code}
	}
	return nil, browsercontrol.ErrProvider
}

func (p *Provider) ensureStarted() error {
	p.startMu.Lock()
	defer p.startMu.Unlock()

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errors.New("browser host is closed")
	}
	if p.cmd != nil {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	node, err := exec.LookPath("node")
	if err != nil {
		return err
	}
	temporaryDirectory, err := os.MkdirTemp("", "kiwi-code-browser-host-")
	if err != nil {
		return err
	}
	if err := os.Chmod(temporaryDirectory, 0o700); err != nil {
		_ = os.RemoveAll(temporaryDirectory)
		return err
	}
	source, err := assets.ReadFile("assets/browser-host.cjs")
	if err != nil {
		_ = os.RemoveAll(temporaryDirectory)
		return err
	}
	script := filepath.Join(temporaryDirectory, "browser-host.cjs")
	if err := os.WriteFile(script, source, 0o600); err != nil {
		_ = os.RemoveAll(temporaryDirectory)
		return err
	}
	legal, err := assets.ReadFile("assets/browser-host.cjs.LEGAL.txt")
	if err != nil {
		_ = os.RemoveAll(temporaryDirectory)
		return err
	}
	if err := os.WriteFile(script+".LEGAL.txt", legal, 0o600); err != nil {
		_ = os.RemoveAll(temporaryDirectory)
		return err
	}

	origins, _ := json.Marshal(p.options.ProtectedOrigins)
	if p.recordingsDir == "" {
		p.recordingsDir = filepath.Join(temporaryDirectory, "recordings")
	}
	if err := os.MkdirAll(p.recordingsDir, 0o700); err != nil {
		_ = os.RemoveAll(temporaryDirectory)
		return err
	}
	if err := os.Chmod(p.recordingsDir, 0o700); err != nil {
		_ = os.RemoveAll(temporaryDirectory)
		return err
	}
	command := exec.Command(node, script)
	configureProcess(command)
	command.Env = minimalEnvironment(p.options.ChromeBinary, string(origins), p.recordingsDir)
	stdin, err := command.StdinPipe()
	if err != nil {
		_ = os.RemoveAll(temporaryDirectory)
		return err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = os.RemoveAll(temporaryDirectory)
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		_ = os.RemoveAll(temporaryDirectory)
		return err
	}
	if err := command.Start(); err != nil {
		_ = os.RemoveAll(temporaryDirectory)
		return err
	}

	p.mu.Lock()
	p.cmd = command
	p.stdin = stdin
	p.done = make(chan struct{})
	p.tempDir = temporaryDirectory
	p.mu.Unlock()
	go p.readResponses(command, stdout)
	go p.readDiagnostics(stderr)
	return nil
}

func minimalEnvironment(chrome, origins, recordingsDirectory string) []string {
	names := []string{"HOME", "LANG", "LC_ALL", "PATH", "TMPDIR", "TEMP", "TMP", "SYSTEMROOT", "PROGRAMFILES", "PROGRAMFILES(X86)"}
	environment := make([]string, 0, len(names)+3)
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			environment = append(environment, name+"="+value)
		}
	}
	environment = append(environment,
		"KIWI_CODE_PROTECTED_ORIGINS="+origins,
		"KIWI_CODE_BROWSER_RECORDINGS_DIR="+recordingsDirectory,
	)
	if chrome != "" {
		environment = append(environment, "KIWI_CODE_CHROME_BIN="+chrome)
	}
	return environment
}

func (p *Provider) readResponses(command *exec.Cmd, stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), maxRPCLineBytes)
	for scanner.Scan() {
		var response rpcResponse
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil || response.ID <= 0 {
			continue
		}
		p.mu.Lock()
		waiting := p.pending[response.ID]
		delete(p.pending, response.ID)
		p.mu.Unlock()
		if waiting != nil {
			waiting <- response
		}
	}
	_ = command.Wait()
	p.processExited(command)
}

func (p *Provider) readDiagnostics(stderr io.Reader) {
	scanner := bufio.NewScanner(io.LimitReader(stderr, 64<<10))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			log.Printf("headless browser host: %s", redactDiagnostic(line))
		}
	}
}

func redactDiagnostic(line string) string {
	if len(line) > 500 {
		line = line[:500] + "…"
	}
	// Chrome diagnostics can contain profile paths and URLs. Keep only a
	// category-level message rather than forwarding the content.
	lower := strings.ToLower(line)
	if strings.Contains(lower, "error") || strings.Contains(lower, "failed") {
		return "Chrome reported a startup/runtime error (details redacted)"
	}
	return "Chrome emitted a diagnostic message (details redacted)"
}

func (p *Provider) processExited(command *exec.Cmd) {
	p.mu.Lock()
	if p.cmd != command {
		p.mu.Unlock()
		return
	}
	for id, waiting := range p.pending {
		delete(p.pending, id)
		close(waiting)
	}
	clear(p.active)
	p.cmd = nil
	p.stdin = nil
	if p.done != nil {
		close(p.done)
	}
	temporaryDirectory := p.tempDir
	p.tempDir = ""
	p.mu.Unlock()
	if temporaryDirectory != "" {
		_ = os.RemoveAll(temporaryDirectory)
	}
}

func (p *Provider) removePending(id int64) {
	p.mu.Lock()
	delete(p.pending, id)
	p.mu.Unlock()
}

func (p *Provider) Close(ctx context.Context) error {
	p.startMu.Lock()
	defer p.startMu.Unlock()
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	command, stdin, done, temporaryDirectory := p.cmd, p.stdin, p.done, p.tempDir
	p.mu.Unlock()
	if command == nil {
		if temporaryDirectory != "" {
			_ = os.RemoveAll(temporaryDirectory)
		}
		return nil
	}

	p.writeMu.Lock()
	if stdin != nil {
		_, _ = io.WriteString(stdin, `{"type":"shutdown"}`+"\n")
		_ = stdin.Close()
	}
	p.writeMu.Unlock()

	timer := time.NewTimer(shutdownTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-ctx.Done():
		_ = killProcess(command)
		<-done
		return ctx.Err()
	case <-timer.C:
		_ = killProcess(command)
		<-done
	}
	return nil
}
