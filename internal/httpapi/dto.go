package httpapi

import (
	"time"

	"secure-brain/internal/application"
	"secure-brain/internal/assets"
	"secure-brain/internal/domain"
	"secure-brain/internal/routes"
)

type dataEnvelope struct {
	Data      any    `json:"data"`
	RequestID string `json:"request_id"`
}

type errorEnvelope struct {
	Error     errorDTO `json:"error"`
	RequestID string   `json:"request_id"`
}

type errorDTO struct {
	Code    domain.Code         `json:"code"`
	Message string              `json:"message"`
	Details domain.ErrorDetails `json:"details,omitempty"`
}

type statusDTO struct {
	Status string `json:"status"`
}

type userDTO struct {
	ID          domain.RecordID `json:"id"`
	Handle      string          `json:"handle"`
	DisplayName string          `json:"display_name"`
	CreatedAt   time.Time       `json:"created_at"`
}

func userResponse(user domain.User) userDTO {
	return userDTO{
		ID: user.ID, Handle: user.Handle, DisplayName: user.DisplayName,
		CreatedAt: user.CreatedAt,
	}
}

func userListResponse(users []domain.User) []userDTO {
	result := make([]userDTO, len(users))
	for i, user := range users {
		result[i] = userResponse(user)
	}
	return result
}

type sessionDTO struct {
	Disclosure string  `json:"disclosure"`
	MockAuth   bool    `json:"mock_auth"`
	User       userDTO `json:"user"`
}

