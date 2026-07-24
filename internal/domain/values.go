package domain

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	canonicalSlugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	queryPathPattern     = regexp.MustCompile(`^/[a-z0-9][a-z0-9/_-]*$`)
	recordIDPattern      = regexp.MustCompile(`^[[:xdigit:]]{8}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{12}$`)
)

// RecordID identifies a persisted record. It is deliberately distinct from the
// canonical IDs exposed for Brains and Services.
type RecordID string

func ParseRecordID(value string) (RecordID, error) {
	if !recordIDPattern.MatchString(value) {
		return "", fmt.Errorf("invalid record UUID")
	}
	return RecordID(value), nil
}

// BrainID and ServiceID are immutable, externally visible canonical node IDs.
type BrainID string
type ServiceID string

func ParseBrainID(value string) (BrainID, error) {
	if !strings.HasPrefix(value, "brain.") || !canonicalSlugPattern.MatchString(strings.TrimPrefix(value, "brain.")) {
		return "", fmt.Errorf("invalid Brain canonical ID")
	}
	return BrainID(value), nil
}

func ParseServiceID(value string) (ServiceID, error) {
	if !strings.HasPrefix(value, "service.") || !canonicalSlugPattern.MatchString(strings.TrimPrefix(value, "service.")) {
		return "", fmt.Errorf("invalid Service canonical ID")
	}
	return ServiceID(value), nil
}

func (id BrainID) Principal() Principal   { return Principal(id) }
func (id ServiceID) Principal() Principal { return Principal(id) }

// Principal is a canonical Brain or Service identity used by policy.
type Principal string

func ParsePrincipal(value string) (Principal, error) {
	if id, err := ParseBrainID(value); err == nil {
		return Principal(id), nil
	}
	if id, err := ParseServiceID(value); err == nil {
		return Principal(id), nil
	}
	return "", fmt.Errorf("invalid principal")
}

func (p Principal) BrainID() (BrainID, bool) {
	id, err := ParseBrainID(string(p))
	return id, err == nil
}

func (p Principal) ServiceID() (ServiceID, bool) {
	id, err := ParseServiceID(string(p))
	return id, err == nil
}

// ObjectKey is a normalized logical path within a Brain. Storage paths are a
// separate adapter concern and must never be accepted as ObjectKeys.
type ObjectKey string

