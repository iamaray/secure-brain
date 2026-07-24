package domain

import "fmt"

type Code string

const (
	CodeInvalidRequest          Code = "INVALID_REQUEST"
	CodeNotAuthenticated        Code = "NOT_AUTHENTICATED"
	CodeNotAuthorized           Code = "NOT_AUTHORIZED"
	CodeNodeNotFound            Code = "NODE_NOT_FOUND"
	CodePathNotFound            Code = "PATH_NOT_FOUND"
	CodePathDisabled            Code = "PATH_DISABLED"
	CodeOperationNotAllowed     Code = "OPERATION_NOT_ALLOWED"
	CodeInitiatorNotOwned       Code = "INITIATOR_NOT_OWNED"
	CodeDestinationMismatch     Code = "DESTINATION_MISMATCH"
	CodePrincipalNotAuthorized  Code = "PRINCIPAL_NOT_AUTHORIZED"
	CodeRouteInvalid            Code = "ROUTE_INVALID"
	CodeRouteTooLong            Code = "ROUTE_TOO_LONG"
	CodeAssetUnavailable        Code = "ASSET_UNAVAILABLE"
	CodeAssetParseFailed        Code = "ASSET_PARSE_FAILED"
	CodeQueryInvalid            Code = "QUERY_INVALID"
	CodeServiceHopFailed        Code = "SERVICE_HOP_FAILED"
	CodeTransferAlreadyResolved Code = "TRANSFER_ALREADY_RESOLVED"
	CodeTransferExpired         Code = "TRANSFER_EXPIRED"
	CodeNameAlreadyExists       Code = "NAME_ALREADY_EXISTS"
	CodeResourceInUse           Code = "RESOURCE_IN_USE"
	CodeConfigVersionConflict   Code = "CONFIG_VERSION_CONFLICT"
	CodeIdempotencyKeyRequired  Code = "IDEMPOTENCY_KEY_REQUIRED"
	CodeIdempotencyKeyReused    Code = "IDEMPOTENCY_KEY_REUSED"
	CodePayloadTooLarge         Code = "PAYLOAD_TOO_LARGE"
	CodeStorageProviderError    Code = "STORAGE_PROVIDER_ERROR"
	CodeChatProviderError       Code = "CHAT_PROVIDER_ERROR"
)

// Error is the single application error shape. Cause and sensitive internals are
// intentionally excluded from JSON responses.
type Error struct {
	Code    Code         `json:"code"`
	Message string       `json:"message"`
	Cause   error        `json:"-"`
	Details ErrorDetails `json:"details,omitempty"`
}

// ErrorDetails contains bounded, non-sensitive structured context whose allowed
// keys are owned by an error code. ROUTE_INVALID currently allows "fields".
type ErrorDetails map[string]any

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Message == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func NewError(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

func WrapError(code Code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Cause: cause}
}
