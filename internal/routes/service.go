package routes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"secure-brain/internal/domain"
)

// ServiceExecutor is the deliberately narrow internal Service boundary.
type ServiceExecutor interface {
	Execute(ctx context.Context, service domain.Service, in domain.Payload) (domain.Payload, error)
}

// IdentityServiceExecutor is the v0 implementation. It returns the exact input
// value: no copying, serialization, reordering, or metadata mutation.
type IdentityServiceExecutor struct{}

func (IdentityServiceExecutor) Execute(ctx context.Context, _ domain.Service, in domain.Payload) (domain.Payload, error) {
	if err := ctx.Err(); err != nil {
		return domain.Payload{}, err
	}
	return in, nil
}

type HopResult struct {
	HopIndex           int
	ServiceID          domain.RecordID
	ServiceCanonicalID domain.ServiceID
	Status             domain.HopStatus
	InputSHA256        string
	OutputSHA256       string
	DurationMS         int
	ErrorCode          *domain.Code
}

// ExecuteHops traverses the finite supplied slice exactly once. Duplicate
// Services remain separate occurrences and produce separate result rows.
func ExecuteHops(ctx context.Context, executor ServiceExecutor, services []domain.Service, input domain.Payload) (domain.Payload, []HopResult, error) {
	if len(services) > DefaultMaxRouteHops {
		return domain.Payload{}, nil, domain.NewError(domain.CodeRouteTooLong, "The saved route exceeds the maximum Service-hop limit.")
	}
	if len(services) == 0 {
		return input, []HopResult{}, nil
	}
	if executor == nil {
		return domain.Payload{}, nil, domain.NewError(domain.CodeServiceHopFailed, "Service executor is unavailable.")
	}
	current := input
	results := make([]HopResult, 0, len(services))
	for i, service := range services {
		if err := ctx.Err(); err != nil {
			return domain.Payload{}, results, domain.WrapError(domain.CodeServiceHopFailed, "Service traversal was canceled.", err)
		}
		inputChecksum := checksum(current.Bytes)
		inputEnvelopeChecksum, fingerprintErr := envelopeChecksum(current)
		if fingerprintErr != nil {
			return domain.Payload{}, results, domain.WrapError(domain.CodeServiceHopFailed, "The payload metadata cannot be verified.", fingerprintErr)
		}
		started := time.Now()
		output, err := executor.Execute(ctx, service, current)
		duration := time.Since(started).Milliseconds()
		if duration < 0 {
			duration = 0
		}
		if duration > int64(^uint(0)>>1) {
			duration = int64(^uint(0) >> 1)
		}
		// Hash the returned bytes even on an executor error. This both satisfies
		// the per-occurrence trace contract and records an in-place mutation by a
		// failing implementation without ever releasing its output.
		outputChecksum := checksum(output.Bytes)
		outputEnvelopeChecksum, envelopeErr := envelopeChecksum(output)
		result := HopResult{
			HopIndex: i, ServiceID: service.ID, ServiceCanonicalID: service.CanonicalID,
			Status: domain.HopStatusCompleted, InputSHA256: inputChecksum,
			OutputSHA256: outputChecksum, DurationMS: int(duration),
		}
		if err != nil || envelopeErr != nil || inputChecksum != outputChecksum || inputEnvelopeChecksum != outputEnvelopeChecksum {
			code := domain.CodeServiceHopFailed
			result.Status = domain.HopStatusFailed
			result.ErrorCode = &code
			results = append(results, result)
			if err != nil {
				return domain.Payload{}, results, domain.WrapError(domain.CodeServiceHopFailed, "A route Service failed.", err)
			}
			if envelopeErr != nil {
				return domain.Payload{}, results, domain.WrapError(domain.CodeServiceHopFailed, "A route Service returned unverifiable payload metadata.", envelopeErr)
			}
			if inputEnvelopeChecksum != outputEnvelopeChecksum {
				return domain.Payload{}, results, domain.NewError(domain.CodeServiceHopFailed, "A route Service changed the payload envelope.")
			}
			return domain.Payload{}, results, domain.NewError(domain.CodeServiceHopFailed, "A route Service changed the payload bytes.")
		}
		results = append(results, result)
		current = output
	}
	return current, results, nil
}

func checksum(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func envelopeChecksum(payload domain.Payload) (string, error) {
	value, err := json.Marshal(struct {
		MediaType         string         `json:"media_type"`
		SuggestedFilename string         `json:"suggested_filename"`
		Metadata          map[string]any `json:"metadata"`
	}{payload.MediaType, payload.SuggestedFilename, payload.Metadata})
	if err != nil {
		return "", err
	}
	return checksum(value), nil
}

// IsServiceHopFailure reports the stable application error while preserving
// wrapped executor and context causes for diagnostics.
func IsServiceHopFailure(err error) bool {
	var appErr *domain.Error
	return errors.As(err, &appErr) && appErr.Code == domain.CodeServiceHopFailed
}
