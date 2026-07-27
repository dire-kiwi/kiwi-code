package server

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
)

type nativeProcessKey struct {
	ProjectID string
	ThreadID  string
}

// Preserve the existing name while Pi and Claude share the same identity type.
type piNativeProcessKey = nativeProcessKey

type nativeProcessSpec struct {
	displayName         string
	endedMessage        string
	unexpectedMessage   string
	writeAfterExitError string
	stopTimeout         time.Duration
}

type nativeProcessCore struct {
	key      nativeProcessKey
	spec     nativeProcessSpec
	command  *exec.Cmd
	stdin    io.WriteCloser
	events   *broadcast.Broker[[]byte]
	done     chan struct{}
	writeMu  sync.Mutex
	exitMu   sync.RWMutex
	exitText string
	request  atomic.Uint64
	stopping atomic.Bool
}

func stopNativeProcessesOnContext(ctx context.Context, once *sync.Once, stop func()) {
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

func collectNativeProcesses[T any](processes map[nativeProcessKey]*T, include func(nativeProcessKey) bool) []*T {
	selected := make([]*T, 0, len(processes))
	for key, process := range processes {
		if include(key) {
			selected = append(selected, process)
		}
	}
	return selected
}

func stopNativeProcessSet[T any](processes []*T, stop func(*T) error) error {
	stopErrors := make([]error, 0, len(processes))
	for _, process := range processes {
		stopErrors = append(stopErrors, stop(process))
	}
	return errors.Join(stopErrors...)
}

func startNativeCommand(key nativeProcessKey, spec nativeProcessSpec, command *exec.Cmd) (*nativeProcessCore, io.ReadCloser, io.ReadCloser, error) {
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open native %s input: %w", spec.displayName, err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, fmt.Errorf("open native %s output: %w", spec.displayName, err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, fmt.Errorf("open native %s diagnostics: %w", spec.displayName, err)
	}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return nil, nil, nil, fmt.Errorf("start native %s: %w", spec.displayName, err)
	}
	return &nativeProcessCore{
		key:     key,
		spec:    spec,
		command: command,
		stdin:   stdin,
		events:  broadcast.NewBroker[[]byte](broadcast.DefaultMaxPending * 2),
		done:    make(chan struct{}),
	}, stdout, stderr, nil
}

func (p *nativeProcessCore) readOutput(output io.Reader, publish func([]byte)) {
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
					log.Printf("read native %s output: project=%q thread=%q error=%v", p.spec.displayName, p.key.ProjectID, p.key.ThreadID, err)
				}
				return
			}
		}
	}()
}

func (p *nativeProcessCore) readDiagnostics(output io.Reader) {
	go func() {
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 64*1024), 1<<20)
		for scanner.Scan() {
			if line := strings.TrimSpace(scanner.Text()); line != "" {
				log.Printf("native %s: project=%q thread=%q %s", p.spec.displayName, p.key.ProjectID, p.key.ThreadID, line)
			}
		}
		if err := scanner.Err(); err != nil {
			log.Printf("read native %s diagnostics: project=%q thread=%q error=%v", p.spec.displayName, p.key.ProjectID, p.key.ThreadID, err)
		}
	}()
}

// run keeps provider state ahead of the exit message and manager retirement.
func (p *nativeProcessCore) run(providerExit func(string), onExit func()) {
	go func() {
		err := p.command.Wait()
		message := p.spec.endedMessage
		if err != nil && !p.stopping.Load() {
			message = p.spec.unexpectedMessage
			log.Printf("native %s exited: project=%q thread=%q error=%v", p.spec.displayName, p.key.ProjectID, p.key.ThreadID, err)
		}
		providerExit(message)
		p.exitMu.Lock()
		p.exitText = message
		p.exitMu.Unlock()
		close(p.done)
		onExit()
	}()
}

func (p *nativeProcessCore) writeLine(payload []byte) error {
	payload = append(bytes.Clone(payload), '\n')
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if channelClosed(p.done) {
		return errors.New(p.spec.writeAfterExitError)
	}
	_, err := p.stdin.Write(payload)
	return err
}

func (p *nativeProcessCore) exitMessage() string {
	p.exitMu.RLock()
	defer p.exitMu.RUnlock()
	if p.exitText == "" {
		return p.spec.endedMessage
	}
	return p.exitText
}

func (p *nativeProcessCore) stop() error {
	if channelClosed(p.done) {
		return nil
	}
	p.stopping.Store(true)
	_ = p.stdin.Close()
	if p.command.Process != nil {
		_ = p.command.Process.Signal(os.Interrupt)
	}
	timer := time.NewTimer(p.spec.stopTimeout)
	defer timer.Stop()
	select {
	case <-p.done:
		return nil
	case <-timer.C:
		if p.command.Process != nil {
			if err := p.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				return err
			}
		}
		<-p.done
		return nil
	}
}
