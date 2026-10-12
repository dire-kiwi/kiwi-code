package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

const signalShutdownChildEnv = "KIWI_CODE_TEST_SIGNAL_SHUTDOWN_CHILD"

func TestMain(m *testing.M) {
	if arguments, ok := os.LookupEnv(signalShutdownChildEnv); ok {
		os.Args = append([]string{os.Args[0]}, strings.Split(arguments, "\n")...)
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestSignalExitCodeIsNeverARestartRequest(t *testing.T) {
	for _, test := range []struct {
		signal os.Signal
		want   int
	}{
		{signal: os.Interrupt, want: 130},
		{signal: syscall.SIGTERM, want: 143},
	} {
		if got := signalExitCode(test.signal); got != test.want {
			t.Errorf("signalExitCode(%s) = %d, want %d", test.signal, got, test.want)
		}
	}
}

func TestSignalShutsDownGracefullyWithSignalExitStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the application")
	}
	for _, test := range []struct {
		signal syscall.Signal
		want   int
	}{
		{signal: syscall.SIGINT, want: 130},
		{signal: syscall.SIGTERM, want: 143},
	} {
		t.Run(test.signal.String(), func(t *testing.T) {
			socket := isolatedTestTmuxSocket(t)
			home := t.TempDir()
			command := exec.Command(os.Args[0], "-test.run=^$")
			command.Env = append(os.Environ(),
				signalShutdownChildEnv+"="+strings.Join([]string{
					"-mode", runtimeModeDevelopment,
					"-addr", "127.0.0.1:0",
					"-data-dir", t.TempDir(),
					"-tmux-socket", socket,
					"-add-current-directory",
				}, "\n"),
				"HOME="+home,
				"XDG_CONFIG_HOME="+home+"/.config",
				"CODEX_HOME="+home+"/.codex",
				"KIWI_CODE_TMUX_SOCKET=",
				"TMUX=",
			)
			stdout, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			exited := make(chan error, 1)
			go func() {
				// Drain stdout before Wait so the shutdown message is not lost.
				scanner := bufio.NewScanner(stdout)
				running := false
				for scanner.Scan() {
					if !running && strings.HasPrefix(scanner.Text(), "kiwi-code is running at ") {
						running = true
						if err := command.Process.Signal(test.signal); err != nil {
							t.Errorf("signal application: %v", err)
						}
					}
				}
				exited <- command.Wait()
			}()

			select {
			case err := <-exited:
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) {
					t.Fatalf("application exit = %v, want status %d", err, test.want)
				}
				if got := exitErr.ExitCode(); got != test.want {
					t.Fatalf("application exit status = %d, want %d", got, test.want)
				}
			case <-time.After(30 * time.Second):
				_ = command.Process.Kill()
				<-exited
				t.Fatal("application did not shut down after signal")
			}
		})
	}
}

func isolatedTestTmuxSocket(t *testing.T) string {
	t.Helper()
	random := make([]byte, 4)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	socket := "kcv-signal-" + hex.EncodeToString(random)
	if socket == "" || socket == productionTmuxSocket {
		t.Fatalf("unsafe test tmux socket %q", socket)
	}
	t.Cleanup(func() {
		if tmux, err := exec.LookPath("tmux"); err == nil {
			_ = exec.Command(tmux, "-L", socket, "kill-server").Run()
		}
	})
	return socket
}
