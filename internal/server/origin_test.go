package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewOriginPolicyValidatesAllowedPort(t *testing.T) {
	for _, port := range []int{-1, 65536} {
		if _, err := newOriginPolicy(Options{AllowedOriginPort: port}); err == nil {
			t.Fatalf("newOriginPolicy(%d) succeeded, want an error", port)
		}
	}

	for _, port := range []int{0, 1, 65535} {
		policy, err := newOriginPolicy(Options{AllowedOriginPort: port})
		if err != nil {
			t.Fatalf("newOriginPolicy(%d): %v", port, err)
		}
		if policy.allowedPort != port {
			t.Fatalf("newOriginPolicy(%d) allowed port = %d", port, policy.allowedPort)
		}
	}
}

func TestConfiguredTmuxSocketName(t *testing.T) {
	name, err := configuredTmuxSocketName(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if name != tmuxSocketName {
		t.Fatalf("default tmux socket = %q, want %q", name, tmuxSocketName)
	}

	name, err = configuredTmuxSocketName(Options{TmuxSocketName: "dmv-validation-a1"})
	if err != nil {
		t.Fatal(err)
	}
	if name != "dmv-validation-a1" {
		t.Fatalf("configured tmux socket = %q", name)
	}

	for _, invalid := range []string{".", "..", "has a space", "../dire-mux", strings.Repeat("a", 65)} {
		if _, err := configuredTmuxSocketName(Options{TmuxSocketName: invalid}); err == nil {
			t.Fatalf("configuredTmuxSocketName(%q) succeeded, want an error", invalid)
		}
	}
}

func TestOriginPolicyAllowsOnlyConfiguredSameHostOrigin(t *testing.T) {
	policy, err := newOriginPolicy(Options{AllowedOriginPort: 5173})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{name: "missing origin", host: "127.0.0.1:8080", want: true},
		{name: "same origin", host: "127.0.0.1:8080", origin: "http://127.0.0.1:8080", want: true},
		{name: "configured port", host: "127.0.0.1:8080", origin: "http://127.0.0.1:5173", want: true},
		{name: "hostname case", host: "LOCALHOST:8080", origin: "http://localhost:5173", want: true},
		{name: "ipv6 host", host: "[::1]:8080", origin: "http://[::1]:5173", want: true},
		{name: "wrong port", host: "127.0.0.1:8080", origin: "http://127.0.0.1:5174", want: false},
		{name: "wrong hostname", host: "127.0.0.1:8080", origin: "http://localhost:5173", want: false},
		{name: "origin path", host: "127.0.0.1:8080", origin: "http://127.0.0.1:5173/", want: false},
		{name: "origin query", host: "127.0.0.1:8080", origin: "http://127.0.0.1:5173?", want: false},
		{name: "origin user", host: "127.0.0.1:8080", origin: "http://user@127.0.0.1:5173", want: false},
		{name: "unsupported scheme", host: "127.0.0.1:8080", origin: "file://127.0.0.1:5173", want: false},
		{name: "opaque origin", host: "127.0.0.1:8080", origin: "null", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://"+test.host+"/api/health", nil)
			request.Header.Set("Origin", test.origin)
			if got := policy.allows(request); got != test.want {
				t.Fatalf("allows host %q origin %q = %t, want %t", test.host, test.origin, got, test.want)
			}
		})
	}
}

func TestOriginPolicyAllowsRemoteFrontendOriginsWhenEnabled(t *testing.T) {
	policy, err := newOriginPolicy(Options{AllowRemoteOrigins: true})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		origin string
		want   bool
	}{
		{origin: "http://tailscale-host:4000", want: true},
		{origin: "https://mux.example.test", want: true},
		{origin: "http://127.0.0.1:5173", want: true},
		{origin: "file://tailscale-host/app", want: false},
		{origin: "http://user@tailscale-host:4000", want: false},
		{origin: "http://tailscale-host:4000/app", want: false},
		{origin: "null", want: false},
	}

	for _, test := range tests {
		request := httptest.NewRequest(http.MethodGet, "http://backend-host:4000/api/health", nil)
		request.Header.Set("Origin", test.origin)
		if got := policy.allows(request); got != test.want {
			t.Errorf("allows remote origin %q = %t, want %t", test.origin, got, test.want)
		}
	}
}

