package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func TestNormalizeBrowserStateSnapshotNoSession(t *testing.T) {
	snapshot := normalizeBrowserStateSnapshot(json.RawMessage(`{
		"message":"Headless Chrome browser session is not running.",
		"status":{
			"reachable":true,
			"presentation":"stream",
			"capabilities":{
				"nativeView":false,
				"interactiveStream":true,
				"preview":true,
				"recording":true
			}
		},
		"backend":"headless-chrome",
		"running":false,
		"pages":[],
		"currentTargetId":null,
		"recording":null,
		"recordings":[]
	}`))
	if snapshot.Backend != "headless-chrome" || snapshot.Presentation != "stream" {
		t.Fatalf("identity = backend %q presentation %q", snapshot.Backend, snapshot.Presentation)
	}
	if snapshot.Reachable == nil || !*snapshot.Reachable || snapshot.Running == nil || *snapshot.Running {
		t.Fatalf("reachability = reachable %#v running %#v", snapshot.Reachable, snapshot.Running)
	}
	if snapshot.CurrentTargetID != nil || snapshot.Current != nil || snapshot.Recording != nil ||
		len(snapshot.Pages) != 0 || len(snapshot.Recordings) != 0 || snapshot.Error != "" {
		t.Fatalf("no-session snapshot = %#v", snapshot)
	}
	assertBrowserSnapshotShape(t, snapshot, true)
}

func TestBrowserStateSnapshotUnavailableProvider(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{projects: store}
	snapshot, err := server.readBrowserStateSnapshot(
		context.Background(),
		item.ID,
		item.Threads[0].ID,
		[]string{"http://localhost:4000"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Error != "Browser provider is unavailable." {
		t.Fatalf("unavailable error = %q", snapshot.Error)
	}
	if snapshot.Pages == nil || snapshot.Recordings == nil {
		t.Fatalf("unavailable collections are nil: %#v", snapshot)
	}
	assertBrowserSnapshotShape(t, snapshot, false)
}

func TestBrowserRecordingSnapshotIncludesActiveAndCompletedWithoutDuplicates(t *testing.T) {
	snapshot := normalizeBrowserStateSnapshot(json.RawMessage(`{
		"recording":{
			"id":"rec-active",
			"state":"recording",
			"targetId":"page-1",
			"title":"Active recording",
			"startedAt":"2026-07-26T00:00:00Z"
		},
		"recordings":[
			{
				"id":"rec-active",
				"state":"recording",
				"targetId":"page-1",
				"title":"Active recording",
				"startedAt":"2026-07-26T00:00:00Z"
			},
			{
				"id":"rec-complete",
				"state":"completed",
				"targetId":"page-2",
				"title":"Completed recording",
				"startedAt":"2026-07-25T00:00:00Z",
				"finishedAt":"2026-07-25T00:01:00Z"
			}
		]
	}`))
	recordings := browserStateRecordingList(snapshot)
	if len(recordings) != 2 || recordings[0].ID != "rec-active" || recordings[1].ID != "rec-complete" {
		t.Fatalf("recording list = %#v", recordings)
	}
}

func TestNormalizeBrowserStateSnapshotSanitizesProviderFields(t *testing.T) {
	snapshot := normalizeBrowserStateSnapshot(json.RawMessage(`{
		"backend":"electron",
		"error":"provider-controlled secret",
		"pages":[
			{"id":"","title":"invalid"},
			{"id":"page-1","title":"Example","url":"https://example.com"}
		],
		"recording":{"id":"bad","state":"unexpected","targetId":"page-1","title":"Bad","startedAt":"now"},
		"recordings":[{"id":"also-bad","state":"unexpected","targetId":"page-1","title":"Bad","startedAt":"now"}],
		"unknown":{"secret":"not forwarded"}
	}`))
	if snapshot.Error != "" || len(snapshot.Pages) != 1 || snapshot.Pages[0].ID != "page-1" ||
		snapshot.Recording != nil || len(snapshot.Recordings) != 0 {
		t.Fatalf("sanitized snapshot = %#v", snapshot)
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil {
		t.Fatal(err)
	}
	if _, exists := object["unknown"]; exists {
		t.Fatalf("snapshot forwarded an unknown field: %s", payload)
	}
}

func assertBrowserSnapshotShape(t *testing.T, snapshot browserStateSnapshot, expectReachability bool) {
	t.Helper()
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"backend", "presentation", "capabilities", "pages", "currentTargetId", "recording", "recordings",
	} {
		if _, exists := object[required]; !exists {
			t.Fatalf("snapshot omitted %q: %s", required, payload)
		}
	}
	for _, nullable := range []string{"currentTargetId", "recording"} {
		if string(object[nullable]) != "null" {
			t.Fatalf("%s = %s, want null", nullable, object[nullable])
		}
	}
	_, reachable := object["reachable"]
	_, running := object["running"]
	if expectReachability != (reachable && running) {
		t.Fatalf("optional reachability fields = reachable %t running %t: %s", reachable, running, payload)
	}
	if _, exists := object["current"]; exists {
		t.Fatalf("snapshot encoded a null current page instead of omitting it: %s", payload)
	}
}
