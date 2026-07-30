package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

const (
	apiRouteGoldenFile      = "testdata/api_routes.txt"
	expectedAPIRouteCount   = 64
	expectedTotalRouteCount = 65
)

type websocketRouteContract struct {
	pattern  string
	protocol string
}

// websocketRouteContracts documents the routes that require a real upgraded
// connection and therefore must not be invoked with an httptest.ResponseRecorder.
var websocketRouteContracts = []websocketRouteContract{
	{
		pattern:  "GET /api/projects/{id}/threads/{threadId}/browser/stream",
		protocol: "browser frame and input stream",
	},
	{
		pattern:  "GET /api/projects/{id}/threads/{threadId}/claude/native",
		protocol: "native Claude agent",
	},
	{
		pattern:  "GET /api/projects/{id}/threads/{threadId}/pi/native",
		protocol: "native Pi agent",
	},
	{
		pattern:  "GET /api/projects/{id}/threads/{threadId}/terminal",
		protocol: "interactive thread terminal",
	},
	{
		pattern:  "GET /api/state",
		protocol: "unified UI state",
	},
	{
		pattern:  "GET /api/tmux/terminal",
		protocol: "tmux browser terminal",
	},
}

var routePlaceholderName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func TestAPIRouteContractMatchesGolden(t *testing.T) {
	actual := productionMuxPatterns(t)
	assertUniqueRoutes(t, "server.go", actual)
	for _, pattern := range actual {
		validateProductionRoutePattern(t, pattern)
	}

	if len(actual) != expectedTotalRouteCount {
		t.Fatalf("production route count = %d, want %d", len(actual), expectedTotalRouteCount)
	}
	apiCount := 0
	for _, pattern := range actual {
		if _, path, ok := splitMethodPattern(pattern); ok && strings.HasPrefix(path, "/api/") {
			apiCount++
		}
	}
	if apiCount != expectedAPIRouteCount {
		t.Fatalf("production API route count = %d, want %d", apiCount, expectedAPIRouteCount)
	}

	sortedActual := slices.Clone(actual)
	sort.Strings(sortedActual)
	golden := readRouteGolden(t)
	if !slices.Equal(sortedActual, golden) {
		t.Fatalf(
			"production routes differ from %s\nmissing from golden:\n%s\nmissing from server.go:\n%s",
			apiRouteGoldenFile,
			indentRoutes(routeDifference(sortedActual, golden)),
			indentRoutes(routeDifference(golden, sortedActual)),
		)
	}

	assertDocumentedWebSocketRoutes(t, sortedActual)
}

func TestAPIRoutePatternsResolve(t *testing.T) {
	patterns := productionMuxPatterns(t)
	probe := http.NewServeMux()
	for _, pattern := range patterns {
		probe.HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})
	}

	for _, pattern := range patterns {
		method, path := requestForPattern(t, pattern)
		request := httptest.NewRequest(method, materializeRoutePath(t, path), nil)
		_, matchedPattern := probe.Handler(request)
		if matchedPattern != pattern {
			t.Errorf("%q resolved as %q", pattern, matchedPattern)
		}
	}
}

func TestReadOnlyAPIRoutesResolveThroughProductionMux(t *testing.T) {
	// Keep filesystem lookups made by settings and path-suggestion handlers
	// inside the test's temporary directory.
	t.Setenv("HOME", t.TempDir())

	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}

	webSockets := documentedWebSocketRouteSet()
	externalProcessReads := map[string]struct{}{
		// Model discovery launches the Pi executable found on PATH. This
		// registration test must not depend on a developer's local Pi install;
		// focused coding-agent tests cover the RPC contract with a fake binary.
		"GET /api/coding-agents": {},
	}
	var skippedMutations []string
	var skippedExternalReads []string
	for _, pattern := range productionMuxPatterns(t) {
		if _, isWebSocket := webSockets[pattern]; isWebSocket {
			continue
		}
		if _, launchesExternalProcess := externalProcessReads[pattern]; launchesExternalProcess {
			skippedExternalReads = append(skippedExternalReads, pattern)
			continue
		}

		method, path := requestForPattern(t, pattern)
		if method != http.MethodGet {
			// Invoking a mutating route solely to inspect dispatch can install
			// files, launch processes, or alter persisted state. Its exact
			// production registration is covered by the AST/golden comparison,
			// and its ServeMux resolution is covered by TestAPIRoutePatternsResolve.
			skippedMutations = append(skippedMutations, pattern)
			continue
		}

		t.Run(pattern, func(t *testing.T) {
			request := httptest.NewRequest(method, materializeRoutePath(t, path), nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if request.Pattern != pattern {
				t.Fatalf(
					"production mux matched %q, want %q (status %d, body %q)",
					request.Pattern,
					pattern,
					response.Code,
					response.Body.String(),
				)
			}
			if strings.HasPrefix(path, "/api/") && request.Pattern == "/" {
				t.Fatalf("%q fell through to the frontend handler", pattern)
			}
		})
	}

	t.Logf(
		"did not invoke %d mutating routes or %d external-process reads; registration and matching are covered without executing their handlers",
		len(skippedMutations),
		len(skippedExternalReads),
	)
}

