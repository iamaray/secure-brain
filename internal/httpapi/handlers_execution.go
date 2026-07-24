package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"secure-brain/internal/application"
	"secure-brain/internal/domain"
	"secure-brain/internal/query"
	"secure-brain/internal/routes"
	"secure-brain/internal/store"
)

type invocationRequest struct {
	InitiatingBrainID string   `json:"initiating_brain_id,omitempty"`
	Query             queryDTO `json:"query"`
}

type queryDTO struct {
	Operation string           `json:"operation"`
	Query     string           `json:"query,omitempty"`
	Select    []string         `json:"select,omitempty"`
	Filters   []queryFilterDTO `json:"filters,omitempty"`
	Limit     int              `json:"limit,omitempty"`
	Offset    int              `json:"offset,omitempty"`
}

type queryFilterDTO struct {
	Column   string `json:"column"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
}

func queryCommand(body queryDTO) application.QueryCommand {
	filters := make([]application.QueryFilter, len(body.Filters))
	for i, filter := range body.Filters {
		filters[i] = application.QueryFilter{
			Column: filter.Column, Operator: filter.Operator, Value: filter.Value,
		}
	}
	return application.QueryCommand{
		Operation: domain.Operation(body.Operation), Query: body.Query, Select: body.Select,
		Filters: filters, Limit: body.Limit, Offset: body.Offset,
	}
}

func (a *API) pullQueryPath(w http.ResponseWriter, r *http.Request) {
	var body invocationRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	sourceID, sourceErr := domain.ParseBrainID(r.PathValue("sourceBrainId"))
	path, pathErr := domain.ParseQueryPath("/" + strings.TrimPrefix(r.PathValue("queryPath"), "/"))
	if sourceErr != nil || pathErr != nil {
		writeError(w, r, domain.NewError(domain.CodePathNotFound, "The query path does not exist."))
		return
	}
	config, err := a.store.ResolveEnabledQueryPath(r.Context(), sourceID, path)
	if err != nil {
		writeError(w, r, domain.NewError(domain.CodePathNotFound, "The query path does not exist."))
		return
	}
	source, err := a.store.GetBrain(r.Context(), config.QueryPath.BrainID)
	if err != nil {
		writeError(w, r, domain.NewError(domain.CodePathNotFound, "The query path does not exist."))
		return
	}
	initiatorID, parseErr := domain.ParseBrainID(body.InitiatingBrainID)
	initiator, err := a.store.GetBrainByCanonicalID(r.Context(), initiatorID)
	if parseErr != nil || err != nil {
		if config.QueryPath.Visibility == domain.VisibilityPrivate && source.OwnerUserID != activeUser(r.Context()).ID {
			writeError(w, r, domain.NewError(domain.CodePathNotFound, "The query path does not exist."))
			return
		}
		writeError(w, r, domain.NewError(domain.CodeInitiatorNotOwned, "The initiating Brain is not owned by the active user."))
		return
	}
	result, appErr := a.executeRoute(r, domain.ExecutionModePull, source, initiator, config, queryCommand(body.Query))
	if appErr != nil {
		var typed *domain.Error
		if config.QueryPath.Visibility == domain.VisibilityPrivate && source.OwnerUserID != activeUser(r.Context()).ID && errors.As(appErr, &typed) {
			writeError(w, r, domain.NewError(domain.CodePathNotFound, "The query path does not exist."))
			return
		}
		writeError(w, r, appErr)
		return
	}
	writeData(w, r, http.StatusOK, routeExecutionResultResponse(result))
}

func (a *API) sendQueryPath(w http.ResponseWriter, r *http.Request) {
	brain, err := a.ownerBrain(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	config, err := a.store.LoadQueryPathConfig(r.Context(), recordIDAtBoundary(r.PathValue("queryPathId")))
	if err != nil || config.QueryPath.BrainID != brain.ID {
		writeError(w, r, domain.NewError(domain.CodePathNotFound, "The query path does not exist."))
		return
	}
	if config.QueryPath.State != domain.QueryPathStateEnabled {
		writeError(w, r, domain.NewError(domain.CodePathDisabled, "The query path is disabled."))
		return
	}
	key, err := requireIdempotencyKey(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var body struct {
		Query queryDTO `json:"query"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	requestBytes, _ := json.Marshal(body)
	record, replay, err := a.startIdempotency(r, "send:"+string(config.QueryPath.ID), key, requestBytes)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if replay != nil {
		writeData(w, r, *record.ResponseStatus, replay)
		return
	}
	result, appErr := a.executeRoute(r, domain.ExecutionModePush, brain, brain, config, queryCommand(body.Query))
	if appErr != nil {
		writeError(w, r, appErr)
		return
	}
	response := routeExecutionResultResponse(result)
	responseBytes, _ := json.Marshal(response)
	_, _ = a.store.CompleteIdempotencyRecord(r.Context(), record.ID, http.StatusCreated, responseBytes)
	writeData(w, r, http.StatusCreated, response)
}

