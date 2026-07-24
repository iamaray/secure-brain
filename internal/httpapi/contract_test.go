package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"secure-brain/internal/application"
	"secure-brain/internal/domain"
	"secure-brain/internal/store"
)

const (
	contractFrontendOrigin = "http://localhost:3000"
	contractSessionSecret  = "0123456789abcdef0123456789abcdef"
)

var requestIDPattern = regexp.MustCompile(`^req_[0-9a-f]{24}$`)

type routeContract struct {
	method  string
	pattern string
	auth    bool
}

// publicRouteManifest is intentionally explicit. It is both the reviewable
// inventory of the v0 API and the input to the runtime reachability test below.
var publicRouteManifest = []routeContract{
	{http.MethodGet, "/healthz", false},
	{http.MethodGet, "/readyz", false},
	{http.MethodGet, "/api/users", false},
	{http.MethodPost, "/api/session", false},
	{http.MethodGet, "/api/session", true},
	{http.MethodDelete, "/api/session", true},
	{http.MethodGet, "/api/brains", true},
	{http.MethodPost, "/api/brains", true},
	{http.MethodGet, "/api/brains/{brainId}", true},
	{http.MethodDelete, "/api/brains/{brainId}", true},
	{http.MethodGet, "/api/brains/{brainId}/app-connections", true},
	{http.MethodPost, "/api/brains/{brainId}/app-connections", true},
	{http.MethodDelete, "/api/brains/{brainId}/app-connections", true},
	{http.MethodDelete, "/api/brains/{brainId}/app-connections/{serviceId}", true},
	{http.MethodGet, "/api/services", true},
	{http.MethodPost, "/api/services", true},
	{http.MethodGet, "/api/services/{serviceId}", true},
	{http.MethodDelete, "/api/services/{serviceId}", true},
	{http.MethodGet, "/api/brains/{brainId}/query-paths", true},
	{http.MethodPost, "/api/brains/{brainId}/query-paths", true},
	{http.MethodGet, "/api/brains/{brainId}/query-paths/{queryPathId}", true},
	{http.MethodPatch, "/api/brains/{brainId}/query-paths/{queryPathId}", true},
	{http.MethodDelete, "/api/brains/{brainId}/query-paths/{queryPathId}", true},
	{http.MethodPost, "/api/brains/{brainId}/query-paths/{queryPathId}/validate", true},
	{http.MethodGet, "/api/brains/{brainId}/assets", true},
	{http.MethodPost, "/api/brains/{brainId}/assets", true},
	{http.MethodGet, "/api/brains/{brainId}/assets/{assetId}", true},
	{http.MethodGet, "/api/brains/{brainId}/assets/{assetId}/content", true},
	{http.MethodDelete, "/api/brains/{brainId}/assets/{assetId}", true},
	{http.MethodPost, "/q/{sourceBrainId}/{queryPath...}", true},
	{http.MethodPost, "/api/brains/{brainId}/query-paths/{queryPathId}/send", true},
	{http.MethodGet, "/api/executions/{executionId}", true},
	{http.MethodGet, "/api/executions/{executionId}/trace", true},
	{http.MethodGet, "/api/brains/{brainId}/transfers", true},
	{http.MethodGet, "/api/transfers/{transferId}", true},
	{http.MethodPost, "/api/transfers/{transferId}/accept", true},
	{http.MethodPost, "/api/transfers/{transferId}/reject", true},
	{http.MethodGet, "/api/network", true},
	{http.MethodGet, "/api/network/search", true},
	{http.MethodPost, "/api/network/nodes", true},
	{http.MethodDelete, "/api/network/nodes/{nodeId}", true},
	{http.MethodGet, "/api/audit-events", true},
	{http.MethodGet, "/api/brains/{brainId}/chat", true},
	{http.MethodPost, "/api/brains/{brainId}/chat", true},
	{http.MethodDelete, "/api/brains/{brainId}/chat", true},
}

func routeTarget(pattern string) string {
	return strings.NewReplacer(
		"{brainId}", "brain.contract",
		"{serviceId}", "service.contract",
		"{queryPathId}", "00000000-0000-4000-8000-000000000010",
		"{assetId}", "00000000-0000-4000-8000-000000000020",
		"{sourceBrainId}", "brain.contract",
		"{queryPath...}", "research/share",
		"{executionId}", "00000000-0000-4000-8000-000000000030",
		"{transferId}", "00000000-0000-4000-8000-000000000040",
		"{nodeId}", "service.contract",
	).Replace(pattern)
}

