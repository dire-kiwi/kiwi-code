package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDevelopmentServerArgsForcesDevelopmentMode(t *testing.T) {
	input := []string{
		"-addr", "127.0.0.1:18080",
		"-add-current-directory",
		"-mode=production",
		"--mode", "production",
		"--",
	}
	want := []string{"-mode=development", "-addr", "127.0.0.1:18080", "-add-current-directory", "--"}
	if got := developmentServerArgs(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("development server args = %#v, want %#v", got, want)
	}
	if len(input) != 7 || input[3] != "-mode=production" {
		t.Fatalf("developmentServerArgs mutated input: %#v", input)
	}
}

func TestSnapshotSourcesIncludesOnlyGoInputs(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "main.go"), "package main\n")
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/test\n")
	writeTestFile(t, filepath.Join(root, "web", "main.tsx"), "export {}\n")
	writeTestFile(t, filepath.Join(root, "node_modules", "dependency.go"), "package dependency\n")
	writeTestFile(t, filepath.Join(root, ".git", "config.go"), "package git\n")

	snapshot, err := snapshotSources(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot) != 2 {
		t.Fatalf("snapshot contains %d files, want 2: %#v", len(snapshot), snapshot)
	}
	if _, found := snapshot["main.go"]; !found {
		t.Fatal("main.go was not watched")
	}
	if _, found := snapshot["go.mod"]; !found {
		t.Fatal("go.mod was not watched")
	}
}

func TestSnapshotsEqual(t *testing.T) {
	base := sourceSnapshot{"main.go": {size: 12}}
	if !snapshotsEqual(base, sourceSnapshot{"main.go": {size: 12}}) {
		t.Fatal("identical snapshots are different")
	}
	if snapshotsEqual(base, sourceSnapshot{"main.go": {size: 13}}) {
		t.Fatal("changed snapshots are equal")
	}
}

func TestRunBackendRelaunchesAfterCleanRestartRequest(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	tmuxSocket := fmt.Sprintf("dmv-%d-%x", port, time.Now().UnixNano()&0xffffff)
	if tmuxSocket == "" || tmuxSocket == "kiwi-code" || tmuxSocket == "dire-mux" {
		t.Fatal("refusing to use a production tmux socket")
	}
	defer func() {
		if tmuxPath, lookErr := exec.LookPath("tmux"); lookErr == nil {
			_ = exec.Command(tmuxPath, "-L", tmuxSocket, "kill-server").Run()
		}
	}()

	binaryDirectory := t.TempDir()
	dataDirectory := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runBackend(ctx, root, binaryDirectory, []string{
			"-addr", fmt.Sprintf("127.0.0.1:%d", port),
			"-data-dir", dataDirectory,
			"-tmux-socket", tmuxSocket,
			"-add-current-directory",
		}, nil)
		close(done)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("development backend did not stop")
		}
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	serverURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	healthURL := serverURL + "/api/health"
	first := waitForDevelopmentHealth(t, client, healthURL, "")
	projectID := requireDevelopmentCurrentDirectoryProject(t, client, serverURL, root)

	request, err := http.NewRequest(http.MethodPost, serverURL+"/api/restart", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("request restart: %v", err)
	}
	if response.StatusCode != http.StatusAccepted {
		_ = response.Body.Close()
		t.Fatalf("restart status = %d", response.StatusCode)
	}
	var restart map[string]string
	decodeErr := json.NewDecoder(response.Body).Decode(&restart)
	closeErr := response.Body.Close()
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if restart["status"] != "restarting" || restart["instanceId"] != first {
		t.Fatalf("restart response = %#v, first instance = %q", restart, first)
	}

	second := waitForDevelopmentHealth(t, client, healthURL, first)
	if restartedProjectID := requireDevelopmentCurrentDirectoryProject(t, client, serverURL, root); restartedProjectID != projectID {
		t.Fatalf("current directory project changed across restart: first=%q second=%q", projectID, restartedProjectID)
	}
	firstPID := first[strings.LastIndex(first, "-")+1:]
	secondPID := second[strings.LastIndex(second, "-")+1:]
	if firstPID == secondPID {
		t.Fatalf("backend restarted without a full process exit: first=%q second=%q", first, second)
	}
}

func requireDevelopmentCurrentDirectoryProject(t *testing.T, client *http.Client, serverURL, root string) string {
	t.Helper()
	response, err := client.Get(serverURL + "/api/projects")
	if err != nil {
		t.Fatalf("list development projects: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list development projects status = %d", response.StatusCode)
	}
	var projects []struct {
		ID      string `json:"id"`
		Path    string `json:"path"`
		Threads []struct {
			Cwd string `json:"cwd"`
		} `json:"threads"`
	}
	if err := json.NewDecoder(response.Body).Decode(&projects); err != nil {
		t.Fatalf("decode development projects: %v", err)
	}
	if len(projects) != 1 || projects[0].ID == "" || projects[0].Path != root ||
		len(projects[0].Threads) != 1 || projects[0].Threads[0].Cwd != root {
		t.Fatalf("development current directory projects = %#v, want one project rooted at %q", projects, root)
	}
	return projects[0].ID
}

func waitForDevelopmentHealth(t *testing.T, client *http.Client, url, previousInstance string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(url)
		if err == nil {
			var health map[string]string
			decodeErr := json.NewDecoder(response.Body).Decode(&health)
			_ = response.Body.Close()
			if decodeErr == nil && response.StatusCode == http.StatusOK && health["status"] == "ok" &&
				health["instanceId"] != "" && health["instanceId"] != previousInstance {
				return health["instanceId"]
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for a development backend instance after %q", previousInstance)
	return ""
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
