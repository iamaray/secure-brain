package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const SQLMaxRouteHops = 20

const SQLMaxPayloadBytes = int64(25 << 20)

type Config struct {
	DatabaseURL            string
	SupabaseURL            string
	SupabaseServiceRoleKey string
	OpenAIAPIKey           string
	SessionSecret          string

	HTTPAddr             string
	FrontendOrigin       string
	OpenAIModel          string
	OpenAIBaseURL        string
	StorageBucket        string
	MaxFileBytes         int64
	MaxRouteHops         int
	MaxRoutePayloadBytes int64
	MaxPreviewBytes      int64
	MaxCSVRows           int
	TransferTTL          time.Duration
	ChatHistoryMessages  int
	ChatMaxOutputTokens  int
	ChatDisabled         bool
	LogLevel             slog.Level
}

// Load reads and validates configuration from the process environment.
func Load() (Config, error) {
	return LoadFrom(os.LookupEnv)
}

// LoadFrom makes strict configuration parsing independently testable.
func LoadFrom(lookup func(string) (string, bool)) (Config, error) {
	var c Config
	var err error

	c.DatabaseURL, err = requiredDatabaseURL(lookup, "DATABASE_URL")
	if err != nil {
		return Config{}, err
	}
	c.SupabaseURL, err = requiredBaseURL(lookup, "SUPABASE_URL")
	if err != nil {
		return Config{}, err
	}
	c.SupabaseServiceRoleKey, err = required(lookup, "SUPABASE_SERVICE_ROLE_KEY")
	if err != nil {
		return Config{}, err
	}
	c.SessionSecret, err = required(lookup, "SESSION_SECRET")
	if err != nil {
		return Config{}, err
	}
	if len([]byte(c.SessionSecret)) < 32 {
		return Config{}, fmt.Errorf("SESSION_SECRET must contain at least 32 bytes")
	}

	c.HTTPAddr, err = optionalAddress(lookup, "HTTP_ADDR", "127.0.0.1:8080")
	if err != nil {
		return Config{}, err
	}
	c.FrontendOrigin, err = optionalOrigin(lookup, "FRONTEND_ORIGIN", "http://localhost:3000")
	if err != nil {
		return Config{}, err
	}
	c.OpenAIModel = optional(lookup, "OPENAI_MODEL", "gpt-5.6-luna")
	c.OpenAIBaseURL, err = optionalBaseURL(lookup, "OPENAI_BASE_URL", "https://api.openai.com")
	if err != nil {
		return Config{}, err
	}
	c.StorageBucket = optional(lookup, "STORAGE_BUCKET", "securebrain-private")

	if c.MaxFileBytes, err = int64Value(lookup, "MAX_FILE_BYTES", 10485760); err != nil {
		return Config{}, err
	}
	if c.MaxFileBytes > SQLMaxPayloadBytes {
		return Config{}, fmt.Errorf("MAX_FILE_BYTES may not exceed %d", SQLMaxPayloadBytes)
	}
	if c.MaxRouteHops, err = intValue(lookup, "MAX_ROUTE_HOPS", 20); err != nil {
		return Config{}, err
	}
	if c.MaxRouteHops > SQLMaxRouteHops {
		return Config{}, fmt.Errorf("MAX_ROUTE_HOPS may not exceed %d", SQLMaxRouteHops)
	}
	if c.MaxRoutePayloadBytes, err = int64Value(lookup, "MAX_ROUTE_PAYLOAD_BYTES", 26214400); err != nil {
		return Config{}, err
	}
	if c.MaxRoutePayloadBytes > SQLMaxPayloadBytes {
		return Config{}, fmt.Errorf("MAX_ROUTE_PAYLOAD_BYTES may not exceed %d", SQLMaxPayloadBytes)
	}
	if c.MaxPreviewBytes, err = int64Value(lookup, "MAX_PREVIEW_BYTES", 262144); err != nil {
		return Config{}, err
	}
	if c.MaxCSVRows, err = intValue(lookup, "MAX_CSV_ROWS", 500); err != nil {
		return Config{}, err
	}
	if c.TransferTTL, err = durationValue(lookup, "TRANSFER_TTL", 24*time.Hour); err != nil {
		return Config{}, err
	}
	if c.ChatHistoryMessages, err = intValue(lookup, "CHAT_HISTORY_MESSAGES", 20); err != nil {
		return Config{}, err
	}
	if c.ChatMaxOutputTokens, err = intValue(lookup, "CHAT_MAX_OUTPUT_TOKENS", 600); err != nil {
		return Config{}, err
	}
	if c.ChatDisabled, err = boolValue(lookup, "CHAT_DISABLED", false); err != nil {
		return Config{}, err
	}
	if c.LogLevel, err = levelValue(lookup, "LOG_LEVEL", slog.LevelInfo); err != nil {
		return Config{}, err
	}

	c.OpenAIAPIKey = optional(lookup, "OPENAI_API_KEY", "")
	if !c.ChatDisabled && c.OpenAIAPIKey == "" {
		return Config{}, fmt.Errorf("OPENAI_API_KEY is required unless CHAT_DISABLED=true")
	}
	return c, nil
}

