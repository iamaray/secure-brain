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
	ID         domain.RecordID
	TokenHash  []byte
	UserID     domain.RecordID
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	User       domain.User
}

// QueryPathCommand is the complete replacement value for a query path.
type QueryPathCommand struct {
	BrainID           domain.RecordID
	Path              domain.QueryPathValue
	Visibility        domain.Visibility
	State             domain.QueryPathState
	Operations        []domain.Operation
	AssetIDs          []domain.RecordID
	AllowedBrainIDs   []domain.RecordID
	AllowedServiceIDs []domain.RecordID
	Route             *RouteCommand
}

// RouteCommand is the persistence-neutral route portion of QueryPathCommand.
type RouteCommand struct {
	TerminalMode       domain.TerminalMode
	DestinationBrainID *domain.RecordID
	ServiceIDs         []domain.RecordID
}

// QueryCommand is the protocol-free query requested for one route execution.
type QueryCommand struct {
	Operation domain.Operation
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
	SourceCanonicalID   domain.BrainID
	SourcePath          domain.QueryPathValue
	ConfigVersion       int64
	Visibility          domain.Visibility
	Assets              []AssetIntegritySnapshot
	Operation           domain.Operation
	ServiceHops         []domain.ServiceID
	Terminal            string
	ResolvedDestination domain.BrainID
}

// AssetIntegritySnapshot binds an execution to the exact scoped asset checksum.
type AssetIntegritySnapshot struct {
	AssetID domain.RecordID
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

// ExecutionStartCommand records the immutable inputs to a route attempt.
type ExecutionStartCommand struct {
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
	Route                  RouteSnapshot
	Result                 ExecutionResultSnapshot
}

// ExecutionTransitionCommand combines a domain-approved lifecycle transition
// with the typed result snapshot owned by the application boundary. It can be
// created only through the named transition functions below.
type ExecutionTransitionCommand struct {
	transition domain.ExecutionTransition
	result     ExecutionResultSnapshot
}

func BeginExecutionRead(startedAt time.Time) ExecutionTransitionCommand {
	return ExecutionTransitionCommand{transition: domain.BeginExecutionRead(startedAt)}
}

func BeginExecutionProcessing(startedAt time.Time) ExecutionTransitionCommand {
	return ExecutionTransitionCommand{transition: domain.BeginExecutionProcessing(startedAt)}
}

func DeliverExecution(result ExecutionResultSnapshot, startedAt, completedAt time.Time) ExecutionTransitionCommand {
	return ExecutionTransitionCommand{
		transition: domain.DeliverExecution(nil, startedAt, completedAt),
		result:     result,
	}
}

func FailExecution(code domain.Code, message string, completedAt time.Time) ExecutionTransitionCommand {
	return ExecutionTransitionCommand{transition: domain.FailExecution(code, message, completedAt)}
}

func FailExecutionWithResult(result ExecutionResultSnapshot, code domain.Code, message string, startedAt, completedAt time.Time) ExecutionTransitionCommand {
	return ExecutionTransitionCommand{
		transition: domain.FailExecutionWithResult(code, message, nil, startedAt, completedAt),
		result:     result,
	}
}

func (command ExecutionTransitionCommand) State() domain.ExecutionState {
	return command.transition.State()
}

func (command ExecutionTransitionCommand) Result() ExecutionResultSnapshot {
	return command.result
}

func (command ExecutionTransitionCommand) ErrorCode() *domain.Code {
	return command.transition.ErrorCode()
}

func (command ExecutionTransitionCommand) ErrorMessage() *string {
	return command.transition.ErrorMessage()
}

func (command ExecutionTransitionCommand) StartedAt() *time.Time {
	return command.transition.StartedAt()
}

func (command ExecutionTransitionCommand) CompletedAt() *time.Time {
	return command.transition.CompletedAt()
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

// ExecutionSnapshot groups one execution with its ordered hop history.
type ExecutionSnapshot struct {
	Execution RouteExecutionSnapshot
	Hops      []domain.ExecutionHop
}

// TransferCreateCommand records a pending routed transfer.
type TransferCreateCommand struct {
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

// TransferQuery contains the bounded filters for a transfer list.
type TransferQuery struct {
	BrainID   domain.RecordID
	Direction string
	Status    domain.TransferStatus
	Before    *time.Time
	Limit     int
}

// IdempotencySnapshot is the recorded state of one idempotent command.
type IdempotencySnapshot struct {
	ID             domain.RecordID
	UserID         domain.RecordID
	Scope          string
	IdempotencyKey domain.IdempotencyKey
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
	ID            domain.RecordID
	EventType     string
	ActorUserID   *domain.RecordID
	ResourceType  string
	ResourceID    *domain.RecordID
	BrainID       *domain.RecordID
	ServiceID     *domain.RecordID
	ExecutionID   *domain.RecordID
	Status        domain.AuditStatus
	Metadata      AuditMetadata
	ViewerUserIDs []domain.RecordID
}

// AuditQuery contains viewer-scoped audit filters.
type AuditQuery struct {
	NodeID    domain.Principal
	EventType string
	Status    domain.AuditStatus
	Before    *time.Time
	Limit     int
}

// NetworkRouteSnapshot is the route projection consumed by the network view.
type NetworkRouteSnapshot struct {
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
	Hops                     []NetworkHopSnapshot
}

// NetworkHopSnapshot is one ordered hop in a network projection.
type NetworkHopSnapshot struct {
	HopIndex    int
	ServiceID   domain.RecordID
	CanonicalID domain.ServiceID
	DisplayName string
	OwnerUserID domain.RecordID
}

// NetworkNodeSnapshot is a searchable Brain or Service projection.
type NetworkNodeSnapshot struct {
	ID          domain.Principal
	Type        string
	DisplayName string
	OwnerUserID domain.RecordID
	Status      domain.NodeStatus
}

// RouteExecutionResult is returned by pull and push workflows.
type RouteExecutionResult struct {
	ExecutionID domain.RecordID
	RouteID     domain.RecordID
	Source      domain.BrainID
	SourcePath  domain.QueryPathValue
	Destination domain.BrainID
	Outcome     string
	Result      *ExecutionResultSnapshot
	Transfer    *domain.Transfer
	Text        *string
	DataBase64  *string
}

// TransferResolutionResult is returned by accept and reject workflows.
type TransferResolutionResult struct {
	TransferID domain.RecordID
	Status     domain.TransferStatus
	Asset      *domain.Asset
}