func ParseObjectKey(value string) (ObjectKey, error) {
	value = strings.ReplaceAll(value, `\`, "/")
	value = strings.TrimPrefix(value, "/")
	if value == "" || len(value) > 512 {
		return "", fmt.Errorf("object key must contain 1 to 512 bytes")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("object key contains an invalid segment")
		}
	}
	for _, r := range value {
		if r == 0 || r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("object key contains a control character")
		}
	}
	return ObjectKey(value), nil
}

// QueryPathValue is the normalized path portion stored on a QueryPath record.
type QueryPathValue string

func ParseQueryPath(value string) (QueryPathValue, error) {
	if !queryPathPattern.MatchString(value) || strings.Contains(value, "//") || strings.HasSuffix(value, "/") {
		return "", fmt.Errorf("the query path is not normalized")
	}
	for _, segment := range strings.Split(strings.TrimPrefix(value, "/"), "/") {
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("the query path cannot contain traversal segments")
		}
	}
	for _, reserved := range []string{"/api", "/q", "/healthz", "/readyz"} {
		if value == reserved || strings.HasPrefix(value, reserved+"/") {
			return "", fmt.Errorf("the query path uses a reserved application prefix")
		}
	}
	return QueryPathValue(value), nil
}

// IdempotencyKey binds one caller-provided command identity.
type IdempotencyKey string

func ParseIdempotencyKey(value string) (IdempotencyKey, error) {
	value = strings.TrimSpace(value)
	if len(value) < 8 || len(value) > 200 {
		return "", fmt.Errorf("idempotency key must contain 8 to 200 characters")
	}
	return IdempotencyKey(value), nil
}

func (s NodeStatus) Valid() bool {
	return s == NodeStatusReady || s == NodeStatusDisabled
}

func ParseNodeStatus(value string) (NodeStatus, error) {
	status := NodeStatus(value)
	if !status.Valid() {
		return "", fmt.Errorf("invalid node status")
	}
	return status, nil
}

func ParseAssetFormat(value string) (AssetFormat, error) {
	format := AssetFormat(value)
	if !format.Valid() {
		return "", fmt.Errorf("invalid asset format")
	}
	return format, nil
}

func (f AssetFormat) Valid() bool {
	switch f {
	case AssetFormatText, AssetFormatMarkdown, AssetFormatCSV, AssetFormatBinary:
		return true
	default:
		return false
	}
}

func (s AssetProcessingState) Valid() bool {
	switch s {
	case AssetStateUploading, AssetStateReady, AssetStateParseFailed, AssetStateUploadFailed:
		return true
	default:
		return false
	}
}

func ParseAssetProcessingState(value string) (AssetProcessingState, error) {
	state := AssetProcessingState(value)
	if !state.Valid() {
		return "", fmt.Errorf("invalid asset processing state")
	}
	return state, nil
}

func (v Visibility) Valid() bool {
	return v == VisibilityPublic || v == VisibilityPrivate
}

func ParseVisibility(value string) (Visibility, error) {
	visibility := Visibility(value)
	if !visibility.Valid() {
		return "", fmt.Errorf("invalid visibility")
	}
	return visibility, nil
}

func (s QueryPathState) Valid() bool {
	return s == QueryPathStateDraft || s == QueryPathStateEnabled || s == QueryPathStateDisabled
}

func ParseQueryPathState(value string) (QueryPathState, error) {
	state := QueryPathState(value)
	if !state.Valid() {
		return "", fmt.Errorf("invalid query-path state")
	}
	return state, nil
}

func (o Operation) Valid() bool {
	return o == OperationRawRead || o == OperationTextSearch || o == OperationCSVQuery
}

func ParseOperation(value string) (Operation, error) {
	operation := Operation(value)
	if !operation.Valid() {
		return "", fmt.Errorf("invalid operation")
	}
	return operation, nil
}

func (m TerminalMode) Valid() bool {
	return m == TerminalModeCaller || m == TerminalModeFixed
}

func ParseTerminalMode(value string) (TerminalMode, error) {
	mode := TerminalMode(value)
	if !mode.Valid() {
		return "", fmt.Errorf("invalid terminal mode")
	}
	return mode, nil
}

func (m ExecutionMode) Valid() bool {
	return m == ExecutionModePull || m == ExecutionModePush
}

func ParseExecutionMode(value string) (ExecutionMode, error) {
	mode := ExecutionMode(value)
	if !mode.Valid() {
		return "", fmt.Errorf("invalid execution mode")
	}
	return mode, nil
}

func (s ExecutionState) Valid() bool {
	switch s {
	case ExecutionStateCreated, ExecutionStateAuthorizing, ExecutionStateReading,
		ExecutionStateProcessing, ExecutionStateDelivered, ExecutionStateFailed:
		return true
	default:
		return false
	}
}

func ParseExecutionState(value string) (ExecutionState, error) {
	state := ExecutionState(value)
	if !state.Valid() {
		return "", fmt.Errorf("invalid execution state")
	}
	return state, nil
}

func (s TransferStatus) Valid() bool {
	switch s {
	case TransferStatusPending, TransferStatusAccepted, TransferStatusRejected, TransferStatusExpired:
		return true
	default:
		return false
	}
}

func ParseTransferStatus(value string) (TransferStatus, error) {
	if value == "" {
		return "", nil
	}
	status := TransferStatus(value)
	if !status.Valid() {
		return "", fmt.Errorf("invalid transfer status")
	}
	return status, nil
}

func ParseAuditStatus(value string) (AuditStatus, error) {
	if value == "" {
		return "", nil
	}
	status := AuditStatus(value)
	switch status {
	case AuditStatusAllowed, AuditStatusDenied, AuditStatusSucceeded, AuditStatusFailed, AuditStatusPending:
		return status, nil
	default:
		return "", fmt.Errorf("invalid audit status")
	}
}
