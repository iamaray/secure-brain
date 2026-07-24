package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"secure-brain/internal/application"
	"secure-brain/internal/domain"
)

func TestStrictJSON(t *testing.T) {
	for _, tc := range []struct {
		body string
		ok   bool
	}{{`{"value":"yes"}`, true}, {`{"other":1}`, false}, {`{"value":"yes"} {}`, false}} {
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(tc.body))
		recorder := httptest.NewRecorder()
		var dst struct {
			Value string `json:"value"`
		}
		err := decodeJSON(recorder, req, &dst)
		if (err == nil) != tc.ok {
			t.Errorf("body %s error = %v", tc.body, err)
		}
	}
}

func TestStrictJSONUsesRuntimeLimit(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"value":"too long"}`))
	limits := application.DefaultLimits()
	limits.MaxJSONBodyBytes = 8
	req = req.WithContext(context.WithValue(req.Context(), limitsKey, limits))
	recorder := httptest.NewRecorder()
	var dst struct {
		Value string `json:"value"`
	}
	if err := decodeJSON(recorder, req, &dst); err == nil {
		t.Fatal("oversized JSON was accepted")
	}
}

func TestStableStatusMapping(t *testing.T) {
	if got := statusForCode(domain.CodePathNotFound); got != http.StatusNotFound {
		t.Fatalf("got %d", got)
	}
	if got := statusForCode(domain.CodeChatProviderError); got != http.StatusBadGateway {
		t.Fatalf("got %d", got)
	}
}
