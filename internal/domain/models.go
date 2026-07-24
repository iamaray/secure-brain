package domain

import "time"

type NodeStatus string

const (
	NodeStatusReady    NodeStatus = "ready"
	NodeStatusDisabled NodeStatus = "disabled"
)

type AssetFormat string

const (
	AssetFormatText     AssetFormat = "text"
	AssetFormatMarkdown AssetFormat = "markdown"
	AssetFormatCSV      AssetFormat = "csv"
	AssetFormatBinary   AssetFormat = "binary"
)

type AssetProcessingState string

const (
	AssetStateUploading    AssetProcessingState = "uploading"
	AssetStateReady        AssetProcessingState = "ready"
	AssetStateParseFailed  AssetProcessingState = "parse_failed"
	AssetStateUploadFailed AssetProcessingState = "upload_failed"
)

type Visibility string

const (
	VisibilityPublic  Visibility = "public"
	VisibilityPrivate Visibility = "private"
)

type QueryPathState string

const (
	QueryPathStateDraft    QueryPathState = "draft"
	QueryPathStateEnabled  QueryPathState = "enabled"
	QueryPathStateDisabled QueryPathState = "disabled"
)

type Operation string

const (
	OperationRawRead    Operation = "raw_read"
	OperationTextSearch Operation = "text_search"
	OperationCSVQuery   Operation = "csv_query"
)

type TerminalMode string

const (
	TerminalModeCaller TerminalMode = "caller"
	TerminalModeFixed  TerminalMode = "fixed"
)

type ExecutionMode string

const (
	ExecutionModePull ExecutionMode = "pull"
	ExecutionModePush ExecutionMode = "push"
)

type ExecutionState string

const (
	ExecutionStateCreated     ExecutionState = "created"
	ExecutionStateAuthorizing ExecutionState = "authorizing"
	ExecutionStateReading     ExecutionState = "reading"
	ExecutionStateProcessing  ExecutionState = "processing"
	ExecutionStateDelivered   ExecutionState = "delivered"
	ExecutionStateFailed      ExecutionState = "failed"
)

type HopStatus string

const (
	HopStatusCompleted HopStatus = "completed"
	HopStatusFailed    HopStatus = "failed"
)

type TransferStatus string

const (
	TransferStatusPending  TransferStatus = "pending"
	TransferStatusAccepted TransferStatus = "accepted"
	TransferStatusRejected TransferStatus = "rejected"
	TransferStatusExpired  TransferStatus = "expired"
)

type ChatRole string

const (
	ChatRoleUser      ChatRole = "user"
	ChatRoleAssistant ChatRole = "assistant"
)

type AuditStatus string

const (
	AuditStatusAllowed   AuditStatus = "allowed"
	AuditStatusDenied    AuditStatus = "denied"
	AuditStatusSucceeded AuditStatus = "succeeded"
	AuditStatusFailed    AuditStatus = "failed"
	AuditStatusPending   AuditStatus = "pending"
)