func productionMuxPatterns(t *testing.T) []string {
	t.Helper()

	fileset := token.NewFileSet()
	file, err := parser.ParseFile(fileset, "server.go", nil, 0)
	if err != nil {
		t.Fatalf("parse server.go: %v", err)
	}

	var constructor *ast.FuncDecl
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Recv == nil && function.Name.Name == "NewWithOptions" {
			constructor = function
			break
		}
	}
	if constructor == nil {
		t.Fatal("server.go does not declare NewWithOptions")
	}

	foundMuxConstruction := false
	var patterns []string
	ast.Inspect(constructor.Body, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.AssignStmt:
			if assignsNewServeMux(node) {
				foundMuxConstruction = true
			}
		case *ast.CallExpr:
			selector, ok := node.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "HandleFunc" {
				return true
			}
			receiver, ok := selector.X.(*ast.Ident)
			if !ok || receiver.Name != "mux" {
				return true
			}
			if len(node.Args) != 2 {
				t.Fatalf("%s: mux.HandleFunc must have two arguments", fileset.Position(node.Pos()))
			}
			literal, ok := node.Args[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				t.Fatalf("%s: mux.HandleFunc pattern must be a string literal", fileset.Position(node.Pos()))
			}
			pattern, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatalf("%s: decode mux.HandleFunc pattern: %v", fileset.Position(literal.Pos()), err)
			}
			patterns = append(patterns, pattern)
		}
		return true
	})

	if !foundMuxConstruction {
		t.Fatal("NewWithOptions does not construct mux with http.NewServeMux")
	}
	if len(patterns) == 0 {
		t.Fatal("NewWithOptions contains no literal mux.HandleFunc registrations")
	}
	return patterns
}

func assignsNewServeMux(assignment *ast.AssignStmt) bool {
	if len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
		return false
	}
	name, ok := assignment.Lhs[0].(*ast.Ident)
	if !ok || name.Name != "mux" {
		return false
	}
	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "NewServeMux" {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "http"
}

func readRouteGolden(t *testing.T) []string {
	t.Helper()

	contents, err := os.ReadFile(apiRouteGoldenFile)
	if err != nil {
		t.Fatalf("read %s: %v", apiRouteGoldenFile, err)
	}
	text := strings.TrimSuffix(string(contents), "\n")
	if text == "" {
		t.Fatalf("%s is empty", apiRouteGoldenFile)
	}
	routes := strings.Split(text, "\n")
	for index, route := range routes {
		if route == "" || strings.TrimSpace(route) != route {
			t.Fatalf("%s:%d contains an empty route or surrounding whitespace", apiRouteGoldenFile, index+1)
		}
	}
	assertUniqueRoutes(t, apiRouteGoldenFile, routes)
	if !sort.StringsAreSorted(routes) {
		t.Fatalf("%s must remain sorted", apiRouteGoldenFile)
	}
	return routes
}

