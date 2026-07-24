package store

import (
	"encoding/json"
	"time"

	"secure-brain/internal/domain"
)

type Session struct {
	ID         domain.RecordID
	TokenHash  []byte
	UserID     domain.RecordID
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
	BrainID           domain.RecordID
	Path              domain.QueryPathValue
	Visibility        domain.Visibility
	State             domain.QueryPathState
	Operations        []domain.Operation
	AssetIDs          []domain.RecordID
	AllowedBrainIDs   []domain.RecordID
	AllowedServiceIDs []domain.RecordID
	Route             *RouteInput
}

type RouteInput struct {
	TerminalMode       domain.TerminalMode
	DestinationBrainID *domain.RecordID
	ServiceIDs         []domain.RecordID
}

type AssetInput struct {
	ID               domain.RecordID
	BrainID          domain.RecordID
	ObjectKey        domain.ObjectKey
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
	ID                     domain.RecordID
	Mode                   domain.ExecutionMode
	QueryPathID            *domain.RecordID
	ActorUserID            *domain.RecordID
	InitiatingBrainID      *domain.RecordID
	SourceBrainID          *domain.RecordID
	DestinationBrainID     *domain.RecordID
	SourceCanonicalID      domain.BrainID
	SourcePath             domain.QueryPathValue
	DestinationCanonicalID *domain.BrainID
	Operation              domain.Operation
	State                  domain.ExecutionState
	RouteSnapshot          json.RawMessage
	ResultMetadata         json.RawMessage
}

type ExecutionHopInput struct {
	ID                 domain.RecordID
	ExecutionID        domain.RecordID
	HopIndex           int
	ServiceID          *domain.RecordID
	ServiceCanonicalID domain.ServiceID
	Status             domain.HopStatus
	InputSHA256        string
	OutputSHA256       string
	DurationMS         int
	ErrorCode          *domain.Code
}

type TransferInput struct {
	ID                     domain.RecordID
	ExecutionID            domain.RecordID
	SourceBrainID          *domain.RecordID
	DestinationBrainID     *domain.RecordID
	SourceCanonicalID      domain.BrainID
	DestinationCanonicalID domain.BrainID
	StoragePath            string
	SuggestedObjectKey     domain.ObjectKey
	SuggestedFilename      string
	MediaType              string
	ByteSize               int64
	SHA256                 string
	ExpiresAt              time.Time
}

type IdempotencyRecord struct {
	ID             domain.RecordID
	UserID         domain.RecordID
	Scope          string
	IdempotencyKey domain.IdempotencyKey
	RequestHash    string
	ResponseStatus *int
	ResponseBody   json.RawMessage
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

type AuditEventInput struct {
	ID            domain.RecordID
	EventType     string
	ActorUserID   *domain.RecordID
	ResourceType  string
	ResourceID    *domain.RecordID
	BrainID       *domain.RecordID
	ServiceID     *domain.RecordID
	ExecutionID   *domain.RecordID
	Status        domain.AuditStatus
	Metadata      json.RawMessage
	ViewerUserIDs []domain.RecordID
}

type AuditFilter struct {
	NodeID    domain.Principal
	EventType string
	Status    domain.AuditStatus
	Before    *time.Time
	Limit     int
}

type TransferFilter struct {
	BrainID   domain.RecordID
	Direction string
	Status    domain.TransferStatus
	Before    *time.Time
	Limit     int
}

type NetworkRoute struct {
	RouteID                  domain.RecordID
	QueryPathID              domain.RecordID
	SourceBrainID            domain.RecordID
	SourceCanonicalID        domain.BrainID
	SourceDisplayName        string
	SourceOwnerUserID        domain.RecordID
	Path                     domain.QueryPathValue
	Operations               []domain.Operation
	Visibility               domain.Visibility
	State                    domain.QueryPathState
	TerminalMode             domain.TerminalMode
	DestinationBrainID       *domain.RecordID
	DestinationCanonicalID   *domain.BrainID
	DestinationDisplayName   *string
	DestinationOwnerUserID   *domain.RecordID
	AllowedBrainOwnerUserIDs []domain.RecordID
	Hops                     []NetworkHop
}

type NetworkHop struct {
	HopIndex    int
	ServiceID   domain.RecordID
	CanonicalID domain.ServiceID
	DisplayName string
	OwnerUserID domain.RecordID
}

type NetworkNode struct {
	ID          domain.Principal
	Type        string
	DisplayName string
	OwnerUserID domain.RecordID
	Status      domain.NodeStatus
}
