package server

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

var terminalConnectionSequence atomic.Uint64

type terminalConnectionDiagnostics struct {
	enabled   bool
	id        uint64
	projectID string
	threadID  string
	tool      string
	started   time.Time
	mu        sync.Mutex
	last      time.Time
	finished  bool
}

func newTerminalConnectionDiagnostics(r *http.Request, projectID, threadID, tool string) *terminalConnectionDiagnostics {
	enabled := r.URL.Query().Get("diagnostics") == "1" || os.Getenv("KIWI_CODE_TERMINAL_DIAGNOSTICS") == "1"
	now := time.Now()
	return &terminalConnectionDiagnostics{
		enabled:   enabled,
		id:        terminalConnectionSequence.Add(1),
		projectID: projectID,
		threadID:  threadID,
		tool:      tool,
		started:   now,
		last:      now,
	}
}

func (d *terminalConnectionDiagnostics) mark(phase string) {
	if d == nil || !d.enabled {
		return
	}
	now := time.Now()
	d.mu.Lock()
	if d.finished {
		d.mu.Unlock()
		return
	}
	delta := now.Sub(d.last)
	total := now.Sub(d.started)
	d.last = now
	d.mu.Unlock()
	log.Printf(
		"terminal timing: connection=%s project=%q thread=%q tool=%q phase=%q delta=%s total=%s",
		strconv.FormatUint(d.id, 10), d.projectID, d.threadID, d.tool, phase,
		delta.Round(time.Microsecond), total.Round(time.Microsecond),
	)
}

func (d *terminalConnectionDiagnostics) finish() {
	if d == nil || !d.enabled {
		return
	}
	now := time.Now()
	d.mu.Lock()
	if d.finished {
		d.mu.Unlock()
		return
	}
	d.finished = true
	total := now.Sub(d.started)
	d.mu.Unlock()
	log.Printf(
		"terminal timing: connection=%s project=%q thread=%q tool=%q phase=%q total=%s",
		strconv.FormatUint(d.id, 10), d.projectID, d.threadID, d.tool, "closed",
		total.Round(time.Microsecond),
	)
}
