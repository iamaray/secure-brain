package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func validEnvironment() map[string]string {
	return map[string]string{
		"DATABASE_URL":              "postgresql://example.invalid/postgres",
		"SUPABASE_URL":              "https://project.supabase.co/",
		"SUPABASE_SERVICE_ROLE_KEY": "service-secret",
		"OPENAI_API_KEY":            "openai-secret",
		"SESSION_SECRET":            "01234567890123456789012345678901",
	}
}

func lookup(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := values[name]
		return v, ok
	}
}

func TestLoadFromDefaultsAndNormalization(t *testing.T) {
	c, err := LoadFrom(lookup(validEnvironment()))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if c.SupabaseURL != "https://project.supabase.co" {
		t.Fatalf("SupabaseURL = %q", c.SupabaseURL)
	}
	if c.HTTPAddr != "127.0.0.1:8080" || c.FrontendOrigin != "http://localhost:3000" {
		t.Fatalf("unexpected HTTP defaults: %#v", c)
	}
	if c.OpenAIModel != "gpt-5.6-luna" || c.OpenAIBaseURL != "https://api.openai.com" {
		t.Fatalf("unexpected OpenAI defaults: %#v", c)
	}
	if c.StorageBucket != "securebrain-private" || c.MaxFileBytes != 10485760 || c.MaxRouteHops != 20 {
		t.Fatalf("unexpected storage/route defaults: %#v", c)
	}
	if c.MaxRoutePayloadBytes != 26214400 || c.MaxPreviewBytes != 262144 || c.MaxCSVRows != 500 {
		t.Fatalf("unexpected size defaults: %#v", c)
	}
	if c.TransferTTL != 24*time.Hour || c.ChatHistoryMessages != 20 || c.ChatMaxOutputTokens != 600 {
		t.Fatalf("unexpected transfer/chat defaults: %#v", c)
	}
	if c.ChatDisabled || c.LogLevel != slog.LevelInfo {
		t.Fatalf("unexpected bool/log defaults: %#v", c)
	}
}

func TestLoadFromAllowsMissingOpenAIKeyOnlyWhenChatDisabled(t *testing.T) {
	values := validEnvironment()
	delete(values, "OPENAI_API_KEY")
	if _, err := LoadFrom(lookup(values)); err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("expected redacted missing-key error, got %v", err)
	}
	values["CHAT_DISABLED"] = "true"
	c, err := LoadFrom(lookup(values))
	if err != nil {
		t.Fatalf("disabled chat: %v", err)
	}
	if !c.ChatDisabled || c.OpenAIAPIKey != "" {
		t.Fatalf("unexpected disabled chat config: %#v", c)
	}
}

func TestLoadFromRejectsInvalidValuesWithoutLeakingSecrets(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"short session secret", "SESSION_SECRET", "too-short-secret"},
		{"bad database URL", "DATABASE_URL", "http://secret.invalid/postgres"},
		{"bad URL", "SUPABASE_URL", "secret-value-not-a-url"},
		{"bad HTTP address", "HTTP_ADDR", "localhost"},
		{"route SQL ceiling", "MAX_ROUTE_HOPS", "21"},
		{"file SQL ceiling", "MAX_FILE_BYTES", "26214401"},
		{"payload SQL ceiling", "MAX_ROUTE_PAYLOAD_BYTES", "26214401"},
		{"zero integer", "MAX_CSV_ROWS", "0"},
		{"bad duration", "TRANSFER_TTL", "tomorrow"},
		{"strict boolean", "CHAT_DISABLED", "1"},
		{"bad level", "LOG_LEVEL", "trace"},
		{"origin with path", "FRONTEND_ORIGIN", "https://example.com/app"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := validEnvironment()
			values[tt.key] = tt.value
			_, err := LoadFrom(lookup(values))
			if err == nil {
				t.Fatal("expected error")
			}
			if strings.Contains(err.Error(), tt.value) {
				t.Fatalf("error leaked configured value: %v", err)
			}
		})
	}
}
