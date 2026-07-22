package store

import (
	"encoding/json"
	"time"

	"secure-brain/internal/domain"
)

type Session struct {
	ID         string
	TokenHash  []byte
	UserID     string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	User       domain.User
}

type QueryPathConfig struct {
	QueryPath       domain.QueryPath
	Assets          []domain.Asset
	AllowedBrains   []domain.Brain
	AllowedServices []domain.Service
	Route           *domain.Route
	Hops            []domain.RouteHop
}

type QueryPathConfigInput struct {
	BrainID           string
	Path              string
	Visibility        domain.Visibility
	State             domain.QueryPathState
	Operations        []domain.Operation
	AssetIDs          []string
	AllowedBrainIDs   []string
	AllowedServiceIDs []string
	Route             *RouteInput
}

type RouteInput struct {
	TerminalMode       domain.TerminalMode
	DestinationBrainID *string
	ServiceIDs         []string
}

type AssetInput struct {
	ID               string
	BrainID          string
	ObjectKey        string
	StoragePath      string
	OriginalFilename string
	MediaType        string
	ByteSize         int64
	SHA256           *string
	Format           domain.AssetFormat
	ProcessingState  domain.AssetProcessingState
	ParseError       *string
}

type ExecutionInput struct {
	ID                     string
	Mode                   domain.ExecutionMode
	QueryPathID            *string
	ActorUserID            *string
	InitiatingBrainID      *string
	SourceBrainID          *string
	DestinationBrainID     *string
	SourceCanonicalID      string
	SourcePath             string
	DestinationCanonicalID *string
	Operation              domain.Operation
	State                  domain.ExecutionState
	RouteSnapshot          json.RawMessage
	ResultMetadata         json.RawMessage
}

type ExecutionUpdate struct {
	State          domain.ExecutionState
	ResultMetadata json.RawMessage
	ErrorCode      *domain.Code
	ErrorMessage   *string
	StartedAt      *time.Time
	CompletedAt    *time.Time
}

type ExecutionHopInput struct {
	ID                 string
	ExecutionID        string
	HopIndex           int
	ServiceID          *string
	ServiceCanonicalID string
	Status             domain.HopStatus
	InputSHA256        string
	OutputSHA256       string
	DurationMS         int
	ErrorCode          *domain.Code
}

type TransferInput struct {
	ID                     string
	ExecutionID            string
	SourceBrainID          *string
	DestinationBrainID     *string
	SourceCanonicalID      string
	DestinationCanonicalID string
	StoragePath            string
	SuggestedObjectKey     string
	SuggestedFilename      string
	MediaType              string
	ByteSize               int64
	SHA256                 string
	ExpiresAt              time.Time
}

type IdempotencyRecord struct {
	ID             string
	UserID         string
	Scope          string
	IdempotencyKey string
	RequestHash    string
	ResponseStatus *int
	ResponseBody   json.RawMessage
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

type AuditEventInput struct {
	ID            string
	EventType     string
	ActorUserID   *string
	ResourceType  string
	ResourceID    *string
	BrainID       *string
	ServiceID     *string
	ExecutionID   *string
	Status        domain.AuditStatus
	Metadata      json.RawMessage
	ViewerUserIDs []string
}

type AuditFilter struct {
	NodeID    string
	EventType string
	Status    domain.AuditStatus
	Before    *time.Time
	Limit     int
}

type TransferFilter struct {
	BrainID   string
	Direction string
	Status    domain.TransferStatus
	Before    *time.Time
	Limit     int
}

type NetworkRoute struct {
	RouteID                  string
	QueryPathID              string
	SourceBrainID            string
	SourceCanonicalID        string
	SourceDisplayName        string
	SourceOwnerUserID        string
	Path                     string
	Operations               []domain.Operation
	Visibility               domain.Visibility
	State                    domain.QueryPathState
	TerminalMode             domain.TerminalMode
	DestinationBrainID       *string
	DestinationCanonicalID   *string
	DestinationDisplayName   *string
	DestinationOwnerUserID   *string
	AllowedBrainOwnerUserIDs []string
	Hops                     []NetworkHop
}

type NetworkHop struct {
	HopIndex    int
	ServiceID   string
	CanonicalID string
	DisplayName string
	OwnerUserID string
}

type NetworkNode struct {
	ID          string
	Type        string
	DisplayName string
	OwnerUserID string
	Status      domain.NodeStatus
}
