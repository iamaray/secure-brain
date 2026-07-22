package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"secure-brain/internal/domain"
)

const maxJSONBody = 1 << 20

type contextKey string

const (
	requestIDKey contextKey = "request_id"
	userKey      contextKey = "user"
)

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (w *responseRecorder) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseRecorder) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return domain.WrapError(domain.CodeInvalidRequest, "The JSON request body is invalid.", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return domain.NewError(domain.CodeInvalidRequest, "The JSON request body must contain one value.")
	}
	return nil
}

func writeData(w http.ResponseWriter, r *http.Request, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "request_id": requestID(r.Context())})
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	appErr := &domain.Error{Code: domain.CodeInvalidRequest, Message: "The request could not be completed."}
	if !errors.As(err, &appErr) {
		appErr = domain.WrapError(domain.CodeInvalidRequest, "The request could not be completed.", err)
	}
	status := statusForCode(appErr.Code)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": appErr, "request_id": requestID(r.Context())})
}

func statusForCode(code domain.Code) int {
	switch code {
	case domain.CodeNotAuthenticated:
		return http.StatusUnauthorized
	case domain.CodeNotAuthorized, domain.CodeInitiatorNotOwned, domain.CodePrincipalNotAuthorized:
		return http.StatusForbidden
	case domain.CodeNodeNotFound, domain.CodePathNotFound:
		return http.StatusNotFound
	case domain.CodeNameAlreadyExists, domain.CodeResourceInUse, domain.CodeConfigVersionConflict,
		domain.CodeIdempotencyKeyReused, domain.CodeTransferAlreadyResolved:
		return http.StatusConflict
	case domain.CodePayloadTooLarge:
		return http.StatusRequestEntityTooLarge
	case domain.CodeRouteInvalid, domain.CodeRouteTooLong, domain.CodePathDisabled,
		domain.CodeOperationNotAllowed, domain.CodeDestinationMismatch, domain.CodeAssetUnavailable,
		domain.CodeAssetParseFailed, domain.CodeQueryInvalid, domain.CodeIdempotencyKeyRequired:
		return http.StatusUnprocessableEntity
	case domain.CodeStorageProviderError, domain.CodeChatProviderError:
		return http.StatusBadGateway
	case domain.CodeTransferExpired:
		return http.StatusGone
	default:
		return http.StatusBadRequest
	}
}

func requestID(ctx context.Context) string {
	if value, ok := ctx.Value(requestIDKey).(string); ok {
		return value
	}
	return "req_unknown"
}

func newRequestID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("req_%d", time.Now().UnixNano())
	}
	return "req_" + hex.EncodeToString(b)
}

func withMiddleware(next http.Handler, logger *slog.Logger, frontendOrigin string) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		requestID := newRequestID()
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/q/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		origin := r.Header.Get("Origin")
		if origin != "" {
			if origin != frontendOrigin {
				writeError(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, requestID)), domain.NewError(domain.CodeNotAuthorized, "The request origin is not allowed."))
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", frontendOrigin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key, If-Match")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		recorder := &responseRecorder{ResponseWriter: w}
		ctx := context.WithValue(r.Context(), requestIDKey, requestID)
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("request panic", "request_id", requestID, "panic_type", fmt.Sprintf("%T", recovered), "stack", string(debug.Stack()))
				if recorder.status == 0 {
					writeError(recorder, r.WithContext(ctx), domain.NewError(domain.CodeInvalidRequest, "An internal error occurred."))
				}
			}
			status := recorder.status
			if status == 0 {
				status = http.StatusOK
			}
			logger.Info("request completed", "request_id", requestID, "method", r.Method, "path", safeLogPath(r.URL.Path), "status", status, "duration_ms", time.Since(started).Milliseconds())
		}()
		next.ServeHTTP(recorder, r.WithContext(ctx))
	})
}

func safeLogPath(path string) string {
	if strings.HasPrefix(path, "/q/") {
		return "/q/{sourceBrainId}/{queryPath...}"
	}
	return path
}