func newContractRouter(db *store.Store) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(db, nil, Options{
		SessionSecret:  []byte(contractSessionSecret),
		FrontendOrigin: contractFrontendOrigin,
		Logger:         logger,
		Limits:         application.DefaultLimits(),
		ChatModel:      "contract-model",
		ChatDisabled:   true,
	})
}

func contractMiddleware(next http.Handler) http.Handler {
	clock := application.SystemClock{}
	return withMiddleware(next, slog.New(slog.NewTextHandler(io.Discard, nil)), contractFrontendOrigin, clock, application.RandomIDGenerator{Clock: clock}, application.DefaultLimits())
}

func contractRequest(handler http.Handler, method, target string, body []byte, headers http.Header, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func signInContractUser(t *testing.T, handler http.Handler, userID string) *http.Cookie {
	t.Helper()
	body, err := json.Marshal(map[string]string{"user_id": userID})
	if err != nil {
		t.Fatal(err)
	}
	response := contractRequest(handler, http.MethodPost, "/api/session", body, http.Header{"Content-Type": {"application/json"}})
	if response.Code != http.StatusCreated {
		t.Fatalf("create test session status = %d body=%s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("create test session cookies = %#v", cookies)
	}
	return cookies[0]
}

func assertRequestID(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	requestID := response.Header().Get("X-Request-ID")
	if !requestIDPattern.MatchString(requestID) {
		t.Fatalf("X-Request-ID = %q, want req_ followed by 24 lower-case hex characters", requestID)
	}
	return requestID
}

func assertJSONBody(t *testing.T, response *httptest.ResponseRecorder, want func(string) string) {
	t.Helper()
	requestID := assertRequestID(t, response)
	if got := response.Body.String(); got != want(requestID) {
		t.Fatalf("response body:\n got: %q\nwant: %q", got, want(requestID))
	}
	if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
}

func errorJSON(code domain.Code, message string) func(string) string {
	return func(requestID string) string {
		return `{"error":{"code":"` + string(code) + `","message":"` + message + `"},"request_id":"` + requestID + "\"}\n"
	}
}

func TestPublicRouteManifestThroughRealRouter(t *testing.T) {
	if got, want := len(publicRouteManifest), 45; got != want {
		t.Fatalf("route manifest contains %d entries, want %d", got, want)
	}
	seen := make(map[string]struct{}, len(publicRouteManifest))
	handler := newContractRouter(nil)

	for _, route := range publicRouteManifest {
		name := route.method + " " + route.pattern
		if _, duplicate := seen[name]; duplicate {
			t.Fatalf("duplicate route contract %q", name)
		}
		seen[name] = struct{}{}
		t.Run(name, func(t *testing.T) {
			target := routeTarget(route.pattern)
			response := contractRequest(handler, route.method, target, []byte(`{}`), nil)
			if route.auth {
				if got := response.Code; got != http.StatusUnauthorized {
					t.Fatalf("status = %d, want %d; body=%s", got, http.StatusUnauthorized, response.Body.String())
				}
				assertJSONBody(t, response, errorJSON(domain.CodeNotAuthenticated, "A mock session is required."))
			} else {
				switch name {
				case "GET /healthz":
					if response.Code != http.StatusOK {
						t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
					}
					assertJSONBody(t, response, func(requestID string) string {
						return `{"data":{"status":"ok"},"request_id":"` + requestID + "\"}\n"
					})
				case "GET /readyz":
					if response.Code != http.StatusServiceUnavailable {
						t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
					}
					assertJSONBody(t, response, func(requestID string) string {
						return `{"data":{"status":"unavailable"},"request_id":"` + requestID + "\"}\n"
					})
				default:
					// A nil store deliberately panics only after these public
					// handlers are selected. The middleware's exact recovery
					// contract is characterized here.
					if response.Code != http.StatusBadRequest {
						t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
					}
					assertJSONBody(t, response, errorJSON(domain.CodeInvalidRequest, "An internal error occurred."))
				}
			}
			if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("X-Content-Type-Options = %q", got)
			}
			wantCache := strings.HasPrefix(target, "/api/") || strings.HasPrefix(target, "/q/")
			if got := response.Header().Get("Cache-Control"); (got == "no-store") != wantCache {
				t.Fatalf("Cache-Control = %q, want no-store=%t", got, wantCache)
			}
		})
	}
}

func TestRouteManifestMatchesRegisteredPatterns(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve contract test path")
	}
	packageDir := filepath.Dir(filename)
	files, err := os.ReadDir(packageDir)
	if err != nil {
		t.Fatal(err)
	}
	var registered []string
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".go") || strings.HasSuffix(file.Name(), "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(packageDir, file.Name()), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file.Name(), err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (selector.Sel.Name != "Handle" && selector.Sel.Name != "HandleFunc") {
				return true
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			pattern, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatalf("unquote route pattern %s: %v", literal.Value, err)
			}
			method, _, hasPath := strings.Cut(pattern, " ")
			if hasPath && slices.Contains([]string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete}, method) {
				registered = append(registered, pattern)
			}
			return true
		})
	}

	expected := make([]string, 0, len(publicRouteManifest))
	for _, route := range publicRouteManifest {
		expected = append(expected, route.method+" "+route.pattern)
	}
	slices.Sort(expected)
	slices.Sort(registered)
	if !slices.Equal(registered, expected) {
		t.Fatalf("registered route patterns do not match manifest:\n registered: %q\n   manifest: %q", registered, expected)
	}
	for i := 1; i < len(registered); i++ {
		if registered[i] == registered[i-1] {
			t.Fatalf("duplicate registered route pattern %q", registered[i])
		}
	}
}

