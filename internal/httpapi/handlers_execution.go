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
	"unicode/utf8"

	"secure-brain/internal/domain"
	"secure-brain/internal/query"
	"secure-brain/internal/routes"
	"secure-brain/internal/store"
)

type invocationRequest struct {
	InitiatingBrainID string        `json:"initiating_brain_id,omitempty"`
	Query             query.Request `json:"query"`
}

func (a *API) pullQueryPath(w http.ResponseWriter, r *http.Request) {
	var body invocationRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	sourceID := r.PathValue("sourceBrainId")
	path := "/" + strings.TrimPrefix(r.PathValue("queryPath"), "/")
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
	initiator, err := a.store.GetBrainByCanonicalID(r.Context(), body.InitiatingBrainID)
	if err != nil {
		if config.QueryPath.Visibility == domain.VisibilityPrivate && source.OwnerUserID != activeUser(r.Context()).ID {
			writeError(w, r, domain.NewError(domain.CodePathNotFound, "The query path does not exist."))
			return
		}
		writeError(w, r, domain.NewError(domain.CodeInitiatorNotOwned, "The initiating Brain is not owned by the active user."))
		return
	}
	result, appErr := a.executeRoute(r, domain.ExecutionModePull, source, initiator, config, body.Query)
	if appErr != nil {
		var typed *domain.Error
		if config.QueryPath.Visibility == domain.VisibilityPrivate && source.OwnerUserID != activeUser(r.Context()).ID && errors.As(appErr, &typed) {
			writeError(w, r, domain.NewError(domain.CodePathNotFound, "The query path does not exist."))
			return
		}
		writeError(w, r, appErr)
		return
	}
	writeData(w, r, http.StatusOK, result)
}

