package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	pollInterval    = 250 * time.Millisecond
	shutdownTimeout = 3 * time.Second
)

type sourceFile struct {
	modified time.Time
	size     int64
}

type sourceSnapshot map[string]sourceFile

type runningServer struct {
	command *exec.Cmd
	done    <-chan error
	binary  string
}

func main() {
	os.Exit(run())
}

func run() int {
	root, err := filepath.Abs(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "find project root: %v\n", err)
		return 1
	}

	temporaryDir, err := os.MkdirTemp("", "dire-mux-dev-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create temporary directory: %v\n", err)
		return 1
	}
	defer os.RemoveAll(temporaryDir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	changes := watchGoSources(ctx, root)
	backendDone := make(chan struct{})
	go func() {
		runBackend(ctx, root, temporaryDir, os.Args[1:], changes)
		close(backendDone)
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	received := <-signals
	fmt.Printf("\nStopping Go development server (%s)...\n", received)
	cancel()
	<-backendDone
	return 0
}

func runBackend(ctx context.Context, root, temporaryDir string, args []string, changes <-chan struct{}) {
	server := startServer(root, temporaryDir, args)
	for {
		var serverDone <-chan error
		if server != nil {
			serverDone = server.done
		}

		select {
		case <-ctx.Done():
			stopServer(server)
			return
		case <-changes:
			fmt.Println("Go source changed; rebuilding backend...")
			replacement := buildServer(root, temporaryDir)
			if replacement == "" {
				continue
			}

			stopServer(server)
			server = launchServer(root, replacement, args)
		case err := <-serverDone:
			_ = os.Remove(server.binary)
			server = nil
			if err != nil {
				fmt.Fprintf(os.Stderr, "Go server exited: %v. Waiting for a source change...\n", err)
				continue
			}
			if ctx.Err() != nil {
				return
			}

			// The application only exits cleanly when its restart endpoint is
			// used. Wait has completed here, so the old process is fully gone
			// before the replacement is built and launched.
			fmt.Println("Go server requested restart; rebuilding backend...")
			server = startServer(root, temporaryDir, args)
		}
	}
}

func startServer(root, temporaryDir string, args []string) *runningServer {
	binary := buildServer(root, temporaryDir)
	if binary == "" {
		return nil
	}
	return launchServer(root, binary, args)
}

func buildServer(root, temporaryDir string) string {
	binary := filepath.Join(temporaryDir, fmt.Sprintf("dire-mux-%d", time.Now().UnixNano()))
	command := exec.Command("go", "build", "-o", binary, ".")
	command.Dir = root
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr

	if err := command.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Go server build failed: %v\n", err)
		return ""
	}
	return binary
}

func launchServer(root, binary string, args []string) *runningServer {
	command := exec.Command(binary, developmentServerArgs(args)...)
	command.Dir = root
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Stdin = os.Stdin
	if err := command.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "start Go server: %v\n", err)
		_ = os.Remove(binary)
		return nil
	}

	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
	}()
	return &runningServer{command: command, done: done, binary: binary}
}

func developmentServerArgs(args []string) []string {
	// cmd/dev is always a development launcher. Put the fixed mode before all
	// forwarded arguments (including a possible -- terminator), and discard any
	// caller-supplied mode so it cannot grant production access.
	result := []string{"-mode=development"}
	for index := 0; index < len(args); index++ {
		argument := args[index]
		name := strings.SplitN(argument, "=", 2)[0]
		if name != "-mode" && name != "--mode" {
			result = append(result, argument)
			continue
		}
		if !strings.Contains(argument, "=") && index+1 < len(args) {
			index++
		}
	}
	return result
}

func stopServer(server *runningServer) {
	if server == nil {
		return
	}
	stopProcess(server.command, server.done)
	_ = os.Remove(server.binary)
}

func stopProcess(command *exec.Cmd, done <-chan error) {
	if command == nil {
		return
	}
	if err := command.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		fmt.Fprintf(os.Stderr, "stop process: %v\n", err)
	}

	select {
	case <-done:
		return
	case <-time.After(shutdownTimeout):
		if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			fmt.Fprintf(os.Stderr, "kill process: %v\n", err)
		}
		<-done
	}
}

func watchGoSources(ctx context.Context, root string) <-chan struct{} {
	changes := make(chan struct{}, 1)
	go func() {
		previous, err := snapshotSources(root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "watch Go sources: %v\n", err)
			previous = nil
		}

		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				next, err := snapshotSources(root)
				if err != nil {
					fmt.Fprintf(os.Stderr, "watch Go sources: %v\n", err)
					continue
				}
				if snapshotsEqual(previous, next) {
					continue
				}
				previous = next
				select {
				case changes <- struct{}{}:
				default:
				}
			}
		}
	}()
	return changes
}

func snapshotSources(root string) (sourceSnapshot, error) {
	snapshot := make(sourceSnapshot)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && ignoredDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !isWatchedSource(relative) {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		snapshot[relative] = sourceFile{modified: info.ModTime(), size: info.Size()}
		return nil
	})
	return snapshot, err
}

func ignoredDirectory(name string) bool {
	return name == ".git" || name == "bin" || name == "node_modules" || strings.HasPrefix(name, ".")
}

func isWatchedSource(path string) bool {
	if filepath.Ext(path) == ".go" {
		return true
	}
	return path == "go.mod" || path == "go.sum"
}

func snapshotsEqual(left, right sourceSnapshot) bool {
	if len(left) != len(right) {
		return false
	}
	for path, leftFile := range left {
		rightFile, found := right[path]
		if !found || !leftFile.modified.Equal(rightFile.modified) || leftFile.size != rightFile.size {
			return false
		}
	}
	return true
}
