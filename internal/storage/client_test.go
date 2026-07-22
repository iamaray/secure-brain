package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"secure-brain/internal/domain"
)

const testServiceKey = "test-service-role-secret"

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "securebrain-private", testServiceKey, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func assertAuth(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Header.Get("apikey") != testServiceKey || r.Header.Get("Authorization") != "Bearer "+testServiceKey {
		t.Fatalf("missing service authentication headers")
	}
}

func TestClientPutUsesPrivateObjectEndpointAndHeaders(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/storage/v1/object/securebrain-private/brains/id/assets/id/sha" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.EscapedPath())
		}
		if r.Header.Get("x-upsert") != "true" || r.Header.Get("Content-Type") != "text/plain" {
			t.Fatalf("unexpected upload headers: %v", r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "payload" {
			t.Fatalf("body = %q", body)
		}
		w.WriteHeader(http.StatusOK)
	})
	if err := client.Put(context.Background(), "brains/id/assets/id/sha", "text/plain", strings.NewReader("payload"), 7, true); err != nil {
		t.Fatalf("Put: %v", err)
	}
}

func TestClientGetReturnsMetadataAndBody(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		if r.URL.Path != "/storage/v1/object/authenticated/securebrain-private/object/path" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("ETag", `"abc"`)
		_, _ = io.WriteString(w, "hello")
	})
	body, metadata, err := client.Get(context.Background(), "object/path")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer body.Close()
	got, _ := io.ReadAll(body)
	if string(got) != "hello" || metadata.MediaType != "text/plain; charset=utf-8" || metadata.ETag != `"abc"` || metadata.Size != 5 {
		t.Fatalf("unexpected result: %q %#v", got, metadata)
	}
}

func TestClientDeleteRetriesServerFailure(t *testing.T) {
	var attempts atomic.Int32
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		if r.Method != http.MethodDelete || r.URL.Path != "/storage/v1/object/securebrain-private" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"prefixes":["one","two/path"]}` {
			t.Fatalf("body = %s", body)
		}
		if attempts.Add(1) == 1 {
			http.Error(w, "provider-secret-body", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	if err := client.Delete(context.Background(), []string{"one", "two/path"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d", attempts.Load())
	}
}

func TestProviderErrorIsTypedAndRedacted(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "provider-secret-body", http.StatusBadGateway)
	})
	err := client.Put(context.Background(), "object", "text/plain", strings.NewReader("x"), 1, false)
	var appErr *domain.Error
	if !errors.As(err, &appErr) || appErr.Code != domain.CodeStorageProviderError {
		t.Fatalf("error = %#v", err)
	}
	if strings.Contains(err.Error(), "provider-secret-body") || strings.Contains(err.Error(), testServiceKey) {
		t.Fatalf("provider error leaked sensitive data: %v", err)
	}
}

func TestClientRejectsUnsafePaths(t *testing.T) {
	client := newTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("request should not be sent")
	})
	for _, path := range []string{"", "/absolute", "a//b", "a/../b", "a/./b"} {
		if _, _, err := client.Get(context.Background(), path); err == nil {
			t.Fatalf("Get(%q) succeeded", path)
		}
	}
}