func TestOriginPolicyRecognizesDefaultOriginPorts(t *testing.T) {
	tests := []struct {
		port   int
		origin string
	}{
		{port: 80, origin: "http://example.test"},
		{port: 443, origin: "https://example.test"},
	}

	for _, test := range tests {
		policy, err := newOriginPolicy(Options{AllowedOriginPort: test.port})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodGet, "http://example.test:8080/api/health", nil)
		request.Header.Set("Origin", test.origin)
		if !policy.allows(request) {
			t.Fatalf("allowed port %d rejected origin %q", test.port, test.origin)
		}
	}
}

func TestOriginPolicyAddsDevelopmentCORSHeaders(t *testing.T) {
	policy, err := newOriginPolicy(Options{AllowedOriginPort: 5173})
	if err != nil {
		t.Fatal(err)
	}

	calls := 0
	handler := withOriginPolicy(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}), policy)

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/health", nil)
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d", response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:5173" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	if !strings.Contains(response.Header().Get("Vary"), "Origin") {
		t.Fatalf("Vary = %q, want Origin", response.Header().Get("Vary"))
	}
	if calls != 1 {
		t.Fatalf("next handler calls = %d, want 1", calls)
	}

	preflight := httptest.NewRequest(http.MethodOptions, "http://127.0.0.1:8080/api/projects", nil)
	preflight.Header.Set("Origin", "http://127.0.0.1:5173")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflight.Header.Set("Access-Control-Request-Headers", "content-type, x-dire-mux-agent-token")
	preflightResponse := httptest.NewRecorder()
	handler.ServeHTTP(preflightResponse, preflight)

	if preflightResponse.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", preflightResponse.Code)
	}
	if got := preflightResponse.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodPost) {
		t.Fatalf("Access-Control-Allow-Methods = %q", got)
	}
	if got := preflightResponse.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type, X-Dire-Mux-Agent-Token" {
		t.Fatalf("Access-Control-Allow-Headers = %q", got)
	}
	if calls != 1 {
		t.Fatalf("preflight called next handler; calls = %d", calls)
	}
}

func TestOriginPolicyAddsRemoteFrontendCORSHeaders(t *testing.T) {
	policy, err := newOriginPolicy(Options{AllowRemoteOrigins: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := withOriginPolicy(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), policy)

	request := httptest.NewRequest(http.MethodGet, "http://backend-host:4000/api/health", nil)
	request.Header.Set("Origin", "http://frontend-host:4000")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "http://frontend-host:4000" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}

	preflight := httptest.NewRequest(http.MethodOptions, "http://backend-host:4000/api/projects", nil)
	preflight.Header.Set("Origin", "http://frontend-host:4000")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflight.Header.Set("Access-Control-Request-Private-Network", "true")
	preflightResponse := httptest.NewRecorder()
	handler.ServeHTTP(preflightResponse, preflight)
	if preflightResponse.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", preflightResponse.Code)
	}
	if got := preflightResponse.Header().Get("Access-Control-Allow-Private-Network"); got != "true" {
		t.Fatalf("Access-Control-Allow-Private-Network = %q", got)
	}
}

func TestOriginPolicyDoesNotEnableCORSWithoutDevelopmentPort(t *testing.T) {
	handler := withOriginPolicy(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), originPolicy{})

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/health", nil)
	request.Header.Set("Origin", "http://127.0.0.1:8080")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("production Access-Control-Allow-Origin = %q", got)
	}
}

func TestOriginPolicyDoesNotAllowAnotherHost(t *testing.T) {
	policy, err := newOriginPolicy(Options{AllowedOriginPort: 5173})
	if err != nil {
		t.Fatal(err)
	}
	handler := withOriginPolicy(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), policy)

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/health", nil)
	request.Header.Set("Origin", "http://example.test:5173")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("disallowed Access-Control-Allow-Origin = %q", got)
	}
}