func required(lookup func(string) (string, bool), name string) (string, error) {
	v, ok := lookup(name)
	v = strings.TrimSpace(v)
	if !ok || v == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return v, nil
}

func optional(lookup func(string) (string, bool), name, fallback string) string {
	v, ok := lookup(name)
	if !ok || strings.TrimSpace(v) == "" {
		return fallback
	}
	return strings.TrimSpace(v)
}

func requiredDatabaseURL(lookup func(string) (string, bool), name string) (string, error) {
	v, err := required(lookup, name)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		return "", fmt.Errorf("%s must be an absolute PostgreSQL connection URL", name)
	}
	return v, nil
}

func optionalAddress(lookup func(string) (string, bool), name, fallback string) (string, error) {
	v := optional(lookup, name, fallback)
	_, port, err := net.SplitHostPort(v)
	if err != nil {
		return "", fmt.Errorf("%s must be a host:port address", name)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("%s must contain a port from 1 through 65535", name)
	}
	return v, nil
}

func requiredBaseURL(lookup func(string) (string, bool), name string) (string, error) {
	v, err := required(lookup, name)
	if err != nil {
		return "", err
	}
	return parseBaseURL(name, v)
}

func optionalBaseURL(lookup func(string) (string, bool), name, fallback string) (string, error) {
	return parseBaseURL(name, optional(lookup, name, fallback))
}

func parseBaseURL(name, value string) (string, error) {
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%s must be an absolute http(s) URL without a query or fragment", name)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}

func optionalOrigin(lookup func(string) (string, bool), name, fallback string) (string, error) {
	v := optional(lookup, name, fallback)
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", fmt.Errorf("%s must be an http(s) origin", name)
	}
	u.Path = ""
	return u.String(), nil
}

func intValue(lookup func(string) (string, bool), name string, fallback int) (int, error) {
	v := optional(lookup, name, strconv.Itoa(fallback))
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return n, nil
}

func int64Value(lookup func(string) (string, bool), name string, fallback int64) (int64, error) {
	v := optional(lookup, name, strconv.FormatInt(fallback, 10))
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return n, nil
}

func durationValue(lookup func(string) (string, bool), name string, fallback time.Duration) (time.Duration, error) {
	v := optional(lookup, name, fallback.String())
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive Go duration", name)
	}
	return d, nil
}

func boolValue(lookup func(string) (string, bool), name string, fallback bool) (bool, error) {
	v := optional(lookup, name, strconv.FormatBool(fallback))
	switch strings.ToLower(v) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be true or false", name)
	}
}

func levelValue(lookup func(string) (string, bool), name string, fallback slog.Level) (slog.Level, error) {
	switch strings.ToLower(optional(lookup, name, fallback.String())) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("%s must be one of debug, info, warn, or error", name)
	}
}