func TestRouteManifestRejectsWrongMethodsAndNearbyPaths(t *testing.T) {
	handler := newContractRouter(nil)
	uniquePaths := make(map[string]struct{}, len(publicRouteManifest))
	for _, route := range publicRouteManifest {
		uniquePaths[routeTarget(route.pattern)] = struct{}{}
	}
	for path := range uniquePaths {
		t.Run("PUT "+path, func(t *testing.T) {
			response := contractRequest(handler, http.MethodPut, path, nil, nil)
			if response.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusMethodNotAllowed, response.Body.String())
			}
			assertRequestID(t, response)
			if got := response.Body.String(); got != "Method Not Allowed\n" {
				t.Fatalf("body = %q", got)
			}
		})
	}

	for _, path := range []string{
		"/",
		"/healthz/",
		"/api",
		"/api/not-a-route",
		"/api/network/search/extra",
	} {
		t.Run("GET "+path, func(t *testing.T) {
			response := contractRequest(handler, http.MethodGet, path, nil, nil)
			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusNotFound, response.Body.String())
			}
			assertRequestID(t, response)
			if got := response.Body.String(); got != "404 page not found\n" {
				t.Fatalf("body = %q", got)
			}
		})
	}

	// Go's trailing-wildcard pattern redirects the slashless form for the
	// registered method. Pin that route-sensitive edge instead of treating it
	// as a not-found response.
	t.Run("POST /q/brain.contract", func(t *testing.T) {
		response := contractRequest(handler, http.MethodPost, "/q/brain.contract", nil, nil)
		if response.Code != http.StatusMovedPermanently {
			t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusMovedPermanently, response.Body.String())
		}
		assertRequestID(t, response)
		if got, want := response.Header().Get("Location"), "/q/brain.contract/"; got != want {
			t.Fatalf("Location = %q, want %q", got, want)
		}
		if got := response.Body.String(); got != "" {
			t.Fatalf("body = %q, want empty", got)
		}
	})
}