func (a *API) sendQueryPath(w http.ResponseWriter, r *http.Request) {
	brain, err := a.ownerBrain(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	config, err := a.store.LoadQueryPathConfig(r.Context(), r.PathValue("queryPathId"))
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
		Query query.Request `json:"query"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	requestBytes, _ := json.Marshal(body)
	record, replay, err := a.startIdempotency(r, "send:"+config.QueryPath.ID, key, requestBytes)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if replay != nil {
		writeData(w, r, *record.ResponseStatus, replay)
		return
	}
	result, appErr := a.executeRoute(r, domain.ExecutionModePush, brain, brain, config, body.Query)
	if appErr != nil {
		writeError(w, r, appErr)
		return
	}
	responseBytes, _ := json.Marshal(result)
	_, _ = a.store.CompleteIdempotencyRecord(r.Context(), record.ID, http.StatusCreated, responseBytes)
	writeData(w, r, http.StatusCreated, result)
}

func (a *API) executeRoute(r *http.Request, mode domain.ExecutionMode, source, initiator domain.Brain, config store.QueryPathConfig, request query.Request) (map[string]any, error) {
	if initiator.OwnerUserID != activeUser(r.Context()).ID {
		return nil, domain.NewError(domain.CodeInitiatorNotOwned, "The active user does not own the initiating Brain.")
	}
	if config.Route == nil {
		return nil, domain.NewError(domain.CodeRouteInvalid, "The enabled path has no route.")
	}
	allowedOperation := false
	for _, operation := range config.QueryPath.Operations {
		if string(operation) == request.Operation {
			allowedOperation = true
			break
		}
	}
	if !allowedOperation {
		return nil, domain.NewError(domain.CodeOperationNotAllowed, "The operation is not enabled on this query path.")
	}
	services := make([]domain.Service, 0, len(config.Hops))
	serviceIDs := make([]string, 0, len(config.Hops))
	for _, hop := range config.Hops {
		service, err := a.store.GetService(r.Context(), hop.ServiceID)
		if err != nil || service.Status != domain.NodeStatusReady {
			return nil, domain.NewError(domain.CodeRouteInvalid, "A saved route Service is unavailable.")
		}
		services, serviceIDs = append(services, service), append(serviceIDs, service.CanonicalID)
	}
	terminal := "caller"
	var destination domain.Brain
	if config.Route.TerminalMode == domain.TerminalModeFixed {
		if config.Route.DestinationBrainID == nil {
			return nil, domain.NewError(domain.CodeRouteInvalid, "The route destination is invalid.")
		}
		value, err := a.store.GetBrain(r.Context(), *config.Route.DestinationBrainID)
		if err != nil {
			return nil, domain.NewError(domain.CodeRouteInvalid, "The route destination is unavailable.")
		}
		destination, terminal = value, value.CanonicalID
	} else {
		destination = initiator
	}
	brainGrants, serviceGrants := []string{}, []string{}
	for _, brain := range config.AllowedBrains {
		brainGrants = append(brainGrants, brain.CanonicalID)
	}
	for _, service := range config.AllowedServices {
		serviceGrants = append(serviceGrants, service.CanonicalID)
	}
	snapshot := map[string]any{"source_canonical_id": source.CanonicalID, "source_path": config.QueryPath.Path, "config_version": config.QueryPath.ConfigVersion, "visibility": config.QueryPath.Visibility, "asset_ids": assetSnapshot(config.Assets), "operation": request.Operation, "service_hops": serviceIDs, "terminal": terminal, "resolved_destination": destination.CanonicalID}
	snapshotBytes, _ := json.Marshal(snapshot)
	now := a.clock.Now().UTC()
	actorID, initiatorID, sourceID, destinationID, queryPathID := activeUser(r.Context()).ID, initiator.ID, source.ID, destination.ID, config.QueryPath.ID
	destinationCanonical := destination.CanonicalID
	execution, err := a.store.InsertExecution(r.Context(), store.ExecutionInput{ID: a.ids.NewUUID(), Mode: mode, QueryPathID: &queryPathID, ActorUserID: &actorID, InitiatingBrainID: &initiatorID, SourceBrainID: &sourceID, DestinationBrainID: &destinationID, SourceCanonicalID: source.CanonicalID, SourcePath: config.QueryPath.Path, DestinationCanonicalID: &destinationCanonical, Operation: domain.Operation(request.Operation), State: domain.ExecutionStateAuthorizing, RouteSnapshot: snapshotBytes, ResultMetadata: json.RawMessage(`{}`)})
	if err != nil {
		return nil, databaseError(err)
	}
	viewers := executionViewers(source, destination, services, actorID)
	a.audit(r, "route.execution_started", "execution", execution.ID, &source.ID, nil, &execution.ID, domain.AuditStatusPending, map[string]any{"mode": mode, "operation": request.Operation}, []string{source.OwnerUserID})
	_, authErr := routes.Authorize(routes.AuthorizationInput{Mode: mode, Visibility: config.QueryPath.Visibility, SourceBrainID: source.CanonicalID, InitiatingBrainID: initiator.CanonicalID, InitiatorOwned: initiator.OwnerUserID == actorID, InitiatorRegistered: true, Terminal: terminal, BrainGrants: brainGrants, ServiceGrants: serviceGrants, ServiceHops: serviceIDs, MaxHops: a.limits.MaxRouteHops})
	if authErr != nil {
		code := errorCode(authErr)
		message := "Route authorization was denied."
		completed := a.clock.Now().UTC()
		_, _ = a.store.UpdateExecutionState(r.Context(), execution.ID, store.ExecutionUpdate{State: domain.ExecutionStateFailed, ErrorCode: &code, ErrorMessage: &message, CompletedAt: &completed})
		a.audit(r, "route.authorization_denied", "execution", execution.ID, &source.ID, nil, &execution.ID, domain.AuditStatusDenied, map[string]any{"error_code": code}, []string{source.OwnerUserID})
		return nil, authErr
	}
	_, _ = a.store.UpdateExecutionState(r.Context(), execution.ID, store.ExecutionUpdate{State: domain.ExecutionStateReading, StartedAt: &now})
	loaded := make([]query.Asset, 0, len(config.Assets))
	total := 0
	for _, asset := range config.Assets {
		if asset.ProcessingState != domain.AssetStateReady && !(asset.ProcessingState == domain.AssetStateParseFailed && request.Operation == query.OperationRawRead) {
			return nil, a.failExecution(r, execution.ID, source.ID, viewers, domain.CodeAssetUnavailable, "A scoped asset is unavailable.")
		}
		body, _, getErr := a.objects.Get(r.Context(), asset.StoragePath)
		if getErr != nil {
			return nil, a.failExecution(r, execution.ID, source.ID, viewers, domain.CodeAssetUnavailable, "A scoped asset could not be read.")
		}
		remaining := int(a.limits.MaxPayloadBytes) - total
		if remaining < 0 {
			remaining = 0
		}
		bytes, readErr := io.ReadAll(io.LimitReader(body, int64(remaining)+1))
		body.Close()
		if readErr != nil || len(bytes) > remaining {
			return nil, a.failExecution(r, execution.ID, source.ID, viewers, domain.CodeAssetUnavailable, "A scoped asset could not be read.")
		}
		sum := sha256.Sum256(bytes)
		if asset.SHA256 != "" && hex.EncodeToString(sum[:]) != asset.SHA256 {
			return nil, a.failExecution(r, execution.ID, source.ID, viewers, domain.CodeAssetUnavailable, "A scoped asset checksum did not match.")
		}
		total += len(bytes)
		if int64(total) > a.limits.MaxPayloadBytes {
			return nil, a.failExecution(r, execution.ID, source.ID, viewers, domain.CodePayloadTooLarge, "The routed payload exceeds the configured limit.")
		}
		loaded = append(loaded, query.Asset{Asset: asset, Bytes: bytes})
	}
	payload, err := query.Execute(loaded, request, a.limits)
	if err != nil {
		return nil, a.failExecution(r, execution.ID, source.ID, viewers, errorCode(err), safeErrorMessage(err))
	}
	_, _ = a.store.UpdateExecutionState(r.Context(), execution.ID, store.ExecutionUpdate{State: domain.ExecutionStateProcessing, StartedAt: &now})
	output, hops, err := routes.ExecuteHops(r.Context(), routes.IdentityServiceExecutor{}, services, payload, a.limits, a.clock)
	for _, hop := range hops {
		serviceID, errorCodeValue := hop.ServiceID, (*domain.Code)(nil)
		if hop.ErrorCode != nil {
			value := *hop.ErrorCode
			errorCodeValue = &value
		}
		_, _ = a.store.InsertExecutionHop(r.Context(), store.ExecutionHopInput{ID: a.ids.NewUUID(), ExecutionID: execution.ID, HopIndex: hop.HopIndex, ServiceID: &serviceID, ServiceCanonicalID: hop.ServiceCanonicalID, Status: hop.Status, InputSHA256: hop.InputSHA256, OutputSHA256: hop.OutputSHA256, DurationMS: hop.DurationMS, ErrorCode: errorCodeValue})
		eventType, auditStatus := "route.hop_completed", domain.AuditStatusSucceeded
		if hop.Status == domain.HopStatusFailed {
			eventType, auditStatus = "route.execution_failed", domain.AuditStatusFailed
		}
		a.audit(r, eventType, "execution", execution.ID, &source.ID, &serviceID, &execution.ID, auditStatus, map[string]any{"hop_index": hop.HopIndex, "service_id": hop.ServiceCanonicalID, "input_sha256": hop.InputSHA256, "output_sha256": hop.OutputSHA256}, viewers)
	}
	if err != nil {
		return nil, a.failExecution(r, execution.ID, source.ID, viewers, domain.CodeServiceHopFailed, "A route Service failed.")
	}
	metadata := map[string]any{"media_type": output.MediaType, "byte_size": len(output.Bytes), "suggested_filename": output.SuggestedFilename, "sha256": checksumBytes(output.Bytes)}
	completed := a.clock.Now().UTC()
	metadataBytes, _ := json.Marshal(metadata)
	if mode == domain.ExecutionModePush {
		transferID := a.ids.NewUUID()
		storagePath := fmt.Sprintf("transfers/%s/%s", transferID, checksumBytes(output.Bytes))
		if err := a.objects.Put(r.Context(), storagePath, output.MediaType, bytes.NewReader(output.Bytes), int64(len(output.Bytes)), false); err != nil {
			return nil, a.failExecution(r, execution.ID, source.ID, viewers, domain.CodeStorageProviderError, "The transfer payload could not be stored.")
		}
		suggestedKey := "inbox/" + filepath.Base(output.SuggestedFilename)
		if strings.TrimSuffix(suggestedKey, "inbox/") == "" {
			suggestedKey = "inbox/transfer.json"
		}
		transfer, insertErr := a.store.InsertTransfer(r.Context(), store.TransferInput{ID: transferID, ExecutionID: execution.ID, SourceBrainID: &sourceID, DestinationBrainID: &destinationID, SourceCanonicalID: source.CanonicalID, DestinationCanonicalID: destination.CanonicalID, StoragePath: storagePath, SuggestedObjectKey: suggestedKey, SuggestedFilename: output.SuggestedFilename, MediaType: output.MediaType, ByteSize: int64(len(output.Bytes)), SHA256: checksumBytes(output.Bytes), ExpiresAt: completed.Add(a.limits.TransferTTL)})
		if insertErr != nil {
			_ = a.objects.Delete(r.Context(), []string{storagePath})
			return nil, a.failExecution(r, execution.ID, source.ID, viewers, domain.CodeInvalidRequest, "The transfer could not be created.")
		}
		_, _ = a.store.UpdateExecutionState(r.Context(), execution.ID, store.ExecutionUpdate{State: domain.ExecutionStateDelivered, ResultMetadata: metadataBytes, StartedAt: &now, CompletedAt: &completed})
		a.audit(r, "transfer.created", "transfer", transfer.ID, &source.ID, nil, &execution.ID, domain.AuditStatusPending, map[string]any{"destination": destination.CanonicalID, "byte_size": len(output.Bytes)}, viewers)
		a.audit(r, "route.execution_completed", "execution", execution.ID, &source.ID, nil, &execution.ID, domain.AuditStatusSucceeded, metadata, viewers)
		return map[string]any{"execution_id": execution.ID, "route_id": config.Route.ID, "source": source.CanonicalID, "source_path": config.QueryPath.Path, "destination": destination.CanonicalID, "outcome": "delivered", "transfer": transfer}, nil
	}
	_, _ = a.store.UpdateExecutionState(r.Context(), execution.ID, store.ExecutionUpdate{State: domain.ExecutionStateDelivered, ResultMetadata: metadataBytes, StartedAt: &now, CompletedAt: &completed})
	a.audit(r, "route.execution_completed", "execution", execution.ID, &source.ID, nil, &execution.ID, domain.AuditStatusSucceeded, metadata, viewers)
	result := map[string]any{"execution_id": execution.ID, "route_id": config.Route.ID, "source": source.CanonicalID, "source_path": config.QueryPath.Path, "destination": destination.CanonicalID, "outcome": "delivered", "result": metadata}
	if utf8.Valid(output.Bytes) && (strings.HasPrefix(output.MediaType, "text/") || output.MediaType == "application/json") {
		result["text"] = string(output.Bytes)
	} else {
		result["data_base64"] = base64.StdEncoding.EncodeToString(output.Bytes)
	}
	return result, nil
}

func assetSnapshot(values []domain.Asset) []map[string]string {
	result := make([]map[string]string, 0, len(values))
	for _, value := range values {
		result = append(result, map[string]string{"asset_id": value.ID, "sha256": value.SHA256})
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

func (a *API) failExecution(r *http.Request, executionID, sourceBrainID string, viewers []string, code domain.Code, message string) error {
	completed := a.clock.Now().UTC()
	_, _ = a.store.UpdateExecutionState(r.Context(), executionID, store.ExecutionUpdate{State: domain.ExecutionStateFailed, ErrorCode: &code, ErrorMessage: &message, CompletedAt: &completed})
	a.audit(r, "route.execution_failed", "execution", executionID, &sourceBrainID, nil, &executionID, domain.AuditStatusFailed, map[string]any{"error_code": code}, viewers)
	return domain.NewError(code, message)
}

func executionViewers(source, destination domain.Brain, services []domain.Service, actor string) []string {
	values := []string{actor, source.OwnerUserID, destination.OwnerUserID}
	for _, service := range services {
		values = append(values, service.OwnerUserID)
	}
	return uniqueStrings(values)
}
func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
func (a *API) audit(r *http.Request, eventType, resourceType, resourceID string, brainID, serviceID, executionID *string, status domain.AuditStatus, metadata map[string]any, viewers []string) {
	actor := activeUser(r.Context()).ID
	if metadata == nil {
		metadata = map[string]any{}
	}
	bytes, _ := json.Marshal(metadata)
	_, _ = a.store.InsertAuditEvent(r.Context(), store.AuditEventInput{ID: a.ids.NewUUID(), EventType: eventType, ActorUserID: &actor, ResourceType: resourceType, ResourceID: &resourceID, BrainID: brainID, ServiceID: serviceID, ExecutionID: executionID, Status: status, Metadata: bytes, ViewerUserIDs: uniqueStrings(viewers)})
}

func (a *API) getExecution(w http.ResponseWriter, r *http.Request) {
	execution, err := a.visibleExecution(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, execution)
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
	writeData(w, r, http.StatusOK, map[string]any{"execution": execution, "hops": hops})
}
func (a *API) visibleExecution(r *http.Request) (domain.RouteExecution, error) {
	execution, err := a.store.GetExecution(r.Context(), r.PathValue("executionId"))
	if err != nil {
		return domain.RouteExecution{}, domain.NewError(domain.CodeNodeNotFound, "The execution does not exist.")
	}
	userID := activeUser(r.Context()).ID
	if execution.State == domain.ExecutionStateFailed && execution.ErrorCode != nil && (*execution.ErrorCode == domain.CodePrincipalNotAuthorized || *execution.ErrorCode == domain.CodeDestinationMismatch) {
		if execution.SourceBrainID != nil {
			if brain, getErr := a.store.GetBrain(r.Context(), *execution.SourceBrainID); getErr == nil && brain.OwnerUserID == userID {
				return execution, nil
			}
		}
		return domain.RouteExecution{}, domain.NewError(domain.CodeNotAuthorized, "The execution is not visible to this user.")
	}
	visible := execution.ActorUserID != nil && *execution.ActorUserID == userID
	for _, id := range []*string{execution.SourceBrainID, execution.DestinationBrainID, execution.InitiatingBrainID} {
		if id != nil {
			if brain, getErr := a.store.GetBrain(r.Context(), *id); getErr == nil && brain.OwnerUserID == userID {
				visible = true
			}
		}
	}
	if !visible {
		return domain.RouteExecution{}, domain.NewError(domain.CodeNotAuthorized, "The execution is not visible to this user.")
	}
	return execution, nil
}

func requireIdempotencyKey(r *http.Request) (string, error) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) < 8 || len(key) > 200 {
		return "", domain.NewError(domain.CodeIdempotencyKeyRequired, "An Idempotency-Key of 8 to 200 characters is required.")
	}
	return key, nil
}
func (a *API) startIdempotency(r *http.Request, scope, key string, body []byte) (store.IdempotencyRecord, any, error) {
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	userID := activeUser(r.Context()).ID
	record, err := a.store.CreateIdempotencyRecord(r.Context(), userID, scope, key, hash, a.clock.Now().Add(a.limits.IdempotencyTTL))
	if err == nil {
		return record, nil, nil
	}
	if !errors.Is(err, store.ErrIdempotencyExists) {
		return store.IdempotencyRecord{}, nil, databaseError(err)
	}
	record, err = a.store.GetIdempotencyRecord(r.Context(), userID, scope, key)
	if err != nil {
		return store.IdempotencyRecord{}, nil, databaseError(err)
	}
	if record.RequestHash != hash {
		return store.IdempotencyRecord{}, nil, domain.NewError(domain.CodeIdempotencyKeyReused, "The idempotency key was already used with a different request.")
	}
	if record.ResponseStatus == nil {
		return store.IdempotencyRecord{}, nil, domain.NewError(domain.CodeIdempotencyKeyReused, "The idempotent request is still in progress.")
	}
	var replay any
	if err := json.Unmarshal(record.ResponseBody, &replay); err != nil {
		return store.IdempotencyRecord{}, nil, databaseError(err)
	}
	return record, replay, nil
}
