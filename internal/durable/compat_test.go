package durable

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestStopMarkerReadsPreRefactorBytes pins the on-disk marker contract: a
// marker file byte-for-byte identical to what the pre-extraction server code
// wrote must load, validate, and adopt. If this test fails, a binary upgrade
// would orphan live stop tombstones.
func TestStopMarkerReadsPreRefactorBytes(t *testing.T) {
	manager := NewStopManager(t.TempDir())
	ref := StopMarkerRef{Scope: StopScopeThread, ProjectID: "project-1", ThreadID: "thread-1"}
	path, err := manager.MarkerPath(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	// Exact serialization produced by the pre-extraction implementation.
	contents := `{"version":1,"scope":"thread","projectId":"project-1","threadId":"thread-1",` +
		`"token":"00112233445566778899aabbccddeeff","sessionNames":["kiwi-code-project-1-thread-1-terminal",` +
		`"kiwi-code-project-1-thread-1-tools"],"createdAt":"2025-01-02T03:04:05Z"}` + "\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	marker, found, err := manager.ReadThread("project-1", "thread-1")
	if err != nil || !found {
		t.Fatalf("read pre-refactor marker: found=%t err=%v", found, err)
	}
	if marker.Version != 1 || marker.Scope != StopScopeThread ||
		marker.ProjectID != "project-1" || marker.ThreadID != "thread-1" ||
		marker.Committed {
		t.Fatalf("marker = %#v", marker)
	}
	if len(marker.SessionNames) != 2 || marker.SessionNames[0] != "kiwi-code-project-1-thread-1-terminal" {
		t.Fatalf("marker session names = %v", marker.SessionNames)
	}
	if !marker.CreatedAt.Equal(time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("marker created at = %v", marker.CreatedAt)
	}

	lease, found, err := manager.AcquireExisting(ref)
	if err != nil || !found || lease == nil {
		t.Fatalf("adopt pre-refactor marker: found=%t err=%v", found, err)
	}
	if !lease.Adopted() {
		t.Fatal("pre-refactor marker was not adopted")
	}
	if err := lease.Rollback(); err != nil {
		t.Fatal(err)
	}
}