func validateProductionRoutePattern(t *testing.T, pattern string) {
	t.Helper()

	method, path, qualified := splitMethodPattern(pattern)
	if pattern == "/" {
		if qualified {
			t.Fatalf("frontend fallback %q unexpectedly has a method", pattern)
		}
		return
	}
	if !qualified {
		t.Fatalf("API pattern %q is not method-qualified", pattern)
	}
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		t.Fatalf("API pattern %q uses unsupported method %q", pattern, method)
	}
	if !strings.HasPrefix(path, "/api/") {
		t.Fatalf("non-frontend pattern %q is outside /api/", pattern)
	}

	seenNames := make(map[string]struct{})
	segments := strings.Split(path, "/")
	for index, segment := range segments {
		if !strings.ContainsAny(segment, "{}") {
			continue
		}
		if len(segment) < 3 || segment[0] != '{' || segment[len(segment)-1] != '}' {
			t.Fatalf("API pattern %q has malformed placeholder segment %q", pattern, segment)
		}
		name := segment[1 : len(segment)-1]
		multi := strings.HasSuffix(name, "...")
		if multi {
			name = strings.TrimSuffix(name, "...")
			if index != len(segments)-1 {
				t.Fatalf("API pattern %q has non-terminal multi-segment placeholder", pattern)
			}
		}
		if !routePlaceholderName.MatchString(name) {
			t.Fatalf("API pattern %q has invalid placeholder name %q", pattern, name)
		}
		if _, duplicate := seenNames[name]; duplicate {
			t.Fatalf("API pattern %q repeats placeholder name %q", pattern, name)
		}
		seenNames[name] = struct{}{}
	}
}

func splitMethodPattern(pattern string) (method, path string, qualified bool) {
	method, path, qualified = strings.Cut(pattern, " ")
	if !qualified || method == "" || path == "" || strings.Contains(path, " ") {
		return "", pattern, false
	}
	return method, path, true
}

func requestForPattern(t *testing.T, pattern string) (method, path string) {
	t.Helper()
	if pattern == "/" {
		return http.MethodGet, "/__api_route_contract_frontend__"
	}
	method, path, ok := splitMethodPattern(pattern)
	if !ok {
		t.Fatalf("cannot construct request for malformed pattern %q", pattern)
	}
	return method, path
}

func materializeRoutePath(t *testing.T, path string) string {
	t.Helper()

	segments := strings.Split(path, "/")
	for index, segment := range segments {
		if !strings.HasPrefix(segment, "{") || !strings.HasSuffix(segment, "}") {
			continue
		}
		name := strings.TrimSuffix(segment[1:len(segment)-1], "...")
		if !routePlaceholderName.MatchString(name) {
			t.Fatalf("cannot materialize invalid placeholder %q in %q", segment, path)
		}
		segments[index] = "contract-" + strings.ToLower(name)
	}
	return strings.Join(segments, "/")
}

func assertUniqueRoutes(t *testing.T, source string, routes []string) {
	t.Helper()
	seen := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		if _, duplicate := seen[route]; duplicate {
			t.Fatalf("%s contains duplicate route %q", source, route)
		}
		seen[route] = struct{}{}
	}
}

func assertDocumentedWebSocketRoutes(t *testing.T, routes []string) {
	t.Helper()
	if len(websocketRouteContracts) != 6 {
		t.Fatalf("documented WebSocket route count = %d, want 6", len(websocketRouteContracts))
	}

	routeSet := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		routeSet[route] = struct{}{}
	}
	seen := make(map[string]struct{}, len(websocketRouteContracts))
	for _, contract := range websocketRouteContracts {
		if contract.protocol == "" {
			t.Fatalf("WebSocket route %q has no protocol description", contract.pattern)
		}
		if _, duplicate := seen[contract.pattern]; duplicate {
			t.Fatalf("duplicate documented WebSocket route %q", contract.pattern)
		}
		seen[contract.pattern] = struct{}{}
		if _, registered := routeSet[contract.pattern]; !registered {
			t.Fatalf("documented WebSocket route %q is not registered", contract.pattern)
		}
	}
}

func documentedWebSocketRouteSet() map[string]struct{} {
	routes := make(map[string]struct{}, len(websocketRouteContracts))
	for _, contract := range websocketRouteContracts {
		routes[contract.pattern] = struct{}{}
	}
	return routes
}

func routeDifference(left, right []string) []string {
	rightSet := make(map[string]struct{}, len(right))
	for _, route := range right {
		rightSet[route] = struct{}{}
	}
	var difference []string
	for _, route := range left {
		if _, found := rightSet[route]; !found {
			difference = append(difference, route)
		}
	}
	return difference
}

func indentRoutes(routes []string) string {
	if len(routes) == 0 {
		return "  (none)"
	}
	return "  " + strings.Join(routes, "\n  ")
}
