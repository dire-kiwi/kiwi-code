package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/ivan/dire-mux/internal/project"
	"github.com/ivan/dire-mux/internal/server"
)

const restartShutdownTimeout = 5 * time.Second

func main() {
	addr := flag.String("addr", envOr("DIRE_MUX_ADDR", productionHTTPAddress), "HTTP listen address")
	dataDir := flag.String("data-dir", envOr("DIRE_MUX_DATA_DIR", defaultDataDir()), "directory used for application data")
	tmuxSocket := flag.String("tmux-socket", envOr("DIRE_MUX_TMUX_SOCKET", ""), "tmux socket name (default: dire-mux; use a unique name for development and tests)")
	allowedOriginPort := flag.Int("allowed-origin-port", 0, "allow API access from a same-host browser origin on this port")
	mode := flag.String("mode", envOr("DIRE_MUX_MODE", runtimeModeProduction), "runtime mode: production or development")
	addCurrentDirectory := flag.Bool("add-current-directory", false, "ensure the server working directory is a project (for isolated development and agent tests)")
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
		AllowedOriginPort:  *allowedOriginPort,
		AllowRemoteOrigins: true,
		TmuxSocketName:     *tmuxSocket,
		CleanupContext:     applicationContext,
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
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- httpServer.ListenAndServe()
	}()

	fmt.Printf("dire-mux is running at http://%s\n", *addr)
	select {
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	case <-restartRequests:
		fmt.Println("Restart requested; shutting down dire-mux completely...")
		stopApplication()
		shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), restartShutdownTimeout)
		if err := httpServer.Shutdown(shutdownContext); err != nil {
			log.Printf("graceful restart shutdown: %v", err)
			if closeErr := httpServer.Close(); closeErr != nil {
				log.Printf("close HTTP server for restart: %v", closeErr)
			}
		}
		cancelShutdown()
	}
}

func defaultDataDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "dire-mux")
	}
	return ".dire-mux"
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