func (a *API) executeRoute(r *http.Request, mode domain.ExecutionMode, source, initiator domain.Brain, config application.QueryPathSnapshot, request application.QueryCommand) (application.RouteExecutionResult, error) {
	if initiator.OwnerUserID != activeUser(r.Context()).ID {
		return application.RouteExecutionResult{}, domain.NewError(domain.CodeInitiatorNotOwned, "The active user does not own the initiating Brain.")
	}
	if config.Route == nil {
		return application.RouteExecutionResult{}, domain.NewError(domain.CodeRouteInvalid, "The enabled path has no route.")
	}
	allowedOperation := false
	for _, operation := range config.QueryPath.Operations {
		if operation == request.Operation {
			allowedOperation = true
			break
		}
	}
	if !allowedOperation {
		return application.RouteExecutionResult{}, domain.NewError(domain.CodeOperationNotAllowed, "The operation is not enabled on this query path.")
	}
	services := make([]domain.Service, 0, len(config.Route.Hops))
	serviceIDs := make([]domain.ServiceID, 0, len(config.Route.Hops))
	for _, hop := range config.Route.Hops {
		service, err := a.store.GetService(r.Context(), hop.ServiceID)
		if err != nil || service.Status != domain.NodeStatusReady {
			return application.RouteExecutionResult{}, domain.NewError(domain.CodeRouteInvalid, "A saved route Service is unavailable.")
		}
		services, serviceIDs = append(services, service), append(serviceIDs, service.CanonicalID)
	}
	terminal := string(domain.TerminalModeCaller)
	var destination domain.Brain
	if config.Route.Route.TerminalMode == domain.TerminalModeFixed {
		if config.Route.Route.DestinationBrainID == nil {
			return application.RouteExecutionResult{}, domain.NewError(domain.CodeRouteInvalid, "The route destination is invalid.")
		}
		value, err := a.store.GetBrain(r.Context(), *config.Route.Route.DestinationBrainID)
		if err != nil {
			return application.RouteExecutionResult{}, domain.NewError(domain.CodeRouteInvalid, "The route destination is unavailable.")
		}
		destination, terminal = value, string(value.CanonicalID)
	} else {
		destination = initiator
	}
	brainGrants, serviceGrants := []domain.BrainID{}, []domain.ServiceID{}
	for _, brain := range config.Policy.AllowedBrains {
		brainGrants = append(brainGrants, brain.CanonicalID)
	}
	for _, service := range config.Policy.AllowedServices {
		serviceGrants = append(serviceGrants, service.CanonicalID)
	}
	snapshot := application.RouteSnapshot{
		SourceCanonicalID: source.CanonicalID, SourcePath: config.QueryPath.Path,
		ConfigVersion: config.QueryPath.ConfigVersion, Visibility: config.QueryPath.Visibility,
		Assets: assetSnapshot(config.Assets), Operation: request.Operation,
		ServiceHops: serviceIDs, Terminal: terminal,
		ResolvedDestination: destination.CanonicalID,
	}
	now := a.now().UTC()
	actorID, initiatorID, sourceID, destinationID, queryPathID := activeUser(r.Context()).ID, initiator.ID, source.ID, destination.ID, config.QueryPath.ID
	destinationCanonical := destination.CanonicalID
	execution, err := a.store.InsertExecution(r.Context(), application.ExecutionStartCommand{ID: newUUID(), Mode: mode, QueryPathID: &queryPathID, ActorUserID: &actorID, InitiatingBrainID: &initiatorID, SourceBrainID: &sourceID, DestinationBrainID: &destinationID, SourceCanonicalID: source.CanonicalID, SourcePath: config.QueryPath.Path, DestinationCanonicalID: &destinationCanonical, Operation: request.Operation, State: domain.ExecutionStateAuthorizing, Route: snapshot})
	if err != nil {
		return application.RouteExecutionResult{}, databaseError(err)
	}
	viewers := executionViewers(source, destination, services, actorID)
	a.audit(r, "route.execution_started", "execution", execution.ID, &source.ID, nil, &execution.ID, domain.AuditStatusPending, application.AuditMetadata{"mode": mode, "operation": request.Operation}, []domain.RecordID{source.OwnerUserID})
	_, authErr := routes.Authorize(routes.AuthorizationInput{Mode: mode, Visibility: config.QueryPath.Visibility, SourceBrainID: source.CanonicalID, InitiatingBrainID: initiator.CanonicalID, InitiatorOwned: initiator.OwnerUserID == actorID, InitiatorRegistered: true, Terminal: terminal, BrainGrants: brainGrants, ServiceGrants: serviceGrants, ServiceHops: serviceIDs, MaxHops: a.maxRouteHops})
	if authErr != nil {
		code := errorCode(authErr)
		message := "Route authorization was denied."
		completed := a.now().UTC()
		_, _ = a.store.TransitionExecution(r.Context(), execution.ID, application.FailExecution(code, message, completed))
		a.audit(r, "route.authorization_denied", "execution", execution.ID, &source.ID, nil, &execution.ID, domain.AuditStatusDenied, application.AuditMetadata{"error_code": code}, []domain.RecordID{source.OwnerUserID})
		return application.RouteExecutionResult{}, authErr
	}
	_, _ = a.store.TransitionExecution(r.Context(), execution.ID, application.BeginExecutionRead(now))
	loaded := make([]query.Asset, 0, len(config.Assets))
	total := 0
	for _, asset := range config.Assets {
		if asset.ProcessingState != domain.AssetStateReady && !(asset.ProcessingState == domain.AssetStateParseFailed && request.Operation == query.OperationRawRead) {
			return application.RouteExecutionResult{}, a.failExecution(r, execution.ID, source.ID, viewers, domain.CodeAssetUnavailable, "A scoped asset is unavailable.")
		}
		body, _, getErr := a.objects.Get(r.Context(), asset.StoragePath)
		if getErr != nil {
			return application.RouteExecutionResult{}, a.failExecution(r, execution.ID, source.ID, viewers, domain.CodeAssetUnavailable, "A scoped asset could not be read.")
		}
		remaining := a.maxRoutePayloadBytes - total
		if remaining < 0 {
			remaining = 0
		}
		bytes, readErr := io.ReadAll(io.LimitReader(body, int64(remaining)+1))
		body.Close()
		if readErr != nil || len(bytes) > remaining {
			return application.RouteExecutionResult{}, a.failExecution(r, execution.ID, source.ID, viewers, domain.CodeAssetUnavailable, "A scoped asset could not be read.")
		}
		sum := sha256.Sum256(bytes)
		if asset.SHA256 != "" && hex.EncodeToString(sum[:]) != asset.SHA256 {
			return application.RouteExecutionResult{}, a.failExecution(r, execution.ID, source.ID, viewers, domain.CodeAssetUnavailable, "A scoped asset checksum did not match.")
		}
		total += len(bytes)
		if total > a.maxRoutePayloadBytes {
			return application.RouteExecutionResult{}, a.failExecution(r, execution.ID, source.ID, viewers, domain.CodePayloadTooLarge, "The routed payload exceeds the configured limit.")
		}
		loaded = append(loaded, query.Asset{Asset: asset, Bytes: bytes})
	}
	filters := make([]query.Filter, len(request.Filters))
	for i, filter := range request.Filters {
		filters[i] = query.Filter{Column: filter.Column, Operator: filter.Operator, Value: filter.Value}
	}
	queryRequest := query.Request{
		Operation: request.Operation, Query: request.Query, Select: request.Select,
		Filters: filters, Limit: request.Limit, Offset: request.Offset,
	}
	payload, err := query.Execute(loaded, queryRequest, query.Limits{MaxPayloadBytes: a.maxRoutePayloadBytes, MaxCSVRows: a.maxCSVRows})
	if err != nil {
		return application.RouteExecutionResult{}, a.failExecution(r, execution.ID, source.ID, viewers, errorCode(err), safeErrorMessage(err))
	}
	_, _ = a.store.TransitionExecution(r.Context(), execution.ID, application.BeginExecutionProcessing(now))
	output, hops, err := routes.ExecuteHops(r.Context(), routes.IdentityServiceExecutor{}, services, payload)
	for _, hop := range hops {
		serviceID, errorCodeValue := hop.ServiceID, (*domain.Code)(nil)
		if hop.ErrorCode != nil {
			value := *hop.ErrorCode
			errorCodeValue = &value
		}
		_, _ = a.store.InsertExecutionHop(r.Context(), application.ExecutionHopCommand{ID: newUUID(), ExecutionID: execution.ID, HopIndex: hop.HopIndex, ServiceID: &serviceID, ServiceCanonicalID: hop.ServiceCanonicalID, Status: hop.Status, InputSHA256: hop.InputSHA256, OutputSHA256: hop.OutputSHA256, DurationMS: hop.DurationMS, ErrorCode: errorCodeValue})
		eventType, auditStatus := "route.hop_completed", domain.AuditStatusSucceeded
		if hop.Status == domain.HopStatusFailed {
			eventType, auditStatus = "route.execution_failed", domain.AuditStatusFailed
		}
		a.audit(r, eventType, "execution", execution.ID, &source.ID, &serviceID, &execution.ID, auditStatus, application.AuditMetadata{"hop_index": hop.HopIndex, "service_id": hop.ServiceCanonicalID, "input_sha256": hop.InputSHA256, "output_sha256": hop.OutputSHA256}, viewers)
	}
	if err != nil {
		return application.RouteExecutionResult{}, a.failExecution(r, execution.ID, source.ID, viewers, domain.CodeServiceHopFailed, "A route Service failed.")
	}
	payloadSnapshot := application.SnapshotPayload(output)
	metadata := application.ExecutionResultSnapshot{
		MediaType: payloadSnapshot.MediaType, ByteSize: len(payloadSnapshot.Bytes),
		SuggestedFilename: payloadSnapshot.SuggestedFilename, SHA256: checksumBytes(payloadSnapshot.Bytes),
	}
	auditMetadata := application.AuditMetadata{"media_type": metadata.MediaType, "byte_size": metadata.ByteSize, "suggested_filename": metadata.SuggestedFilename, "sha256": metadata.SHA256}
	completed := a.now().UTC()
	if mode == domain.ExecutionModePush {
		transferID := newUUID()
		storagePath := fmt.Sprintf("transfers/%s/%s", transferID, checksumBytes(output.Bytes))
		if err := a.objects.Put(r.Context(), storagePath, output.MediaType, bytes.NewReader(output.Bytes), int64(len(output.Bytes)), false); err != nil {
			return application.RouteExecutionResult{}, a.failExecution(r, execution.ID, source.ID, viewers, domain.CodeStorageProviderError, "The transfer payload could not be stored.")
		}
		suggestedKey, _ := domain.ParseObjectKey("inbox/" + filepath.Base(output.SuggestedFilename))
		if strings.TrimSuffix(string(suggestedKey), "inbox/") == "" {
			suggestedKey, _ = domain.ParseObjectKey("inbox/transfer.json")
		}
		transfer, insertErr := a.store.InsertTransfer(r.Context(), application.TransferCreateCommand{ID: transferID, ExecutionID: execution.ID, SourceBrainID: &sourceID, DestinationBrainID: &destinationID, SourceCanonicalID: source.CanonicalID, DestinationCanonicalID: destination.CanonicalID, StoragePath: storagePath, SuggestedObjectKey: suggestedKey, SuggestedFilename: output.SuggestedFilename, MediaType: output.MediaType, ByteSize: int64(len(output.Bytes)), SHA256: checksumBytes(output.Bytes), ExpiresAt: completed.Add(a.transferTTL)})
		if insertErr != nil {
			_ = a.objects.Delete(r.Context(), []string{storagePath})
			return application.RouteExecutionResult{}, a.failExecution(r, execution.ID, source.ID, viewers, domain.CodeInvalidRequest, "The transfer could not be created.")
		}
		_, _ = a.store.TransitionExecution(r.Context(), execution.ID, application.DeliverExecution(metadata, now, completed))
		a.audit(r, "transfer.created", "transfer", transfer.ID, &source.ID, nil, &execution.ID, domain.AuditStatusPending, application.AuditMetadata{"destination": destination.CanonicalID, "byte_size": len(output.Bytes)}, viewers)
		a.audit(r, "route.execution_completed", "execution", execution.ID, &source.ID, nil, &execution.ID, domain.AuditStatusSucceeded, auditMetadata, viewers)
		return application.RouteExecutionResult{ExecutionID: execution.ID, RouteID: config.Route.Route.ID, Source: source.CanonicalID, SourcePath: config.QueryPath.Path, Destination: destination.CanonicalID, Outcome: "delivered", Transfer: &transfer}, nil
	}
	_, _ = a.store.TransitionExecution(r.Context(), execution.ID, application.DeliverExecution(metadata, now, completed))
	a.audit(r, "route.execution_completed", "execution", execution.ID, &source.ID, nil, &execution.ID, domain.AuditStatusSucceeded, auditMetadata, viewers)
	result := application.RouteExecutionResult{ExecutionID: execution.ID, RouteID: config.Route.Route.ID, Source: source.CanonicalID, SourcePath: config.QueryPath.Path, Destination: destination.CanonicalID, Outcome: "delivered", Result: &metadata}
	if utf8.Valid(output.Bytes) && (strings.HasPrefix(output.MediaType, "text/") || output.MediaType == "application/json") {
		text := string(output.Bytes)
		result.Text = &text
	} else {
		encoded := base64.StdEncoding.EncodeToString(output.Bytes)
		result.DataBase64 = &encoded
	}
	return result, nil
}

