package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/dire-kiwi/kiwi-code/internal/browsercontrol"
	"github.com/dire-kiwi/kiwi-code/internal/project"
	"github.com/dire-kiwi/kiwi-code/internal/wire"
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
				"preview":true
			}
		},
		"backend":"headless-chrome",
		"running":false,
		"pages":[],
		"currentTargetId":null
	}`))
	if snapshot.Backend != "headless-chrome" || snapshot.Presentation != "stream" {
		t.Fatalf("identity = backend %q presentation %q", snapshot.Backend, snapshot.Presentation)
	}
	if snapshot.Reachable == nil || !*snapshot.Reachable || snapshot.Running == nil || *snapshot.Running {
		t.Fatalf("reachability = reachable %#v running %#v", snapshot.Reachable, snapshot.Running)
	}
	if snapshot.CurrentTargetID != nil || snapshot.Current != nil || len(snapshot.Pages) != 0 || snapshot.Error != "" {
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
	if snapshot.Pages == nil {
		t.Fatalf("unavailable pages are nil: %#v", snapshot)
	}
	assertBrowserSnapshotShape(t, snapshot, false)
}

func TestBrowserStatusTopicPublishesNormalizedStatus(t *testing.T) {
	status := json.RawMessage(`{
		"backend":"headless-chrome",
		"presentation":"stream",
		"capabilities":{"nativeView":false,"interactiveStream":true,"preview":true},
		"reachable":true,
		"running":true,
		"pages":[{"id":"page-1","title":"Example","url":"https://example.com"}],
		"currentTargetId":"page-1"
	}`)
	provider := browserActionTestProvider{action: func(_ context.Context, request browsercontrol.Request) (json.RawMessage, error) {
		if request.Operation != "session.status" {
			return nil, browsercontrol.ErrProvider
		}
		return status, nil
	}}
	application, item, thread := newBrowserServerTestFixture(t, provider)
	connection, closeServer := openStateTestSocket(t, application)
	defer closeServer()
	defer connection.Close()
	if ready := readStateTestMessage(t, connection); ready.Type != wire.ServerReady {
		t.Fatalf("ready = %#v", ready)
	}

	writeStateTestMessage(t, connection, map[string]any{
		"t":  wire.ClientSub,
		"id": uint32(1),
		"topic": map[string]any{
			"tag":       stateTopicBrowserStatus,
			"projectId": item.ID,
			"threadId":  thread.ID,
		},
	})
	message := readStateTestMessage(t, connection)
	if message.Type != wire.ServerSnap || message.ID != 1 || message.Seq != 1 {
		t.Fatalf("browser status snapshot message = %#v", message)
	}
	var snapshot browserStateSnapshot
	if err := json.Unmarshal(message.Data, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Pages) != 1 || snapshot.Pages[0].ID != "page-1" {
		t.Fatalf("browser.status pages = %#v", snapshot.Pages)
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
		"unknown":{"secret":"not forwarded"}
	}`))
	if snapshot.Error != "" || len(snapshot.Pages) != 1 || snapshot.Pages[0].ID != "page-1" {
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
		"backend", "presentation", "capabilities", "pages", "currentTargetId",
	} {
		if _, exists := object[required]; !exists {
			t.Fatalf("snapshot omitted %q: %s", required, payload)
		}
	}
	if string(object["currentTargetId"]) != "null" {
		t.Fatalf("currentTargetId = %s, want null", object["currentTargetId"])
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