func TestMiddlewareCORSAndPreflightContract(t *testing.T) {
	handler := newContractRouter(nil)
	t.Run("allowed origin", func(t *testing.T) {
		response := contractRequest(handler, http.MethodGet, "/healthz", nil, http.Header{"Origin": {contractFrontendOrigin}})
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
		}
		assertRequestID(t, response)
		for name, want := range map[string]string{
			"Access-Control-Allow-Origin":      contractFrontendOrigin,
			"Access-Control-Allow-Credentials": "true",
			"Access-Control-Allow-Headers":     "Content-Type, Idempotency-Key, If-Match",
			"Access-Control-Allow-Methods":     "GET, POST, PATCH, DELETE, OPTIONS",
			"Vary":                             "Origin",
		} {
			if got := response.Header().Get(name); got != want {
				t.Errorf("%s = %q, want %q", name, got, want)
			}
		}
	})
	t.Run("allowed preflight bypasses routing and authentication", func(t *testing.T) {
		response := contractRequest(handler, http.MethodOptions, "/api/not-a-route", nil, http.Header{"Origin": {contractFrontendOrigin}})
		if response.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
		}
		assertRequestID(t, response)
		if response.Body.Len() != 0 || response.Header().Get("Content-Type") != "" {
			t.Fatalf("preflight body=%q Content-Type=%q", response.Body.String(), response.Header().Get("Content-Type"))
		}
		if got := response.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("Cache-Control = %q", got)
		}
		if got := response.Header().Get("Access-Control-Allow-Origin"); got != contractFrontendOrigin {
			t.Fatalf("Access-Control-Allow-Origin = %q", got)
		}
	})
	t.Run("originless OPTIONS also bypasses routing", func(t *testing.T) {
		response := contractRequest(handler, http.MethodOptions, "/not-a-route", nil, nil)
		if response.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
		}
		assertRequestID(t, response)
		if response.Body.Len() != 0 || response.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("body=%q allow-origin=%q", response.Body.String(), response.Header().Get("Access-Control-Allow-Origin"))
		}
	})
	for _, method := range []string{http.MethodGet, http.MethodOptions} {
		t.Run("rejected origin "+method, func(t *testing.T) {
			response := contractRequest(handler, method, "/api/brains", nil, http.Header{"Origin": {"https://not-allowed.example"}})
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
			}
			assertJSONBody(t, response, errorJSON(domain.CodeNotAuthorized, "The request origin is not allowed."))
			if got := response.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Fatalf("Access-Control-Allow-Origin = %q", got)
			}
			if got := response.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q", got)
			}
		})
	}
}

func TestAuthenticationContractThroughRealRouter(t *testing.T) {
	handler := newContractRouter(nil)
	t.Run("missing cookie", func(t *testing.T) {
		response := contractRequest(handler, http.MethodGet, "/api/session", nil, nil)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
		}
		assertJSONBody(t, response, errorJSON(domain.CodeNotAuthenticated, "A mock session is required."))
	})
	t.Run("malformed cookie", func(t *testing.T) {
		response := contractRequest(handler, http.MethodGet, "/api/session", nil, nil, &http.Cookie{Name: sessionCookieName, Value: "not-a-session"})
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
		}
		assertJSONBody(t, response, errorJSON(domain.CodeNotAuthenticated, "The mock session is invalid."))
	})
	t.Run("valid signature reaches session lookup", func(t *testing.T) {
		value, _, err := newSessionToken([]byte(contractSessionSecret))
		if err != nil {
			t.Fatal(err)
		}
		response := contractRequest(handler, http.MethodGet, "/api/session", nil, nil, &http.Cookie{Name: sessionCookieName, Value: value})
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
		}
		assertJSONBody(t, response, errorJSON(domain.CodeInvalidRequest, "An internal error occurred."))
	})
}

func TestJSONRequestContractThroughRealRouter(t *testing.T) {
	handler := newContractRouter(nil)
	tooLarge := append([]byte(`{"user_id":"`), bytes.Repeat([]byte("a"), int(application.DefaultLimits().MaxJSONBodyBytes))...)
	tooLarge = append(tooLarge, []byte(`"}`)...)
	tests := []struct {
		name    string
		body    []byte
		message string
	}{
		{"empty", nil, "The JSON request body is invalid."},
		{"malformed", []byte(`{"user_id":`), "The JSON request body is invalid."},
		{"unknown field", []byte(`{"user_id":"user","extra":true}`), "The JSON request body is invalid."},
		{"trailing JSON", []byte(`{"user_id":"user"} {}`), "The JSON request body must contain one value."},
		{"over one MiB", tooLarge, "The JSON request body is invalid."},
		// encoding/json currently replaces malformed UTF-8 and accepts the
		// document. The nil-store panic proves decoding proceeded to lookup.
		{"malformed UTF-8 accepted", []byte("{\"user_id\":\"\xff\"}"), "An internal error occurred."},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := contractRequest(handler, http.MethodPost, "/api/session", test.body, http.Header{"Content-Type": {"application/json"}})
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			assertJSONBody(t, response, errorJSON(domain.CodeInvalidRequest, test.message))
			if got := response.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q", got)
			}
		})
	}
}

