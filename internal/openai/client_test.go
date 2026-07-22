package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"secure-brain/internal/domain"
)

func TestBrainInstructionsExact(t *testing.T) {
	want := `You are the SecureBrain demo assistant for the Brain named Maya Brain
(brain.maya). Answer helpfully and concisely. This is a simulated
Brain-aware experience. You have not been given, cannot inspect, and must not claim
knowledge of files uploaded to this Brain. If asked what is in those files, state
that this v0 chat is not grounded in uploaded content. Do not imply that you can
modify Brains, files, routes, permissions, Services, or transfers.`
	if got := BrainInstructions("Maya Brain", "brain.maya"); got != want {
		t.Fatalf("instructions mismatch\nwant: %q\n got: %q", want, got)
	}
}

func TestRespondSendsExactShapeAndExtractsAssistantOutputText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer openai-test-key" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected headers: %v", r.Header)
		}
		var got map[string]any
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got["model"] != "gpt-test" || got["store"] != false || got["instructions"] != "fixed" || got["max_output_tokens"] != float64(600) {
			t.Fatalf("unexpected request: %#v", got)
		}
		text, ok := got["text"].(map[string]any)
		if !ok || text["verbosity"] != "low" {
			t.Fatalf("unexpected text config: %#v", got["text"])
		}
		input, ok := got["input"].([]any)
		if !ok || len(input) != 2 {
			t.Fatalf("unexpected input: %#v", got["input"])
		}
		io.WriteString(w, `{"output":[`+
			`{"type":"reasoning","content":[{"type":"output_text","text":"ignored"}]},`+
			`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello"},{"type":"refusal","text":"ignored"}]},`+
			`{"type":"message","role":"user","content":[{"type":"output_text","text":"ignored"}]},`+
			`{"type":"message","role":"assistant","content":[{"type":"output_text","text":" world"}]}`+
			`]}`)
	}))
	defer server.Close()
	client, err := NewClient(server.URL+"/", "openai-test-key", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.Respond(context.Background(), Request{
		Model:           "gpt-test",
		Instructions:    "fixed",
		Input:           []Message{{Role: "user", Content: "earlier"}, {Role: "user", Content: "now"}},
		MaxOutputTokens: 600,
	})
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if got != "Hello world" {
		t.Fatalf("response = %q", got)
	}
}

func TestRespondClassifiesMalformedMissingAndProviderErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"malformed", http.StatusOK, `{"output":`},
		{"missing output", http.StatusOK, `{"output":[{"type":"message","role":"assistant","content":[]}]}`},
		{"provider", http.StatusTooManyRequests, `provider-secret-body`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			client, _ := NewClient(server.URL, "openai-test-key", server.Client())
			_, err := client.Respond(context.Background(), Request{Model: "gpt-test", MaxOutputTokens: 1})
			var appErr *domain.Error
			if !errors.As(err, &appErr) || appErr.Code != domain.CodeChatProviderError {
				t.Fatalf("error = %#v", err)
			}
			if strings.Contains(err.Error(), tt.body) || strings.Contains(err.Error(), "openai-test-key") {
				t.Fatalf("provider error leaked content: %v", err)
			}
		})
	}
}
