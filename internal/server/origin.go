package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Options configures server runtime behavior.
type Options struct {
	// AllowedOriginPort permits a browser frontend on this port to call the API
	// when its hostname matches the hostname used for the backend request. The
	// development launcher uses this for Vite without opening the API to
	// unrelated cross-origin sites.
	AllowedOriginPort int

	// AllowRemoteOrigins permits Kiwi Code frontends served by another HTTP(S)
	// origin to call this backend and open its WebSockets. The browser backend
	// picker relies on this in both production and development. Kiwi Code has no
	// authentication and this option is intended only for trusted LAN/Tailscale
	// environments.
	AllowRemoteOrigins bool

	// TmuxSocketName overrides the persistent tmux server name. Leave this
	// empty only in production. Development, tests, and validation runs must
	// provide an isolated name so they cannot access the user's sessions.
	TmuxSocketName string

	// CleanupContext controls the periodic archived-thread and unattached-
	// worktree cleanup loop. A nil context uses context.Background().
	CleanupContext context.Context

	// CleanupInterval overrides the one-hour cleanup interval. It is primarily
	// useful for integration tests.
	CleanupInterval time.Duration

	// DisableCleanup prevents the background cleanup loop from starting.
	DisableCleanup bool

	// Restart requests a graceful application-process restart. The current
	// process exits completely; its launcher is responsible for starting the
	// replacement. When nil, the restart API reports that restarting is
	// unavailable.
	Restart func()
}

type originPolicy struct {
	allowedPort       int
	allowRemoteOrigin bool
}

func newOriginPolicy(options Options) (originPolicy, error) {
	if options.AllowedOriginPort < 0 || options.AllowedOriginPort > 65535 {
		return originPolicy{}, fmt.Errorf("allowed origin port must be 0 or between 1 and 65535")
	}
	return originPolicy{
		allowedPort:       options.AllowedOriginPort,
		allowRemoteOrigin: options.AllowRemoteOrigins,
	}, nil
}

func configuredTmuxSocketName(options Options) (string, error) {
	name := options.TmuxSocketName
	if name == "" {
		return tmuxSocketName, nil
	}
	if len(name) > 64 {
		return "", fmt.Errorf("tmux socket name must be 64 characters or fewer")
	}
	for _, character := range name {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return "", fmt.Errorf("tmux socket name may contain only letters, numbers, dots, hyphens, and underscores")
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("tmux socket name is invalid")
	}
	return name, nil
}

func (p originPolicy) allows(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}

	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return false
	}
	if parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return false
	}

	// Same-origin requests remain valid when cross-origin access is disabled.
	if strings.EqualFold(parsed.Host, r.Host) {
		return true
	}
	if p.allowRemoteOrigin {
		return true
	}
	if p.allowedPort == 0 || effectiveOriginPort(parsed) != p.allowedPort {
		return false
	}

	requestURL := url.URL{Host: r.Host}
	return requestURL.Hostname() != "" && strings.EqualFold(parsed.Hostname(), requestURL.Hostname())
}

func effectiveOriginPort(origin *url.URL) int {
	if port := origin.Port(); port != "" {
		parsed, err := strconv.Atoi(port)
		if err == nil {
			return parsed
		}
		return 0
	}
	if origin.Scheme == "http" {
		return 80
	}
	if origin.Scheme == "https" {
		return 443
	}
	return 0
}

func withOriginPolicy(next http.Handler, policy originPolicy) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if (policy.allowedPort == 0 && !policy.allowRemoteOrigin) || !strings.HasPrefix(r.URL.Path, "/api/") || origin == "" || !policy.allows(r) {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Add("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Origin", origin)
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.Header().Add("Vary", "Access-Control-Request-Method")
			w.Header().Add("Vary", "Access-Control-Request-Headers")
			w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, "+agentTokenHeader)
			if strings.EqualFold(r.Header.Get("Access-Control-Request-Private-Network"), "true") {
				w.Header().Add("Vary", "Access-Control-Request-Private-Network")
				w.Header().Set("Access-Control-Allow-Private-Network", "true")
			}
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
