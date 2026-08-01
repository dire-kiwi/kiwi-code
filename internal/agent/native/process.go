// Package native runs coding agents as headless subprocesses (no tmux, no
// PTY): line-oriented stdio pumped into a broadcast broker, graceful stop
// with interrupt-then-kill, and a generic per-thread process set shared by
// every native agent implementation.
package native

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/broadcast"
	"github.com/dire-kiwi/kiwi-code/internal/thread"
	"path/filepath"
)

// Key identifies the thread a native process belongs to.
type Key = thread.Key

type Spec struct {
	DisplayName         string
	EndedMessage        string
	UnexpectedMessage   string
	WriteAfterExitError string
	StopTimeout         time.Duration
}

type Core struct {
	Key      Key
	Spec     Spec
	command  *exec.Cmd
	stdin    io.WriteCloser
	Events   *broadcast.Broker[[]byte]
	Done     chan struct{}
	writeMu  sync.Mutex
	exitMu   sync.RWMutex
	exitText string
	Request  atomic.Uint64
	stopping atomic.Bool
}

func StopOnContext(ctx context.Context, once *sync.Once, stop func()) {
	if ctx == nil || ctx.Done() == nil {
		return
	}
	once.Do(func() {
		go func() {
			<-ctx.Done()
			stop()
		}()
	})
}

func Collect[P any](processes map[Key]P, include func(Key) bool) []P {
	selected := make([]P, 0, len(processes))
	for key, process := range processes {
		if include(key) {
			selected = append(selected, process)
		}
	}
	return selected
}

func StopSet[P any](processes []P, stop func(P) error) error {
	stopErrors := make([]error, 0, len(processes))
	for _, process := range processes {
		stopErrors = append(stopErrors, stop(process))
	}
	return errors.Join(stopErrors...)
}

func StartCommand(key Key, spec Spec, command *exec.Cmd) (*Core, io.ReadCloser, io.ReadCloser, error) {
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open native %s input: %w", spec.DisplayName, err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, fmt.Errorf("open native %s output: %w", spec.DisplayName, err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, fmt.Errorf("open native %s diagnostics: %w", spec.DisplayName, err)
	}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return nil, nil, nil, fmt.Errorf("start native %s: %w", spec.DisplayName, err)
	}
	return &Core{
		Key:     key,
		Spec:    spec,
		command: command,
		stdin:   stdin,
		Events:  broadcast.NewBroker[[]byte](broadcast.DefaultMaxPending * 2),
		Done:    make(chan struct{}),
	}, stdout, stderr, nil
}

func (p *Core) ReadOutput(output io.Reader, publish func([]byte)) {
	go func() {
		reader := bufio.NewReader(output)
		for {
			line, err := reader.ReadBytes('\n')
			line = bytes.TrimSuffix(line, []byte{'\n'})
			line = bytes.TrimSuffix(line, []byte{'\r'})
			if len(line) > 0 {
				publish(line)
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					log.Printf("read native %s output: project=%q thread=%q error=%v", p.Spec.DisplayName, p.Key.ProjectID, p.Key.ThreadID, err)
				}
				return
			}
		}
	}()
}

func (p *Core) ReadDiagnostics(output io.Reader) {
	go func() {
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 64*1024), 1<<20)
		for scanner.Scan() {
			if line := strings.TrimSpace(scanner.Text()); line != "" {
				log.Printf("native %s: project=%q thread=%q %s", p.Spec.DisplayName, p.Key.ProjectID, p.Key.ThreadID, line)
			}
		}
		if err := scanner.Err(); err != nil {
			log.Printf("read native %s diagnostics: project=%q thread=%q error=%v", p.Spec.DisplayName, p.Key.ProjectID, p.Key.ThreadID, err)
		}
	}()
}

// run keeps provider state ahead of the exit message and manager retirement.
func (p *Core) Run(providerExit func(string), onExit func()) {
	go func() {
		err := p.command.Wait()
		message := p.Spec.EndedMessage
		if err != nil && !p.stopping.Load() {
			message = p.Spec.UnexpectedMessage
			log.Printf("native %s exited: project=%q thread=%q error=%v", p.Spec.DisplayName, p.Key.ProjectID, p.Key.ThreadID, err)
		}
		providerExit(message)
		p.exitMu.Lock()
		p.exitText = message
		p.exitMu.Unlock()
		close(p.Done)
		onExit()
	}()
}

