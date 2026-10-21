package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
	"github.com/dire-kiwi/kiwi-code/internal/server"
)

const restartShutdownTimeout = 5 * time.Second

func main() {
	addr := flag.String("addr", envOr("KIWI_CODE_ADDR", productionHTTPAddress), "HTTP listen address")
	dataDir := flag.String("data-dir", envOr("KIWI_CODE_DATA_DIR", defaultDataDir()), "directory used for application data")
	tmuxSocket := flag.String("tmux-socket", envOr("KIWI_CODE_TMUX_SOCKET", ""), "tmux socket name (default: kiwi-code; use a unique name for development and tests)")
	allowedOriginPort := flag.Int("allowed-origin-port", 0, "allow API access from a same-host browser origin on this port")
	mode := flag.String("mode", envOr("KIWI_CODE_MODE", runtimeModeProduction), "runtime mode: production or development")
	addCurrentDirectory := flag.Bool("add-current-directory", false, "ensure the server working directory is a project (for isolated development and agent tests)")
	browserBackend := flag.String("browser-backend", envOr("KIWI_CODE_BROWSER_BACKEND", "headless"), "browser backend: headless or electron")
	chromeBinary := flag.String("chrome-binary", envOr("KIWI_CODE_CHROME_BIN", ""), "Chrome executable for the headless browser backend")
	flag.Parse()

	if err := validateRuntimeStartup(runtimeConfiguration{
		Mode:                *mode,
		Address:             *addr,
		AllowedOriginPort:   *allowedOriginPort,
		TmuxSocket:          *tmuxSocket,
		AddCurrentDirectory: *addCurrentDirectory,
	}); err != nil {
		log.Fatalf("refuse unsafe startup: %v", err)
	}

	applicationContext, stopApplication := context.WithCancel(context.Background())
	defer stopApplication()
	restartRequests := make(chan struct{}, 1)

	store, err := project.NewStore(filepath.Join(*dataDir, "projects.json"))
	if err != nil {
		log.Fatalf("open project store: %v", err)
	}
	if *addCurrentDirectory {
		workingDirectory, err := os.Getwd()
		if err != nil {
			log.Fatalf("find current directory project: %v", err)
		}
		item, added, err := ensureProjectPath(store, workingDirectory)
		if err != nil {
			log.Fatalf("add current directory project: %v", err)
		}
		action := "Using"
		if added {
			action = "Added"
		}
		fmt.Printf("%s current directory project %q (%s)\n", action, item.Name, item.Path)
	}

	handler, err := server.NewWithOptions(store, server.Options{
		AllowedOriginPort:       *allowedOriginPort,
		TmuxSocketName:          *tmuxSocket,
		CleanupContext:          applicationContext,
		BrowserBackend:          *browserBackend,
		BrowserChromeBinary:     *chromeBinary,
		BrowserProtectedOrigins: browserProtectedOrigins(*addr, *allowedOriginPort),
		Restart: func() {
			select {
			case restartRequests <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		log.Fatalf("create server: %v", err)
	}

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext: func(net.Listener) context.Context {
			return applicationContext
		},
	}
	// Signals stay registered for the whole shutdown: a supervisor such as
	// cmd/dev may deliver a second interrupt, and every shutdown step below
	// is already bounded by restartShutdownTimeout.
	shutdownSignals := make(chan os.Signal, 1)
	signal.Notify(shutdownSignals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(shutdownSignals)

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- httpServer.ListenAndServe()
	}()

	fmt.Printf("kiwi-code is running at http://%s\n", *addr)
	var serveErr error
	exitCode := 0
	select {
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			serveErr = err
		}
	case <-restartRequests:
		fmt.Println("Restart requested; shutting down kiwi-code completely...")
		stopApplication()
		shutdownHTTPServer(httpServer, "restart")
	case received := <-shutdownSignals:
		fmt.Printf("Received %s; shutting down kiwi-code...\n", received)
		exitCode = signalExitCode(received)
		stopApplication()
		shutdownHTTPServer(httpServer, "signal")
	}
	stopApplication()
	if closer, ok := handler.(interface{ Close(context.Context) error }); ok {
		closeContext, cancelClose := context.WithTimeout(context.Background(), restartShutdownTimeout)
		if err := closer.Close(closeContext); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("close application: %v", err)
		}
		cancelClose()
	}
	if serveErr != nil {
		log.Fatal(serveErr)
	}
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

func shutdownHTTPServer(httpServer *http.Server, reason string) {
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), restartShutdownTimeout)
	defer cancelShutdown()
	if err := httpServer.Shutdown(shutdownContext); err != nil {
		log.Printf("graceful %s shutdown: %v", reason, err)
		if closeErr := httpServer.Close(); closeErr != nil {
			log.Printf("close HTTP server for %s: %v", reason, closeErr)
		}
	}
}

// signalExitCode reports a signal shutdown with the shell's 128+N status.
// Launchers (`make run`, cmd/dev) treat exit status 0 as a restart request,
// so a signal-driven shutdown must never exit successfully.
func signalExitCode(received os.Signal) int {
	if number, ok := received.(syscall.Signal); ok && number > 0 {
		return 128 + int(number)
	}
	return 1
}

func browserProtectedOrigins(address string, allowedOriginPort int) []string {
	_, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return nil
	}
	origins := []string{"http://127.0.0.1:" + port, "http://localhost:" + port, "http://[::1]:" + port}
	if allowedOriginPort > 0 {
		value := fmt.Sprintf("%d", allowedOriginPort)
		origins = append(origins, "http://127.0.0.1:"+value, "http://localhost:"+value, "http://[::1]:"+value)
	}
	return origins
}

func defaultDataDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "kiwi-code")
	}
	return ".kiwi-code"
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
