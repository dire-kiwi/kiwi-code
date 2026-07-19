package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ivan/dire-mux/internal/headless"
)

func main() {
	serverURL := flag.String("url", "http://127.0.0.1:4000", "Dire Mux server URL")
	clients := flag.Int("clients", 3, "number of simultaneous global and shared-terminal clients")
	projectPath := flag.String("project-path", "", "existing server-visible directory to use; defaults to a local temporary directory")
	skipTerminal := flag.Bool("skip-terminal", false, "test HTTP and status streams without tmux WebSockets")
	timeout := flag.Duration("timeout", 20*time.Second, "overall test timeout")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := headless.Run(ctx, headless.Options{
		BaseURL:      *serverURL,
		Clients:      *clients,
		ProjectPath:  *projectPath,
		Output:       os.Stdout,
		SkipTerminal: *skipTerminal,
	}); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "FAIL  %v\n", err)
		os.Exit(1)
	}
}
