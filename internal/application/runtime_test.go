package application

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("unavailable")
}

func TestDefaultLimits(t *testing.T) {
	got := DefaultLimits()
	if got.MaxFileBytes != 10<<20 || got.MaxPayloadBytes != 25<<20 ||
		got.MaxRouteHops != 20 || got.MaxPreviewBytes != 256<<10 {
		t.Fatalf("unexpected size and route limits: %#v", got)
	}
	if got.MaxCSVRows != 500 || got.MaxCSVPreviewRows != 100 ||
		got.MaxTextMatches != 200 || got.DefaultPageSize != 50 || got.MaxPageSize != 200 {
		t.Fatalf("unexpected query and pagination limits: %#v", got)
	}
	if got.TransferTTL != 24*time.Hour || got.ChatHistoryMessages != 20 ||
		got.MaxChatMessageRunes != 4_000 || got.MaxJSONBodyBytes != 1<<20 {
		t.Fatalf("unexpected transfer, chat, and JSON limits: %#v", got)
	}
}

func TestLimitsWithDefaultsAndSafetyCeilings(t *testing.T) {
	got := (Limits{
		MaxFileBytes:        MaxPayloadBytes + 1,
		MaxPayloadBytes:     MaxPayloadBytes + 1,
		MaxRouteHops:        MaxRouteHops + 1,
		MaxCSVRows:          501,
		MaxTextMatches:      201,
		MaxTextContextRunes: 201,
	}).WithDefaults()
	want := DefaultLimits()
	want.MaxFileBytes = MaxPayloadBytes
	if got != want {
		t.Fatalf("limits = %#v, want %#v", got, want)
	}
}

func TestRandomIDGenerator(t *testing.T) {
	generator := RandomIDGenerator{Reader: bytes.NewReader(make([]byte, 28))}
	if got := generator.NewUUID(); got != "00000000-0000-4000-8000-000000000000" {
		t.Fatalf("UUID = %q", got)
	}
	if got := generator.NewRequestID(); got != "req_000000000000000000000000" {
		t.Fatalf("request ID = %q", got)
	}
}

func TestRequestIDFallbackUsesInjectedClock(t *testing.T) {
	now := time.Unix(0, 123)
	generator := RandomIDGenerator{Reader: failingReader{}, Clock: ClockFunc(func() time.Time { return now })}
	if got := generator.NewRequestID(); got != "req_123" {
		t.Fatalf("request ID = %q", got)
	}
}
