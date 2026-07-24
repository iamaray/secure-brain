package application

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"secure-brain/internal/domain"
)

const (
	// MaxPayloadBytes is also the database and object-storage safety ceiling.
	MaxPayloadBytes int64 = 25 << 20
	MaxRouteHops          = 20
)

// Limits is the single runtime value for compatibility-sensitive application
// bounds. Configurable fields are populated at the composition boundary; fixed
// fields retain their documented v0 defaults.
type Limits struct {
	MaxFileBytes              int64
	MaxPayloadBytes           int64
	MaxRouteHops              int
	MaxPreviewBytes           int64
	MaxCSVRows                int
	MaxCSVPreviewRows         int
	DefaultCSVRows            int
	MaxCSVOffset              int
	MaxTextMatches            int
	MaxTextContextRunes       int
	MaxJSONBodyBytes          int64
	DefaultPageSize           int
	MaxPageSize               int
	TransferTTL               time.Duration
	IdempotencyTTL            time.Duration
	ChatHistoryMessages       int
	MaxChatMessageRunes       int
	ChatMaxOutputTokens       int
	MaxMultipartOverheadBytes int64
}

// DefaultLimits returns a fresh value containing the public v0 defaults.
func DefaultLimits() Limits {
	return Limits{
		MaxFileBytes:              10 << 20,
		MaxPayloadBytes:           MaxPayloadBytes,
		MaxRouteHops:              MaxRouteHops,
		MaxPreviewBytes:           256 << 10,
		MaxCSVRows:                500,
		MaxCSVPreviewRows:         100,
		DefaultCSVRows:            100,
		MaxCSVOffset:              10_000,
		MaxTextMatches:            200,
		MaxTextContextRunes:       200,
		MaxJSONBodyBytes:          1 << 20,
		DefaultPageSize:           50,
		MaxPageSize:               200,
		TransferTTL:               24 * time.Hour,
		IdempotencyTTL:            24 * time.Hour,
		ChatHistoryMessages:       20,
		MaxChatMessageRunes:       4_000,
		ChatMaxOutputTokens:       600,
		MaxMultipartOverheadBytes: 1 << 20,
	}
}

// WithDefaults fills non-positive values and applies the established hard
// safety ceilings. This preserves the previous behavior of query limits, where
// values above a fixed safety bound selected that bound.
func (limits Limits) WithDefaults() Limits {
	defaults := DefaultLimits()
	fillInt64 := func(value *int64, fallback int64) {
		if *value <= 0 {
			*value = fallback
		}
	}
	fillInt := func(value *int, fallback int) {
		if *value <= 0 {
			*value = fallback
		}
	}
	fillDuration := func(value *time.Duration, fallback time.Duration) {
		if *value <= 0 {
			*value = fallback
		}
	}

	fillInt64(&limits.MaxFileBytes, defaults.MaxFileBytes)
	fillInt64(&limits.MaxPayloadBytes, defaults.MaxPayloadBytes)
	fillInt(&limits.MaxRouteHops, defaults.MaxRouteHops)
	fillInt64(&limits.MaxPreviewBytes, defaults.MaxPreviewBytes)
	fillInt(&limits.MaxCSVRows, defaults.MaxCSVRows)
	fillInt(&limits.MaxCSVPreviewRows, defaults.MaxCSVPreviewRows)
	fillInt(&limits.DefaultCSVRows, defaults.DefaultCSVRows)
	fillInt(&limits.MaxCSVOffset, defaults.MaxCSVOffset)
	fillInt(&limits.MaxTextMatches, defaults.MaxTextMatches)
	fillInt(&limits.MaxTextContextRunes, defaults.MaxTextContextRunes)
	fillInt64(&limits.MaxJSONBodyBytes, defaults.MaxJSONBodyBytes)
	fillInt(&limits.DefaultPageSize, defaults.DefaultPageSize)
	fillInt(&limits.MaxPageSize, defaults.MaxPageSize)
	fillDuration(&limits.TransferTTL, defaults.TransferTTL)
	fillDuration(&limits.IdempotencyTTL, defaults.IdempotencyTTL)
	fillInt(&limits.ChatHistoryMessages, defaults.ChatHistoryMessages)
	fillInt(&limits.MaxChatMessageRunes, defaults.MaxChatMessageRunes)
	fillInt(&limits.ChatMaxOutputTokens, defaults.ChatMaxOutputTokens)
	fillInt64(&limits.MaxMultipartOverheadBytes, defaults.MaxMultipartOverheadBytes)

	if limits.MaxFileBytes > MaxPayloadBytes {
		limits.MaxFileBytes = MaxPayloadBytes
	}
	if limits.MaxPayloadBytes > MaxPayloadBytes {
		limits.MaxPayloadBytes = MaxPayloadBytes
	}
	if limits.MaxRouteHops > MaxRouteHops {
		limits.MaxRouteHops = MaxRouteHops
	}
	if limits.MaxCSVRows > defaults.MaxCSVRows {
		limits.MaxCSVRows = defaults.MaxCSVRows
	}
	if limits.MaxCSVPreviewRows > defaults.MaxCSVPreviewRows {
		limits.MaxCSVPreviewRows = defaults.MaxCSVPreviewRows
	}
	if limits.MaxTextMatches > defaults.MaxTextMatches {
		limits.MaxTextMatches = defaults.MaxTextMatches
	}
	if limits.MaxTextContextRunes > defaults.MaxTextContextRunes {
		limits.MaxTextContextRunes = defaults.MaxTextContextRunes
	}
	return limits
}

// MaxObjectBytes is the adapter assertion for any blob the application can
// legitimately ask object storage to persist or return.
func (limits Limits) MaxObjectBytes() int64 {
	limits = limits.WithDefaults()
	if limits.MaxFileBytes > limits.MaxPayloadBytes {
		return limits.MaxFileBytes
	}
	return limits.MaxPayloadBytes
}

// Clock owns reads of wall-clock time in application workflows.
type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time {
	return time.Now()
}

type ClockFunc func() time.Time

func (clock ClockFunc) Now() time.Time {
	return clock()
}

// IDGenerator owns externally visible request and persisted-record identities.
type IDGenerator interface {
	NewUUID() domain.RecordID
	NewRequestID() string
}

// RandomIDGenerator generates cryptographically random identities. Clock is
// used only for the compatibility fallback of request-ID generation.
type RandomIDGenerator struct {
	Reader io.Reader
	Clock  Clock
}

func (generator RandomIDGenerator) randomReader() io.Reader {
	if generator.Reader != nil {
		return generator.Reader
	}
	return rand.Reader
}

func (generator RandomIDGenerator) clock() Clock {
	if generator.Clock != nil {
		return generator.Clock
	}
	return SystemClock{}
}

func (generator RandomIDGenerator) NewUUID() domain.RecordID {
	var value [16]byte
	if _, err := io.ReadFull(generator.randomReader(), value[:]); err != nil {
		panic("crypto/rand unavailable")
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	id, err := domain.ParseRecordID(strings.Join([]string{encoded[:8], encoded[8:12], encoded[12:16], encoded[16:20], encoded[20:]}, "-"))
	if err != nil {
		panic("generated invalid UUID")
	}
	return id
}

func (generator RandomIDGenerator) NewRequestID() string {
	value := make([]byte, 12)
	if _, err := io.ReadFull(generator.randomReader(), value); err != nil {
		return fmt.Sprintf("req_%d", generator.clock().Now().UnixNano())
	}
	return "req_" + hex.EncodeToString(value)
}
