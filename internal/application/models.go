// Package application owns the protocol-free values exchanged by use cases and
// their adapters. These types intentionally have no JSON, HTTP, SQL, or storage
// annotations; each outer boundary translates its own representation.
package application

import (
	"time"

	"secure-brain/internal/domain"
)

// SessionSnapshot is the authenticated session state loaded for a request.
type SessionSnapshot struct {
	ID         string
	TokenHash  []byte
	UserID     string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	User       domain.User
}

// QueryPathCommand is the complete replacement value for a query path.
type QueryPathCommand struct {
	BrainID           string
	Path              string
	Visibility        domain.Visibility
	State             domain.QueryPathState
	Operations        []domain.Operation
	AssetIDs          []string
	AllowedBrainIDs   []string
	AllowedServiceIDs []string
	Route             *RouteCommand
}

// RouteCommand is the persistence-neutral route portion of QueryPathCommand.
type RouteCommand struct {
	TerminalMode       domain.TerminalMode
	DestinationBrainID *string
	ServiceIDs         []string
}

// QueryCommand is the protocol-free query requested for one route execution.
type QueryCommand struct {
	Operation string
	Query     string
	Select    []string
	Filters   []QueryFilter
	Limit     int
	Offset    int
}

// QueryFilter is one bounded CSV predicate.
type QueryFilter struct {
	Column   string
	Operator string
	Value    string
}

// QueryPathSnapshot is a resolved, point-in-time query-path configuration.
// Callers treat snapshots as immutable values and create a new command to change
// configuration.
type QueryPathSnapshot struct {
	QueryPath domain.QueryPath
	Assets    []domain.Asset
	Policy    PolicySnapshot
	Route     *ConfiguredRouteSnapshot
}

// PolicySnapshot captures the principals allowed by one query-path version.
type PolicySnapshot struct {
	AllowedBrains   []domain.Brain
	AllowedServices []domain.Service
}

// ConfiguredRouteSnapshot captures the stored route and its ordered hops.
type ConfiguredRouteSnapshot struct {
	Route domain.Route
	Hops  []domain.RouteHop
}

// RouteSnapshot captures the resolved route used by one execution.
type RouteSnapshot struct {
	SourceCanonicalID   string
	SourcePath          string
	ConfigVersion       int64
	Visibility          domain.Visibility
	Assets              []AssetIntegritySnapshot
	Operation           domain.Operation
	ServiceHops         []string
	Terminal            string
	ResolvedDestination string
}

// AssetIntegritySnapshot binds an execution to the exact scoped asset checksum.
type AssetIntegritySnapshot struct {
	AssetID string
	SHA256  string
}

// PayloadSnapshot describes routed bytes without exposing an infrastructure
// representation. Bytes are copied when a snapshot is created.
type PayloadSnapshot struct {
	Bytes             []byte
	MediaType         string
	SuggestedFilename string
	Metadata          PayloadMetadata
}

// PayloadMetadata is intentionally open-ended because each query operation has a
// different documented key set. Current keys are operation, asset_count,
// asset_ids, structured, match_count, truncated, file_count, and
// returned_row_count.
type PayloadMetadata = domain.PayloadMetadata

// SnapshotPayload makes a defensive point-in-time copy of a routed payload.
func SnapshotPayload(payload domain.Payload) PayloadSnapshot {
	bytes := append([]byte(nil), payload.Bytes...)
	metadata := make(PayloadMetadata, len(payload.Metadata))
	for key, value := range payload.Metadata {
		metadata[key] = value
	}
	return PayloadSnapshot{
		Bytes:             bytes,
		MediaType:         payload.MediaType,
		SuggestedFilename: payload.SuggestedFilename,
		Metadata:          metadata,
	}
}

// AssetWriteCommand contains the metadata required to insert or replace an asset.
type AssetWriteCommand struct {
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

// ExecutionStartCommand records the immutable inputs to a route attempt.
type ExecutionStartCommand struct {
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
	Route                  RouteSnapshot
	Result                 ExecutionResultSnapshot
}

// ExecutionTransitionCommand describes the next persisted execution state.
type ExecutionTransitionCommand struct {
	State        domain.ExecutionState
	Result       ExecutionResultSnapshot
	ErrorCode    *domain.Code
	ErrorMessage *string
	StartedAt    *time.Time
	CompletedAt  *time.Time
}

// ExecutionResultSnapshot is the bounded metadata persisted for a routed result.
type ExecutionResultSnapshot struct {
	MediaType         string
	ByteSize          int
	SuggestedFilename string
	SHA256            string
}

// RouteExecutionSnapshot is one persisted execution with typed configuration and
// result snapshots. The embedded domain value contains only lifecycle fields.
type RouteExecutionSnapshot struct {
	domain.RouteExecution
	Route  RouteSnapshot
	Result ExecutionResultSnapshot
}

// ExecutionHopCommand records one ordered Service occurrence.
type ExecutionHopCommand struct {
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

// ExecutionSnapshot groups one execution with its ordered hop history.
type ExecutionSnapshot struct {
	Execution RouteExecutionSnapshot
	Hops      []domain.ExecutionHop
}

// TransferCreateCommand records a pending routed transfer.
type TransferCreateCommand struct {
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

// TransferQuery contains the bounded filters for a transfer list.
type TransferQuery struct {
	BrainID   string
	Direction string
	Status    domain.TransferStatus
	Before    *time.Time
	Limit     int
}

// IdempotencySnapshot is the recorded state of one idempotent command.
type IdempotencySnapshot struct {
	ID             string
	UserID         string
	Scope          string
	IdempotencyKey string
	RequestHash    string
	ResponseStatus *int
	ResponseBody   []byte
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

// AuditMetadata is intentionally open-ended because event types own distinct
// metadata schemas. Values may contain only non-sensitive scalar values and
// bounded scalar slices; payload bytes, chat text, and secrets are forbidden.
type AuditMetadata = domain.AuditMetadata

// AuditRecordCommand records an event and all of its authorized viewers.
type AuditRecordCommand struct {
	ID            string
	EventType     string
	ActorUserID   *string
	ResourceType  string
	ResourceID    *string
	BrainID       *string
	ServiceID     *string
	ExecutionID   *string
	Status        domain.AuditStatus
	Metadata      AuditMetadata
	ViewerUserIDs []string
}

// AuditQuery contains viewer-scoped audit filters.
type AuditQuery struct {
	NodeID    string
	EventType string
	Status    domain.AuditStatus
	Before    *time.Time
	Limit     int
}

// NetworkRouteSnapshot is the route projection consumed by the network view.
type NetworkRouteSnapshot struct {
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
	Hops                     []NetworkHopSnapshot
}

// NetworkHopSnapshot is one ordered hop in a network projection.
type NetworkHopSnapshot struct {
	HopIndex    int
	ServiceID   string
	CanonicalID string
	DisplayName string
	OwnerUserID string
}

// NetworkNodeSnapshot is a searchable Brain or Service projection.
type NetworkNodeSnapshot struct {
	ID          string
	Type        string
	DisplayName string
	OwnerUserID string
	Status      domain.NodeStatus
}

// RouteExecutionResult is returned by pull and push workflows.
type RouteExecutionResult struct {
	ExecutionID string
	RouteID     string
	Source      string
	SourcePath  string
	Destination string
	Outcome     string
	Result      *ExecutionResultSnapshot
	Transfer    *domain.Transfer
	Text        *string
	DataBase64  *string
}

// TransferResolutionResult is returned by accept and reject workflows.
type TransferResolutionResult struct {
	TransferID string
	Status     domain.TransferStatus
	Asset      *domain.Asset
}