func (p *Core) WriteLine(payload []byte) error {
	payload = append(bytes.Clone(payload), '\n')
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if channelClosed(p.Done) {
		return errors.New(p.Spec.WriteAfterExitError)
	}
	_, err := p.stdin.Write(payload)
	return err
}

func (p *Core) ExitMessage() string {
	p.exitMu.RLock()
	defer p.exitMu.RUnlock()
	if p.exitText == "" {
		return p.Spec.EndedMessage
	}
	return p.exitText
}

func (p *Core) Stop() error {
	if channelClosed(p.Done) {
		return nil
	}
	p.stopping.Store(true)
	_ = p.stdin.Close()
	if p.command.Process != nil {
		_ = p.command.Process.Signal(os.Interrupt)
	}
	timer := time.NewTimer(p.Spec.StopTimeout)
	defer timer.Stop()
	select {
	case <-p.Done:
		return nil
	case <-timer.C:
		if p.command.Process != nil {
			if err := p.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				return err
			}
		}
		<-p.Done
		return nil
	}
}

func channelClosed(channel <-chan struct{}) bool {
	select {
	case <-channel:
		return true
	default:
		return false
	}
}

// ManagerCore is the per-thread process bookkeeping shared by every native
// agent manager: one mutex guarding a process map keyed by thread. Compound
// operations (get-or-start, restart) lock Mu directly so per-agent state
// guarded by the same mutex stays consistent.
type ManagerCore[P any] struct {
	Mu        sync.Mutex
	Processes map[Key]P
}

func NewManagerCore[P any]() ManagerCore[P] {
	return ManagerCore[P]{Processes: make(map[Key]P)}
}

// StopThread stops the thread's process if one is running.
func (m *ManagerCore[P]) StopThread(key Key, stop func(P) error) error {
	m.Mu.Lock()
	process, found := m.Processes[key]
	m.Mu.Unlock()
	if !found {
		return nil
	}
	return stop(process)
}

// StopProject stops every process belonging to the project, joining errors.
func (m *ManagerCore[P]) StopProject(projectID string, stop func(P) error) error {
	m.Mu.Lock()
	processes := Collect(m.Processes, func(key Key) bool {
		return key.ProjectID == projectID
	})
	m.Mu.Unlock()
	return StopSet(processes, stop)
}

// StopAll stops every process, logging failures with the agent's label.
func (m *ManagerCore[P]) StopAll(label string, stop func(P) error, key func(P) Key) {
	m.Mu.Lock()
	processes := Collect(m.Processes, func(Key) bool { return true })
	m.Mu.Unlock()
	for _, process := range processes {
		if err := stop(process); err != nil {
			k := key(process)
			log.Printf("stop native %s: project=%q thread=%q error=%v", label, k.ProjectID, k.ThreadID, err)
		}
	}
}

// Remove stops the thread's process, forgets it, and deletes its session
// directory under sessionRoot.
func (m *ManagerCore[P]) Remove(key Key, sessionRoot string, stop func(P) error, forget func()) error {
	stopErr := m.StopThread(key, stop)
	m.Mu.Lock()
	delete(m.Processes, key)
	if forget != nil {
		forget()
	}
	m.Mu.Unlock()
	removeErr := os.RemoveAll(filepath.Join(sessionRoot, key.ProjectID, key.ThreadID))
	return errors.Join(stopErr, removeErr)
}

// RemoveProject stops the project's processes and deletes its session
// directory subtree under sessionRoot.
func (m *ManagerCore[P]) RemoveProject(projectID, sessionRoot string, stop func(P) error, forget func()) error {
	stopErr := m.StopProject(projectID, stop)
	if forget != nil {
		m.Mu.Lock()
		forget()
		m.Mu.Unlock()
	}
	removeErr := os.RemoveAll(filepath.Join(sessionRoot, projectID))
	return errors.Join(stopErr, removeErr)
}

// NewCore assembles a core directly. StartCommand is the production path;
// NewCore exists for adapters and tests that need a core without spawning a
// command. A nil events broker gets a default one.
func NewCore(key Key, spec Spec, stdin io.WriteCloser, events *broadcast.Broker[[]byte]) *Core {
	if events == nil {
		events = broadcast.NewBroker[[]byte](broadcast.DefaultMaxPending * 2)
	}
	return &Core{Key: key, Spec: spec, stdin: stdin, Events: events, Done: make(chan struct{})}
}