func TestEnvelopeJSONOmittedNullEmptyAndOrderContract(t *testing.T) {
	type shape struct {
		Omitted string   `json:"omitted,omitempty"`
		Null    *string  `json:"null"`
		Empty   []string `json:"empty"`
		Ordered []string `json:"ordered"`
	}
	handler := contractMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeData(w, r, http.StatusOK, shape{Empty: []string{}, Ordered: []string{"first", "second"}})
	}))
	response := contractRequest(handler, http.MethodGet, "/contract", nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	assertJSONBody(t, response, func(requestID string) string {
		return `{"data":{"null":null,"empty":[],"ordered":["first","second"]},"request_id":"` + requestID + "\"}\n"
	})
}

func TestStatusAndUnknownDatabaseErrorContract(t *testing.T) {
	tests := []struct {
		code domain.Code
		want int
	}{
		{domain.CodeNotAuthenticated, http.StatusUnauthorized},
		{domain.CodeNotAuthorized, http.StatusForbidden},
		{domain.CodeInitiatorNotOwned, http.StatusForbidden},
		{domain.CodePrincipalNotAuthorized, http.StatusForbidden},
		{domain.CodeNodeNotFound, http.StatusNotFound},
		{domain.CodePathNotFound, http.StatusNotFound},
		{domain.CodeNameAlreadyExists, http.StatusConflict},
		{domain.CodeResourceInUse, http.StatusConflict},
		{domain.CodeConfigVersionConflict, http.StatusConflict},
		{domain.CodeIdempotencyKeyReused, http.StatusConflict},
		{domain.CodeTransferAlreadyResolved, http.StatusConflict},
		{domain.CodePayloadTooLarge, http.StatusRequestEntityTooLarge},
		{domain.CodeRouteInvalid, http.StatusUnprocessableEntity},
		{domain.CodeRouteTooLong, http.StatusUnprocessableEntity},
		{domain.CodePathDisabled, http.StatusUnprocessableEntity},
		{domain.CodeOperationNotAllowed, http.StatusUnprocessableEntity},
		{domain.CodeDestinationMismatch, http.StatusUnprocessableEntity},
		{domain.CodeAssetUnavailable, http.StatusUnprocessableEntity},
		{domain.CodeAssetParseFailed, http.StatusUnprocessableEntity},
		{domain.CodeQueryInvalid, http.StatusUnprocessableEntity},
		{domain.CodeIdempotencyKeyRequired, http.StatusUnprocessableEntity},
		{domain.CodeStorageProviderError, http.StatusBadGateway},
		{domain.CodeChatProviderError, http.StatusBadGateway},
		{domain.CodeTransferExpired, http.StatusGone},
		{domain.CodeInvalidRequest, http.StatusBadRequest},
		{domain.CodeServiceHopFailed, http.StatusBadRequest},
		{domain.Code("UNKNOWN"), http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(string(test.code), func(t *testing.T) {
			if got := statusForCode(test.code); got != test.want {
				t.Fatalf("statusForCode(%q) = %d, want %d", test.code, got, test.want)
			}
		})
	}

	mapped := databaseError(errors.New("database detail that must not escape"))
	handler := contractMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, mapped)
	}))
	response := contractRequest(handler, http.MethodGet, "/contract", nil, nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	assertJSONBody(t, response, errorJSON(domain.CodeInvalidRequest, "The request could not be persisted."))
}

func TestPanicResponseRecorderContract(t *testing.T) {
	tests := []struct {
		name      string
		handler   http.Handler
		status    int
		body      func(string) string
		mediaType string
	}{
		{
			name: "before response",
			handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				panic("contract")
			}),
			status:    http.StatusBadRequest,
			body:      errorJSON(domain.CodeInvalidRequest, "An internal error occurred."),
			mediaType: "application/json; charset=utf-8",
		},
		{
			name: "after response status",
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				panic("contract")
			}),
			status: http.StatusAccepted,
			body: func(string) string {
				return ""
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := contractMiddleware(test.handler)
			response := contractRequest(handler, http.MethodGet, "/contract", nil, nil)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			requestID := assertRequestID(t, response)
			if got := response.Body.String(); got != test.body(requestID) {
				t.Fatalf("body = %q, want %q", got, test.body(requestID))
			}
			if got := response.Header().Get("Content-Type"); got != test.mediaType {
				t.Fatalf("Content-Type = %q, want %q", got, test.mediaType)
			}
		})
	}
}

