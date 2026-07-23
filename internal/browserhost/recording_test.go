package browserhost

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/dire-kiwi/kiwi-code/internal/browsercontrol"
)

func TestProviderOpensOwnedRecordingRange(t *testing.T) {
	directory := t.TempDir()
	id := "rec-0123456789abcdef0123456789abcdef"
	contents := []byte("webm-recording")
	metadata := persistedRecording{
		Version: 1, ID: id, ProjectID: "project", ThreadID: "thread", TargetID: "page",
		Title: "Demonstrate checkout flow", StartedAt: "2026-07-23T10:00:00Z", FinishedAt: "2026-07-23T10:00:05Z",
		DurationMS: 5000, Bytes: int64(len(contents)), MIMEType: "video/webm;codecs=vp9", Filename: id + ".webm",
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, metadata.Filename), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, id+".json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	provider := New(Options{RecordingsDirectory: directory})
	recording, err := provider.OpenRecordingRange(context.Background(), "project", "thread", id, "bytes=5-8")
	if err != nil {
		t.Fatal(err)
	}
	defer recording.Body.Close()
	got, err := io.ReadAll(recording.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(contents[5:9]) || recording.Title != metadata.Title || !recording.Partial {
		t.Fatalf("recording = %#v, body = %q", recording, got)
	}
	if _, err := provider.OpenRecordingRange(context.Background(), "other", "thread", id, ""); err != browsercontrol.ErrRecordingNotFound {
		t.Fatalf("cross-project open error = %v", err)
	}
}