type User struct {
	ID          RecordID  `json:"id"`
	Handle      string    `json:"handle"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
}

type Brain struct {
	ID          RecordID   `json:"id"`
	OwnerUserID RecordID   `json:"owner_user_id,omitempty"`
	Slug        string     `json:"slug"`
	CanonicalID BrainID    `json:"canonical_id"`
	DisplayName string     `json:"display_name"`
	Status      NodeStatus `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type Service struct {
	ID             RecordID   `json:"id"`
	OwnerUserID    RecordID   `json:"owner_user_id,omitempty"`
	Slug           string     `json:"slug"`
	CanonicalID    ServiceID  `json:"canonical_id"`
	DisplayName    string     `json:"display_name"`
	Status         NodeStatus `json:"status"`
	CapabilityTags []string   `json:"capability_tags"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type MockSession struct {
	ID         RecordID  `json:"id"`
	TokenHash  []byte    `json:"-"`
	UserID     RecordID  `json:"user_id"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type Asset struct {
	ID               RecordID             `json:"id"`
	BrainID          RecordID             `json:"brain_id"`
	ObjectKey        ObjectKey            `json:"object_key"`
	StoragePath      string               `json:"-"`
	OriginalFilename string               `json:"original_filename"`
	MediaType        string               `json:"media_type"`
	ByteSize         int64                `json:"byte_size"`
	SHA256           string               `json:"sha256,omitempty"`
	Format           AssetFormat          `json:"format"`
	ProcessingState  AssetProcessingState `json:"processing_state"`
	ParseError       *string              `json:"parse_error,omitempty"`
	CreatedAt        time.Time            `json:"created_at"`
	UpdatedAt        time.Time            `json:"updated_at"`
}

type QueryPath struct {
	ID            RecordID       `json:"id"`
	BrainID       RecordID       `json:"brain_id"`
	Path          QueryPathValue `json:"path"`
	Visibility    Visibility     `json:"visibility"`
	State         QueryPathState `json:"state"`
	Operations    []Operation    `json:"operations"`
	ConfigVersion int64          `json:"config_version"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type QueryPathAsset struct {
	QueryPathID RecordID `json:"query_path_id"`
	AssetID     RecordID `json:"asset_id"`
	Position    int      `json:"position"`
}

type Route struct {
	ID                 RecordID     `json:"id"`
	QueryPathID        RecordID     `json:"query_path_id"`
	TerminalMode       TerminalMode `json:"terminal_mode"`
	DestinationBrainID *RecordID    `json:"destination_brain_id,omitempty"`
	CreatedAt          time.Time    `json:"created_at"`
	UpdatedAt          time.Time    `json:"updated_at"`
}

type RouteHop struct {
	RouteID   RecordID `json:"route_id"`
	HopIndex  int      `json:"hop_index"`
	ServiceID RecordID `json:"service_id"`
}

type RouteExecution struct {
	ID                     RecordID       `json:"id"`
	Mode                   ExecutionMode  `json:"mode"`
	QueryPathID            *RecordID      `json:"query_path_id,omitempty"`
	ActorUserID            *RecordID      `json:"actor_user_id,omitempty"`
	InitiatingBrainID      *RecordID      `json:"initiating_brain_id,omitempty"`
	SourceBrainID          *RecordID      `json:"source_brain_id,omitempty"`
	DestinationBrainID     *RecordID      `json:"destination_brain_id,omitempty"`
	SourceCanonicalID      BrainID        `json:"source_canonical_id"`
	SourcePath             QueryPathValue `json:"source_path"`
	DestinationCanonicalID *BrainID       `json:"destination_canonical_id,omitempty"`
	Operation              Operation      `json:"operation"`
	State                  ExecutionState `json:"state"`
	ErrorCode              *Code          `json:"error_code,omitempty"`
	ErrorMessage           *string        `json:"error_message,omitempty"`
	CreatedAt              time.Time      `json:"created_at"`
	StartedAt              *time.Time     `json:"started_at,omitempty"`
	CompletedAt            *time.Time     `json:"completed_at,omitempty"`
}

type ExecutionHop struct {
	ID                 RecordID  `json:"id"`
	ExecutionID        RecordID  `json:"execution_id"`
	HopIndex           int       `json:"hop_index"`
	ServiceID          *RecordID `json:"service_id,omitempty"`
	ServiceCanonicalID ServiceID `json:"service_canonical_id"`
	Status             HopStatus `json:"status"`
	InputSHA256        string    `json:"input_sha256"`
	OutputSHA256       string    `json:"output_sha256"`
	DurationMS         int       `json:"duration_ms"`
	ErrorCode          *Code     `json:"error_code,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
}

type Transfer struct {
	ID                     RecordID       `json:"id"`
	ExecutionID            RecordID       `json:"execution_id"`
	SourceBrainID          *RecordID      `json:"source_brain_id,omitempty"`
	DestinationBrainID     *RecordID      `json:"destination_brain_id,omitempty"`
	SourceCanonicalID      BrainID        `json:"source_canonical_id"`
	DestinationCanonicalID BrainID        `json:"destination_canonical_id"`
	Status                 TransferStatus `json:"status"`
	StoragePath            string         `json:"-"`
	SuggestedObjectKey     ObjectKey      `json:"suggested_object_key"`
	SuggestedFilename      string         `json:"suggested_filename"`
	MediaType              string         `json:"media_type"`
	ByteSize               int64          `json:"byte_size"`
	SHA256                 string         `json:"sha256"`
	AcceptedAssetID        *RecordID      `json:"accepted_asset_id,omitempty"`
	CreatedAt              time.Time      `json:"created_at"`
	ExpiresAt              time.Time      `json:"expires_at"`
	ResolvedAt             *time.Time     `json:"resolved_at,omitempty"`
}

type ChatMessage struct {
	ID        RecordID  `json:"id"`
	BrainID   RecordID  `json:"brain_id"`
	UserID    RecordID  `json:"user_id"`
	Role      ChatRole  `json:"role"`
	Content   string    `json:"content"`
	Model     *string   `json:"model,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type AuditEvent struct {
	ID           RecordID      `json:"id"`
	EventType    string        `json:"event_type"`
	ActorUserID  *RecordID     `json:"actor_user_id,omitempty"`
	ResourceType string        `json:"resource_type"`
	ResourceID   *RecordID     `json:"resource_id,omitempty"`
	BrainID      *RecordID     `json:"brain_id,omitempty"`
	ServiceID    *RecordID     `json:"service_id,omitempty"`
	ExecutionID  *RecordID     `json:"execution_id,omitempty"`
	Status       AuditStatus   `json:"status"`
	Metadata     AuditMetadata `json:"metadata"`
	CreatedAt    time.Time     `json:"created_at"`
}

// AuditMetadata contains bounded, non-sensitive event context. Each event type
// owns its allowed keys; payload bytes, chat text, and secrets are forbidden.
type AuditMetadata map[string]any

// Payload is the byte-preserving representation passed through route Services.
type Payload struct {
	Bytes             []byte          `json:"-"`
	MediaType         string          `json:"media_type"`
	SuggestedFilename string          `json:"suggested_filename"`
	Metadata          PayloadMetadata `json:"metadata"`
}

// PayloadMetadata is operation-specific structured context. Current allowed
// keys are operation, asset_count, asset_ids, structured, match_count,
// truncated, file_count, and returned_row_count.
type PayloadMetadata map[string]any