func TestSessionAndStableUsersHTTPContractIntegration(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_TEST_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_TEST_URL is not set; skipping database-backed HTTP contract test")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer pool.Close()
	db := store.New(pool)
	if err := db.CheckSchemaVersion(context.Background()); err != nil {
		t.Fatalf("check schema: %v", err)
	}
	users, err := db.ListUsers(context.Background())
	if err != nil || len(users) == 0 {
		t.Fatalf("list seeded users: %v", err)
	}
	if !slices.IsSortedFunc(users, func(a, b domain.User) int {
		if a.Handle == b.Handle {
			return strings.Compare(a.ID, b.ID)
		}
		return strings.Compare(a.Handle, b.Handle)
	}) {
		t.Fatalf("users are not ordered by handle then ID: %#v", users)
	}
	handler := newContractRouter(db)

	listed := contractRequest(handler, http.MethodGet, "/api/users", nil, nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("GET /api/users status = %d body=%s", listed.Code, listed.Body.String())
	}
	assertMarshaledEnvelope(t, listed, users)

	sessionBody, err := json.Marshal(map[string]string{"user_id": users[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	created := contractRequest(handler, http.MethodPost, "https://securebrain.test/api/session", sessionBody, http.Header{"Content-Type": {"application/json"}})
	if created.Code != http.StatusCreated {
		t.Fatalf("POST /api/session status = %d body=%s", created.Code, created.Body.String())
	}
	assertMarshaledEnvelope(t, created, map[string]any{
		"user":       users[0],
		"mock_auth":  true,
		"disclosure": "Local demo authentication only.",
	})
	cookies := created.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("created session cookies = %#v", cookies)
	}
	sessionCookie := cookies[0]
	if sessionCookie.Name != sessionCookieName || sessionCookie.Value == "" || sessionCookie.Path != "/" ||
		sessionCookie.MaxAge != 43200 || !sessionCookie.HttpOnly || !sessionCookie.Secure ||
		sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("created session cookie = %#v", sessionCookie)
	}
	wantSetCookie := sessionCookieName + "=" + sessionCookie.Value + "; Path=/; Max-Age=43200; HttpOnly; Secure; SameSite=Lax"
	if got := created.Header().Get("Set-Cookie"); got != wantSetCookie {
		t.Fatalf("Set-Cookie = %q, want %q", got, wantSetCookie)
	}

	current := contractRequest(handler, http.MethodGet, "https://securebrain.test/api/session", nil, nil, sessionCookie)
	if current.Code != http.StatusOK {
		t.Fatalf("GET /api/session status = %d body=%s", current.Code, current.Body.String())
	}
	assertMarshaledEnvelope(t, current, map[string]any{
		"user":       users[0],
		"mock_auth":  true,
		"disclosure": "Local demo authentication only.",
	})

	deleted := contractRequest(handler, http.MethodDelete, "https://securebrain.test/api/session", nil, nil, sessionCookie)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("DELETE /api/session status = %d body=%s", deleted.Code, deleted.Body.String())
	}
	if deleted.Body.Len() != 0 || deleted.Header().Get("Content-Type") != "" {
		t.Fatalf("DELETE body=%q Content-Type=%q", deleted.Body.String(), deleted.Header().Get("Content-Type"))
	}
	if got, want := deleted.Header().Get("Set-Cookie"), sessionCookieName+"=; Path=/; Max-Age=0; HttpOnly; Secure; SameSite=Lax"; got != want {
		t.Fatalf("Set-Cookie = %q, want %q", got, want)
	}

	expired := contractRequest(handler, http.MethodGet, "https://securebrain.test/api/session", nil, nil, sessionCookie)
	if expired.Code != http.StatusUnauthorized {
		t.Fatalf("GET deleted session status = %d body=%s", expired.Code, expired.Body.String())
	}
	assertJSONBody(t, expired, errorJSON(domain.CodeNotAuthenticated, "The mock session is invalid or expired."))
}

func assertMarshaledEnvelope(t *testing.T, response *httptest.ResponseRecorder, data any) {
	t.Helper()
	requestID := assertRequestID(t, response)
	want, err := json.Marshal(map[string]any{"data": data, "request_id": requestID})
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	if !bytes.Equal(response.Body.Bytes(), want) {
		t.Fatalf("response body:\n got: %s\nwant: %s", response.Body.Bytes(), want)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
}