func assetSnapshot(values []domain.Asset) []application.AssetIntegritySnapshot {
	result := make([]application.AssetIntegritySnapshot, 0, len(values))
	for _, value := range values {
		result = append(result, application.AssetIntegritySnapshot{AssetID: value.ID, SHA256: value.SHA256})
	}
	return result
}
func checksumBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
func errorCode(err error) domain.Code {
	var appErr *domain.Error
	if errors.As(err, &appErr) {
		return appErr.Code
	}
	return domain.CodeInvalidRequest
}
func safeErrorMessage(err error) string {
	var appErr *domain.Error
	if errors.As(err, &appErr) {
		return appErr.Message
	}
	return "Route execution failed."
}

func (a *API) failExecution(r *http.Request, executionID, sourceBrainID domain.RecordID, viewers []domain.RecordID, code domain.Code, message string) error {
	completed := a.now().UTC()
	_, _ = a.store.TransitionExecution(r.Context(), executionID, application.FailExecution(code, message, completed))
	a.audit(r, "route.execution_failed", "execution", executionID, &sourceBrainID, nil, &executionID, domain.AuditStatusFailed, application.AuditMetadata{"error_code": code}, viewers)
	return domain.NewError(code, message)
}

func executionViewers(source, destination domain.Brain, services []domain.Service, actor domain.RecordID) []domain.RecordID {
	values := []domain.RecordID{actor, source.OwnerUserID, destination.OwnerUserID}
	for _, service := range services {
		values = append(values, service.OwnerUserID)
	}
	return uniqueRecordIDs(values)
}
func uniqueRecordIDs(values []domain.RecordID) []domain.RecordID {
	seen := map[domain.RecordID]bool{}
	result := []domain.RecordID{}
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
func (a *API) audit(r *http.Request, eventType, resourceType string, resourceID domain.RecordID, brainID, serviceID, executionID *domain.RecordID, status domain.AuditStatus, metadata application.AuditMetadata, viewers []domain.RecordID) {
	actor := activeUser(r.Context()).ID
	if metadata == nil {
		metadata = application.AuditMetadata{}
	}
	_, _ = a.store.InsertAuditEvent(r.Context(), application.AuditRecordCommand{ID: newUUID(), EventType: eventType, ActorUserID: &actor, ResourceType: resourceType, ResourceID: &resourceID, BrainID: brainID, ServiceID: serviceID, ExecutionID: executionID, Status: status, Metadata: metadata, ViewerUserIDs: uniqueRecordIDs(viewers)})
}

func (a *API) getExecution(w http.ResponseWriter, r *http.Request) {
	execution, err := a.visibleExecution(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, executionResponse(execution))
}
func (a *API) getExecutionTrace(w http.ResponseWriter, r *http.Request) {
	execution, err := a.visibleExecution(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	hops, err := a.store.ListExecutionHops(r.Context(), execution.ID)
	if err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	writeData(w, r, http.StatusOK, executionTraceDTO{
		Execution: executionResponse(execution), Hops: executionHopListResponse(hops),
	})
}
func (a *API) visibleExecution(r *http.Request) (application.RouteExecutionSnapshot, error) {
	execution, err := a.store.GetExecution(r.Context(), recordIDAtBoundary(r.PathValue("executionId")))
	if err != nil {
		return application.RouteExecutionSnapshot{}, domain.NewError(domain.CodeNodeNotFound, "The execution does not exist.")
	}
	userID := activeUser(r.Context()).ID
	if execution.State == domain.ExecutionStateFailed && execution.ErrorCode != nil && (*execution.ErrorCode == domain.CodePrincipalNotAuthorized || *execution.ErrorCode == domain.CodeDestinationMismatch) {
		if execution.SourceBrainID != nil {
			if brain, getErr := a.store.GetBrain(r.Context(), *execution.SourceBrainID); getErr == nil && brain.OwnerUserID == userID {
				return execution, nil
			}
		}
		return application.RouteExecutionSnapshot{}, domain.NewError(domain.CodeNotAuthorized, "The execution is not visible to this user.")
	}
	visible := execution.ActorUserID != nil && *execution.ActorUserID == userID
	for _, id := range []*domain.RecordID{execution.SourceBrainID, execution.DestinationBrainID, execution.InitiatingBrainID} {
		if id != nil {
			if brain, getErr := a.store.GetBrain(r.Context(), *id); getErr == nil && brain.OwnerUserID == userID {
				visible = true
			}
		}
	}
	if !visible {
		return application.RouteExecutionSnapshot{}, domain.NewError(domain.CodeNotAuthorized, "The execution is not visible to this user.")
	}
	return execution, nil
}

func requireIdempotencyKey(r *http.Request) (domain.IdempotencyKey, error) {
	key, err := domain.ParseIdempotencyKey(r.Header.Get("Idempotency-Key"))
	if err != nil {
		return "", domain.NewError(domain.CodeIdempotencyKeyRequired, "An Idempotency-Key of 8 to 200 characters is required.")
	}
	return key, nil
}
func (a *API) startIdempotency(r *http.Request, scope string, key domain.IdempotencyKey, body []byte) (application.IdempotencySnapshot, json.RawMessage, error) {
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	userID := activeUser(r.Context()).ID
	record, err := a.store.CreateIdempotencyRecord(r.Context(), userID, scope, key, hash, a.now().Add(24*time.Hour))
	if err == nil {
		return record, nil, nil
	}
	if !errors.Is(err, store.ErrIdempotencyExists) {
		return application.IdempotencySnapshot{}, nil, databaseError(err)
	}
	record, err = a.store.GetIdempotencyRecord(r.Context(), userID, scope, key)
	if err != nil {
		return application.IdempotencySnapshot{}, nil, databaseError(err)
	}
	if record.RequestHash != hash {
		return application.IdempotencySnapshot{}, nil, domain.NewError(domain.CodeIdempotencyKeyReused, "The idempotency key was already used with a different request.")
	}
	if record.ResponseStatus == nil {
		return application.IdempotencySnapshot{}, nil, domain.NewError(domain.CodeIdempotencyKeyReused, "The idempotent request is still in progress.")
	}
	var replayValue any
	if err := json.Unmarshal(record.ResponseBody, &replayValue); err != nil {
		return application.IdempotencySnapshot{}, nil, databaseError(err)
	}
	normalized, err := json.Marshal(replayValue)
	if err != nil {
		return application.IdempotencySnapshot{}, nil, databaseError(err)
	}
	return record, json.RawMessage(normalized), nil
}