type brainDTO struct {
	ID          domain.RecordID   `json:"id"`
	OwnerUserID domain.RecordID   `json:"owner_user_id,omitempty"`
	Slug        string            `json:"slug"`
	CanonicalID domain.BrainID    `json:"canonical_id"`
	DisplayName string            `json:"display_name"`
	Status      domain.NodeStatus `json:"status"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

func brainResponse(brain domain.Brain, includeOwner bool) brainDTO {
	var owner domain.RecordID
	if includeOwner {
		owner = brain.OwnerUserID
	}
	return brainDTO{
		ID: brain.ID, OwnerUserID: owner, Slug: brain.Slug,
		CanonicalID: brain.CanonicalID, DisplayName: brain.DisplayName,
		Status: brain.Status, CreatedAt: brain.CreatedAt, UpdatedAt: brain.UpdatedAt,
	}
}

func brainListResponse(brains []domain.Brain, includeOwner bool) []brainDTO {
	result := make([]brainDTO, len(brains))
	for i, brain := range brains {
		result[i] = brainResponse(brain, includeOwner)
	}
	return result
}

type serviceDTO struct {
	ID             domain.RecordID   `json:"id"`
	OwnerUserID    domain.RecordID   `json:"owner_user_id,omitempty"`
	Slug           string            `json:"slug"`
	CanonicalID    domain.ServiceID  `json:"canonical_id"`
	DisplayName    string            `json:"display_name"`
	Status         domain.NodeStatus `json:"status"`
	CapabilityTags []string          `json:"capability_tags"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

func serviceResponse(service domain.Service, includeOwner bool) serviceDTO {
	var owner domain.RecordID
	if includeOwner {
		owner = service.OwnerUserID
	}
	return serviceDTO{
		ID: service.ID, OwnerUserID: owner, Slug: service.Slug,
		CanonicalID: service.CanonicalID, DisplayName: service.DisplayName,
		Status: service.Status, CapabilityTags: service.CapabilityTags,
		CreatedAt: service.CreatedAt, UpdatedAt: service.UpdatedAt,
	}
}

func serviceListResponse(services []domain.Service, includeOwner bool) []serviceDTO {
	result := make([]serviceDTO, len(services))
	for i, service := range services {
		result[i] = serviceResponse(service, includeOwner)
	}
	return result
}

type assetDTO struct {
	ID               domain.RecordID             `json:"id"`
	BrainID          domain.RecordID             `json:"brain_id"`
	ObjectKey        domain.ObjectKey            `json:"object_key"`
	OriginalFilename string                      `json:"original_filename"`
	MediaType        string                      `json:"media_type"`
	ByteSize         int64                       `json:"byte_size"`
	SHA256           string                      `json:"sha256,omitempty"`
	Format           domain.AssetFormat          `json:"format"`
	ProcessingState  domain.AssetProcessingState `json:"processing_state"`
	ParseError       *string                     `json:"parse_error,omitempty"`
	CreatedAt        time.Time                   `json:"created_at"`
	UpdatedAt        time.Time                   `json:"updated_at"`
}

func assetResponse(asset domain.Asset) assetDTO {
	return assetDTO{
		ID: asset.ID, BrainID: asset.BrainID, ObjectKey: asset.ObjectKey,
		OriginalFilename: asset.OriginalFilename, MediaType: asset.MediaType,
		ByteSize: asset.ByteSize, SHA256: asset.SHA256, Format: asset.Format,
		ProcessingState: asset.ProcessingState, ParseError: asset.ParseError,
		CreatedAt: asset.CreatedAt, UpdatedAt: asset.UpdatedAt,
	}
}

func assetListResponse(items []domain.Asset) []assetDTO {
	result := make([]assetDTO, len(items))
	for i, asset := range items {
		result[i] = assetResponse(asset)
	}
	return result
}

type previewDTO struct {
	Kind      string     `json:"kind"`
	Text      string     `json:"text,omitempty"`
	Headers   []string   `json:"headers,omitempty"`
	Rows      [][]string `json:"rows,omitempty"`
	Truncated bool       `json:"truncated"`
}

type assetContentDTO struct {
	Asset   assetDTO   `json:"asset"`
	Preview previewDTO `json:"preview"`
}

func assetPreviewResponse(preview assets.Preview) previewDTO {
	return previewDTO{
		Kind: preview.Kind, Text: preview.Text, Headers: preview.Headers,
		Rows: preview.Rows, Truncated: preview.Truncated,
	}
}

type queryPathListItemDTO struct {
	ID            domain.RecordID       `json:"id"`
	BrainID       domain.RecordID       `json:"brain_id"`
	Path          domain.QueryPathValue `json:"path"`
	Visibility    domain.Visibility     `json:"visibility"`
	State         domain.QueryPathState `json:"state"`
	Operations    []domain.Operation    `json:"operations"`
	ConfigVersion int64                 `json:"config_version"`
	CreatedAt     time.Time             `json:"created_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
}

func queryPathListResponse(paths []domain.QueryPath) []queryPathListItemDTO {
	result := make([]queryPathListItemDTO, len(paths))
	for i, path := range paths {
		result[i] = queryPathListItemDTO{
			ID: path.ID, BrainID: path.BrainID, Path: path.Path,
			Visibility: path.Visibility, State: path.State, Operations: path.Operations,
			ConfigVersion: path.ConfigVersion, CreatedAt: path.CreatedAt,
			UpdatedAt: path.UpdatedAt,
		}
	}
	return result
}

type fieldErrorDTO struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type queryPathValidationDTO struct {
	Fields []fieldErrorDTO `json:"fields"`
	Valid  bool            `json:"valid"`
}

func fieldErrorListResponse(fields []routes.FieldError) []fieldErrorDTO {
	result := make([]fieldErrorDTO, len(fields))
	for i, field := range fields {
		result[i] = fieldErrorDTO{
			Field: field.Field, Code: field.Code, Message: field.Message,
		}
	}
	return result
}

type routeSnapshotDTO struct {
	AssetIDs            []assetIntegritySnapshotDTO `json:"asset_ids"`
	ConfigVersion       int64                       `json:"config_version"`
	Operation           domain.Operation            `json:"operation"`
	ResolvedDestination domain.BrainID              `json:"resolved_destination"`
	ServiceHops         []domain.ServiceID          `json:"service_hops"`
	SourceCanonicalID   domain.BrainID              `json:"source_canonical_id"`
	SourcePath          domain.QueryPathValue       `json:"source_path"`
	Terminal            string                      `json:"terminal"`
	Visibility          domain.Visibility           `json:"visibility"`
}

type assetIntegritySnapshotDTO struct {
	AssetID domain.RecordID `json:"asset_id"`
	SHA256  string          `json:"sha256"`
}

func routeSnapshotResponse(snapshot application.RouteSnapshot) routeSnapshotDTO {
	assets := make([]assetIntegritySnapshotDTO, len(snapshot.Assets))
	for i, asset := range snapshot.Assets {
		assets[i] = assetIntegritySnapshotDTO{AssetID: asset.AssetID, SHA256: asset.SHA256}
	}
	return routeSnapshotDTO{
		SourceCanonicalID: snapshot.SourceCanonicalID, SourcePath: snapshot.SourcePath,
		ConfigVersion: snapshot.ConfigVersion, Visibility: snapshot.Visibility,
		AssetIDs: assets, Operation: snapshot.Operation, ServiceHops: snapshot.ServiceHops,
		Terminal: snapshot.Terminal, ResolvedDestination: snapshot.ResolvedDestination,
	}
}

type executionResultMetadataDTO struct {
	ByteSize          *int    `json:"byte_size,omitempty"`
	MediaType         *string `json:"media_type,omitempty"`
	SHA256            *string `json:"sha256,omitempty"`
	SuggestedFilename *string `json:"suggested_filename,omitempty"`
}

func executionResultMetadataResponse(result application.ExecutionResultSnapshot) executionResultMetadataDTO {
	if result == (application.ExecutionResultSnapshot{}) {
		return executionResultMetadataDTO{}
	}
	return executionResultMetadataDTO{
		MediaType: &result.MediaType, ByteSize: &result.ByteSize,
		SuggestedFilename: &result.SuggestedFilename, SHA256: &result.SHA256,
	}
}

type executionDTO struct {
	ID                     domain.RecordID            `json:"id"`
	Mode                   domain.ExecutionMode       `json:"mode"`
	QueryPathID            *domain.RecordID           `json:"query_path_id,omitempty"`
	ActorUserID            *domain.RecordID           `json:"actor_user_id,omitempty"`
	InitiatingBrainID      *domain.RecordID           `json:"initiating_brain_id,omitempty"`
	SourceBrainID          *domain.RecordID           `json:"source_brain_id,omitempty"`
	DestinationBrainID     *domain.RecordID           `json:"destination_brain_id,omitempty"`
	SourceCanonicalID      domain.BrainID             `json:"source_canonical_id"`
	SourcePath             domain.QueryPathValue      `json:"source_path"`
	DestinationCanonicalID *domain.BrainID            `json:"destination_canonical_id,omitempty"`
	Operation              domain.Operation           `json:"operation"`
	State                  domain.ExecutionState      `json:"state"`
	RouteSnapshot          routeSnapshotDTO           `json:"route_snapshot"`
	ResultMetadata         executionResultMetadataDTO `json:"result_metadata"`
	ErrorCode              *domain.Code               `json:"error_code,omitempty"`
	ErrorMessage           *string                    `json:"error_message,omitempty"`
	CreatedAt              time.Time                  `json:"created_at"`
	StartedAt              *time.Time                 `json:"started_at,omitempty"`
	CompletedAt            *time.Time                 `json:"completed_at,omitempty"`
}

func executionResponse(snapshot application.RouteExecutionSnapshot) executionDTO {
	execution := snapshot.RouteExecution
	return executionDTO{
		ID: execution.ID, Mode: execution.Mode, QueryPathID: execution.QueryPathID,
		ActorUserID: execution.ActorUserID, InitiatingBrainID: execution.InitiatingBrainID,
		SourceBrainID: execution.SourceBrainID, DestinationBrainID: execution.DestinationBrainID,
		SourceCanonicalID: execution.SourceCanonicalID, SourcePath: execution.SourcePath,
		DestinationCanonicalID: execution.DestinationCanonicalID, Operation: execution.Operation,
		State: execution.State, RouteSnapshot: routeSnapshotResponse(snapshot.Route),
		ResultMetadata: executionResultMetadataResponse(snapshot.Result),
		ErrorCode:      execution.ErrorCode, ErrorMessage: execution.ErrorMessage,
		CreatedAt: execution.CreatedAt, StartedAt: execution.StartedAt,
		CompletedAt: execution.CompletedAt,
	}
}

type executionHopDTO struct {
	ID                 domain.RecordID  `json:"id"`
	ExecutionID        domain.RecordID  `json:"execution_id"`
	HopIndex           int              `json:"hop_index"`
	ServiceID          *domain.RecordID `json:"service_id,omitempty"`
	ServiceCanonicalID domain.ServiceID `json:"service_canonical_id"`
	Status             domain.HopStatus `json:"status"`
	InputSHA256        string           `json:"input_sha256"`
	OutputSHA256       string           `json:"output_sha256"`
	DurationMS         int              `json:"duration_ms"`
	ErrorCode          *domain.Code     `json:"error_code,omitempty"`
	CreatedAt          time.Time        `json:"created_at"`
}

func executionHopListResponse(hops []domain.ExecutionHop) []executionHopDTO {
	result := make([]executionHopDTO, len(hops))
	for i, hop := range hops {
		result[i] = executionHopDTO{
			ID: hop.ID, ExecutionID: hop.ExecutionID, HopIndex: hop.HopIndex,
			ServiceID: hop.ServiceID, ServiceCanonicalID: hop.ServiceCanonicalID,
			Status: hop.Status, InputSHA256: hop.InputSHA256,
			OutputSHA256: hop.OutputSHA256, DurationMS: hop.DurationMS,
			ErrorCode: hop.ErrorCode, CreatedAt: hop.CreatedAt,
		}
	}
	return result
}

type executionTraceDTO struct {
	Execution executionDTO      `json:"execution"`
	Hops      []executionHopDTO `json:"hops"`
}

type transferDTO struct {
	ID                     domain.RecordID       `json:"id"`
	ExecutionID            domain.RecordID       `json:"execution_id"`
	SourceBrainID          *domain.RecordID      `json:"source_brain_id,omitempty"`
	DestinationBrainID     *domain.RecordID      `json:"destination_brain_id,omitempty"`
	SourceCanonicalID      domain.BrainID        `json:"source_canonical_id"`
	DestinationCanonicalID domain.BrainID        `json:"destination_canonical_id"`
	Status                 domain.TransferStatus `json:"status"`
	SuggestedObjectKey     domain.ObjectKey      `json:"suggested_object_key"`
	SuggestedFilename      string                `json:"suggested_filename"`
	MediaType              string                `json:"media_type"`
	ByteSize               int64                 `json:"byte_size"`
	SHA256                 string                `json:"sha256"`
	AcceptedAssetID        *domain.RecordID      `json:"accepted_asset_id,omitempty"`
	CreatedAt              time.Time             `json:"created_at"`
	ExpiresAt              time.Time             `json:"expires_at"`
	ResolvedAt             *time.Time            `json:"resolved_at,omitempty"`
}

func transferResponse(transfer domain.Transfer) transferDTO {
	return transferDTO{
		ID: transfer.ID, ExecutionID: transfer.ExecutionID,
		SourceBrainID: transfer.SourceBrainID, DestinationBrainID: transfer.DestinationBrainID,
		SourceCanonicalID:      transfer.SourceCanonicalID,
		DestinationCanonicalID: transfer.DestinationCanonicalID, Status: transfer.Status,
		SuggestedObjectKey: transfer.SuggestedObjectKey,
		SuggestedFilename:  transfer.SuggestedFilename, MediaType: transfer.MediaType,
		ByteSize: transfer.ByteSize, SHA256: transfer.SHA256,
		AcceptedAssetID: transfer.AcceptedAssetID, CreatedAt: transfer.CreatedAt,
		ExpiresAt: transfer.ExpiresAt, ResolvedAt: transfer.ResolvedAt,
	}
}

func transferListResponse(items []domain.Transfer) []transferDTO {
	result := make([]transferDTO, len(items))
	for i, transfer := range items {
		result[i] = transferResponse(transfer)
	}
	return result
}

type transferPreviewDTO struct {
	DataBase64 *string `json:"data_base64,omitempty"`
	Text       *string `json:"text,omitempty"`
	Truncated  bool    `json:"truncated"`
}

type transferDetailDTO struct {
	Preview  *transferPreviewDTO `json:"preview,omitempty"`
	Transfer transferDTO         `json:"transfer"`
}

type transferResolutionDTO struct {
	Asset      *assetDTO             `json:"asset,omitempty"`
	Status     domain.TransferStatus `json:"status"`
	TransferID domain.RecordID       `json:"transfer_id"`
}

func transferResolutionResponse(result application.TransferResolutionResult) transferResolutionDTO {
	response := transferResolutionDTO{TransferID: result.TransferID, Status: result.Status}
	if result.Asset != nil {
		asset := assetResponse(*result.Asset)
		response.Asset = &asset
	}
	return response
}

type routeExecutionResultDTO struct {
	DataBase64  *string                     `json:"data_base64,omitempty"`
	Destination domain.BrainID              `json:"destination"`
	ExecutionID domain.RecordID             `json:"execution_id"`
	Outcome     string                      `json:"outcome"`
	Result      *executionResultMetadataDTO `json:"result,omitempty"`
	RouteID     domain.RecordID             `json:"route_id"`
	Source      domain.BrainID              `json:"source"`
	SourcePath  domain.QueryPathValue       `json:"source_path"`
	Text        *string                     `json:"text,omitempty"`
	Transfer    *transferDTO                `json:"transfer,omitempty"`
}

func routeExecutionResultResponse(result application.RouteExecutionResult) routeExecutionResultDTO {
	response := routeExecutionResultDTO{
		ExecutionID: result.ExecutionID, RouteID: result.RouteID, Source: result.Source,
		SourcePath: result.SourcePath, Destination: result.Destination,
		Outcome: result.Outcome, Text: result.Text, DataBase64: result.DataBase64,
	}
	if result.Result != nil {
		metadata := executionResultMetadataResponse(*result.Result)
		response.Result = &metadata
	}
	if result.Transfer != nil {
		transfer := transferResponse(*result.Transfer)
		response.Transfer = &transfer
	}
	return response
}

type auditEventDTO struct {
	ID           domain.RecordID           `json:"id"`
	EventType    string                    `json:"event_type"`
	ActorUserID  *domain.RecordID          `json:"actor_user_id,omitempty"`
	ResourceType string                    `json:"resource_type"`
	ResourceID   *domain.RecordID          `json:"resource_id,omitempty"`
	BrainID      *domain.RecordID          `json:"brain_id,omitempty"`
	ServiceID    *domain.RecordID          `json:"service_id,omitempty"`
	ExecutionID  *domain.RecordID          `json:"execution_id,omitempty"`
	Status       domain.AuditStatus        `json:"status"`
	Metadata     application.AuditMetadata `json:"metadata"`
	CreatedAt    time.Time                 `json:"created_at"`
}

func auditEventListResponse(events []domain.AuditEvent) []auditEventDTO {
	result := make([]auditEventDTO, len(events))
	for i, event := range events {
		result[i] = auditEventDTO{
			ID: event.ID, EventType: event.EventType, ActorUserID: event.ActorUserID,
			ResourceType: event.ResourceType, ResourceID: event.ResourceID,
			BrainID: event.BrainID, ServiceID: event.ServiceID,
			ExecutionID: event.ExecutionID, Status: event.Status,
			Metadata: application.AuditMetadata(event.Metadata), CreatedAt: event.CreatedAt,
		}
	}
	return result
}

type chatMessageDTO struct {
	ID        domain.RecordID `json:"id"`
	BrainID   domain.RecordID `json:"brain_id"`
	UserID    domain.RecordID `json:"user_id"`
	Role      domain.ChatRole `json:"role"`
	Content   string          `json:"content"`
	Model     *string         `json:"model,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type chatDTO struct {
	Disclosure string           `json:"disclosure"`
	Grounded   bool             `json:"grounded"`
	Messages   []chatMessageDTO `json:"messages"`
}

func chatResponse(messages []domain.ChatMessage) chatDTO {
	items := make([]chatMessageDTO, len(messages))
	for i, message := range messages {
		items[i] = chatMessageDTO{
			ID: message.ID, BrainID: message.BrainID, UserID: message.UserID,
			Role: message.Role, Content: message.Content, Model: message.Model,
			CreatedAt: message.CreatedAt,
		}
	}
	return chatDTO{
		Messages: items, Grounded: false,
		Disclosure: "This simulated Brain chat is not grounded in uploaded files.",
	}
}

type canvasNodeDTO struct {
	DisplayName string `json:"display_name"`
	NodeID      string `json:"node_id"`
	OnCanvas    bool   `json:"on_canvas"`
	Type        string `json:"type"`
}

type networkNodeDTO struct {
	DisplayName string            `json:"display_name"`
	ID          string            `json:"id"`
	OnCanvas    bool              `json:"on_canvas"`
	Owned       bool              `json:"owned"`
	Ports       []string          `json:"ports,omitempty"`
	Status      domain.NodeStatus `json:"status"`
	Type        string            `json:"type"`
}

type networkEdgeDTO struct {
	From        string `json:"from"`
	HopIndex    int    `json:"hop_index"`
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	QueryPathID string `json:"query_path_id"`
	RouteID     string `json:"route_id"`
	To          string `json:"to"`
}

type networkRouteDTO struct {
	ID            string             `json:"id"`
	Label         string             `json:"label"`
	Operations    []domain.Operation `json:"operations"`
	Path          string             `json:"path"`
	QueryPathID   string             `json:"query_path_id"`
	Sequence      []string           `json:"sequence"`
	ServiceHops   []string           `json:"service_hops"`
	SourceBrainID string             `json:"source_brain_id"`
	Terminal      string             `json:"terminal"`
	Visibility    domain.Visibility  `json:"visibility"`
}

type networkDTO struct {
	Edges  []networkEdgeDTO  `json:"edges"`
	Nodes  []networkNodeDTO  `json:"nodes"`
	Routes []networkRouteDTO `json:"routes"`
}
