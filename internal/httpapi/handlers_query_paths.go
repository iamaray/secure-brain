package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"secure-brain/internal/domain"
	"secure-brain/internal/routes"
	"secure-brain/internal/store"
)

type queryPathRequest struct {
	Path              string                `json:"path"`
	AssetIDs          []string              `json:"asset_ids"`
	Operations        []domain.Operation    `json:"operations"`
	Visibility        domain.Visibility     `json:"visibility"`
	AllowedBrainIDs   []string              `json:"allowed_brain_ids"`
	AllowedServiceIDs []string              `json:"allowed_service_ids"`
	Route             *routes.RouteConfig   `json:"route"`
	State             domain.QueryPathState `json:"state"`
}

type queryPathPatch struct {
	Path              *string                `json:"path"`
	AssetIDs          *[]string              `json:"asset_ids"`
	Operations        *[]domain.Operation    `json:"operations"`
	Visibility        *domain.Visibility     `json:"visibility"`
	AllowedBrainIDs   *[]string              `json:"allowed_brain_ids"`
	AllowedServiceIDs *[]string              `json:"allowed_service_ids"`
	Route             *routes.RouteConfig    `json:"route"`
	State             *domain.QueryPathState `json:"state"`
}

type queryPathResponse struct {
	ID                string                `json:"id"`
	BrainID           string                `json:"brain_id"`
	Path              string                `json:"path"`
	AssetIDs          []string              `json:"asset_ids"`
	Operations        []domain.Operation    `json:"operations"`
	Visibility        domain.Visibility     `json:"visibility"`
	AllowedBrainIDs   []string              `json:"allowed_brain_ids"`
	AllowedServiceIDs []string              `json:"allowed_service_ids"`
	Route             *routes.RouteConfig   `json:"route,omitempty"`
	RouteID           *string               `json:"route_id,omitempty"`
	State             domain.QueryPathState `json:"state"`
	ConfigVersion     int64                 `json:"config_version"`
}

func (a *API) ownerBrain(r *http.Request) (domain.Brain, error) {
	brain, err := a.store.GetBrainByCanonicalID(r.Context(), r.PathValue("brainId"))
	if err != nil {
		return domain.Brain{}, domain.NewError(domain.CodeNodeNotFound, "The Brain does not exist.")
	}
	if brain.OwnerUserID != activeUser(r.Context()).ID {
		return domain.Brain{}, domain.NewError(domain.CodeNotAuthorized, "Only the Brain owner may manage its query paths.")
	}
	return brain, nil
}

func (a *API) listQueryPaths(w http.ResponseWriter, r *http.Request) {
	brain, err := a.ownerBrain(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	paths, err := a.store.ListQueryPaths(r.Context(), brain.ID, 50)
	if err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	writeData(w, r, http.StatusOK, paths)
}

func (a *API) createQueryPath(w http.ResponseWriter, r *http.Request) {
	brain, err := a.ownerBrain(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var body queryPathRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	input, fields, err := a.prepareQueryPath(r, brain, "", body)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if len(fields) != 0 {
		writeError(w, r, &domain.Error{Code: domain.CodeRouteInvalid, Message: "The query-path configuration is invalid.", Details: map[string]any{"fields": fields}})
		return
	}
	created, err := a.store.CreateQueryPath(r.Context(), input)
	if err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	response, err := a.queryPathResponse(r, created)
	if err != nil {
		writeError(w, r, err)
		return
	}
	a.audit(r, "query_path.created", "query_path", created.QueryPath.ID, &brain.ID, nil, nil, domain.AuditStatusSucceeded, map[string]any{"path": created.QueryPath.Path, "state": created.QueryPath.State, "config_version": created.QueryPath.ConfigVersion}, []string{brain.OwnerUserID})
	if created.QueryPath.State == domain.QueryPathStateEnabled {
		a.audit(r, "query_path.enabled", "query_path", created.QueryPath.ID, &brain.ID, nil, nil, domain.AuditStatusSucceeded, map[string]any{"path": created.QueryPath.Path}, []string{brain.OwnerUserID})
	}
	writeData(w, r, http.StatusCreated, response)
}

func (a *API) getQueryPath(w http.ResponseWriter, r *http.Request) {
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
	response, err := a.queryPathResponse(r, config)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, response)
}

func (a *API) patchQueryPath(w http.ResponseWriter, r *http.Request) {
	brain, err := a.ownerBrain(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	version, err := ifMatchVersion(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	current, err := a.store.LoadQueryPathConfig(r.Context(), r.PathValue("queryPathId"))
	if err != nil || current.QueryPath.BrainID != brain.ID {
		writeError(w, r, domain.NewError(domain.CodePathNotFound, "The query path does not exist."))
		return
	}
	base, err := a.requestFromConfig(r, current)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var patch queryPathPatch
	if err := decodeJSON(w, r, &patch); err != nil {
		writeError(w, r, err)
		return
	}
	mergeQueryPathPatch(&base, patch)
	input, fields, err := a.prepareQueryPath(r, brain, current.QueryPath.ID, base)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if len(fields) != 0 {
		writeError(w, r, &domain.Error{Code: domain.CodeRouteInvalid, Message: "The query-path configuration is invalid.", Details: map[string]any{"fields": fields}})
		return
	}
	updated, err := a.store.ReplaceQueryPath(r.Context(), current.QueryPath.ID, version, input)
	if errors.Is(err, store.ErrVersionConflict) {
		writeError(w, r, domain.NewError(domain.CodeConfigVersionConflict, "The query path was changed by another request."))
		return
	}
	if err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	response, err := a.queryPathResponse(r, updated)
	if err != nil {
		writeError(w, r, err)
		return
	}
	a.audit(r, "query_path.updated", "query_path", updated.QueryPath.ID, &brain.ID, nil, nil, domain.AuditStatusSucceeded, map[string]any{"path": updated.QueryPath.Path, "state": updated.QueryPath.State, "config_version": updated.QueryPath.ConfigVersion}, []string{brain.OwnerUserID})
	if current.QueryPath.State != updated.QueryPath.State && (updated.QueryPath.State == domain.QueryPathStateEnabled || updated.QueryPath.State == domain.QueryPathStateDisabled) {
		eventType := "query_path." + string(updated.QueryPath.State)
		a.audit(r, eventType, "query_path", updated.QueryPath.ID, &brain.ID, nil, nil, domain.AuditStatusSucceeded, map[string]any{"path": updated.QueryPath.Path}, []string{brain.OwnerUserID})
	}
	writeData(w, r, http.StatusOK, response)
}

func mergeQueryPathPatch(dst *queryPathRequest, patch queryPathPatch) {
	if patch.Path != nil {
		dst.Path = *patch.Path
	}
	if patch.AssetIDs != nil {
		dst.AssetIDs = *patch.AssetIDs
	}
	if patch.Operations != nil {
		dst.Operations = *patch.Operations
	}
	if patch.Visibility != nil {
		dst.Visibility = *patch.Visibility
	}
	if patch.AllowedBrainIDs != nil {
		dst.AllowedBrainIDs = *patch.AllowedBrainIDs
	}
	if patch.AllowedServiceIDs != nil {
		dst.AllowedServiceIDs = *patch.AllowedServiceIDs
	}
	if patch.Route != nil {
		dst.Route = patch.Route
	}
	if patch.State != nil {
		dst.State = *patch.State
	}
}

func (a *API) deleteQueryPath(w http.ResponseWriter, r *http.Request) {
	brain, err := a.ownerBrain(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	version, err := ifMatchVersion(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := a.store.DeleteQueryPath(r.Context(), brain.ID, r.PathValue("queryPathId"), version); errors.Is(err, store.ErrVersionConflict) {
		writeError(w, r, domain.NewError(domain.CodeConfigVersionConflict, "The query path version does not match."))
		return
	} else if err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	a.audit(r, "query_path.deleted", "query_path", r.PathValue("queryPathId"), &brain.ID, nil, nil, domain.AuditStatusSucceeded, map[string]any{}, []string{brain.OwnerUserID})
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) validateQueryPath(w http.ResponseWriter, r *http.Request) {
	brain, err := a.ownerBrain(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var body queryPathRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	_, fields, err := a.prepareQueryPath(r, brain, r.PathValue("queryPathId"), body)
	if err != nil {
		writeError(w, r, err)
		return
	}
	a.audit(r, "route.validated", "query_path", r.PathValue("queryPathId"), &brain.ID, nil, nil, domain.AuditStatusSucceeded, map[string]any{"valid": len(fields) == 0}, []string{brain.OwnerUserID})
	writeData(w, r, http.StatusOK, map[string]any{"valid": len(fields) == 0, "fields": fields})
}

func ifMatchVersion(r *http.Request) (int64, error) {
	value := r.Header.Get("If-Match")
	if value == "" {
		return 0, domain.NewError(domain.CodeConfigVersionConflict, "If-Match with the current config_version is required.")
	}
	version, err := strconv.ParseInt(value, 10, 64)
	if err != nil || version <= 0 {
		return 0, domain.NewError(domain.CodeInvalidRequest, "If-Match must be a positive config_version.")
	}
	return version, nil
}

func (a *API) prepareQueryPath(r *http.Request, brain domain.Brain, queryPathID string, body queryPathRequest) (store.QueryPathConfigInput, []routes.FieldError, error) {
	brains, err := a.store.ListBrains(r.Context(), nil)
	if err != nil {
		return store.QueryPathConfigInput{}, nil, databaseError(err)
	}
	services, err := a.store.ListServices(r.Context(), nil)
	if err != nil {
		return store.QueryPathConfigInput{}, nil, databaseError(err)
	}
	paths, err := a.store.ListQueryPaths(r.Context(), brain.ID, 200)
	if err != nil {
		return store.QueryPathConfigInput{}, nil, databaseError(err)
	}
	brainMap, serviceMap, existing := map[string]domain.Brain{}, map[string]domain.Service{}, map[string]string{}
	for _, value := range brains {
		brainMap[value.CanonicalID] = value
	}
	for _, value := range services {
		serviceMap[value.CanonicalID] = value
	}
	for _, value := range paths {
		existing[value.Path] = value.ID
	}
	assetMap := make(map[string]domain.Asset, len(body.AssetIDs))
	for _, id := range body.AssetIDs {
		if asset, err := a.store.GetAssetInBrain(r.Context(), brain.ID, id); err == nil {
			assetMap[id] = asset
		}
	}
	cfg := routes.Configuration{QueryPathID: queryPathID, Path: body.Path, AssetIDs: body.AssetIDs, Operations: body.Operations, Visibility: body.Visibility, AllowedBrainIDs: body.AllowedBrainIDs, AllowedServiceIDs: body.AllowedServiceIDs, Route: body.Route, State: body.State}
	fields := routes.ValidateConfiguration(cfg, routes.ValidationContext{ActorUserID: activeUser(r.Context()).ID, SourceBrain: brain, Assets: assetMap, Brains: brainMap, Services: serviceMap, ExistingPaths: existing, MaxHops: a.maxRouteHops})
	input := store.QueryPathConfigInput{BrainID: brain.ID, Path: body.Path, Visibility: body.Visibility, State: body.State, Operations: body.Operations, AssetIDs: body.AssetIDs}
	for _, id := range body.AllowedBrainIDs {
		if value, ok := brainMap[id]; ok {
			input.AllowedBrainIDs = append(input.AllowedBrainIDs, value.ID)
		}
	}
	for _, id := range body.AllowedServiceIDs {
		if value, ok := serviceMap[id]; ok {
			input.AllowedServiceIDs = append(input.AllowedServiceIDs, value.ID)
		}
	}
	if body.Route != nil {
		routeInput := &store.RouteInput{}
		if body.Route.Terminal == "caller" {
			routeInput.TerminalMode = domain.TerminalModeCaller
		} else if value, ok := brainMap[body.Route.Terminal]; ok {
			routeInput.TerminalMode = domain.TerminalModeFixed
			routeInput.DestinationBrainID = &value.ID
		}
		for _, id := range body.Route.ServiceHops {
			if value, ok := serviceMap[id]; ok {
				routeInput.ServiceIDs = append(routeInput.ServiceIDs, value.ID)
			}
		}
		input.Route = routeInput
	}
	return input, fields, nil
}

func (a *API) queryPathResponse(r *http.Request, config store.QueryPathConfig) (queryPathResponse, error) {
	response := queryPathResponse{ID: config.QueryPath.ID, BrainID: config.QueryPath.BrainID, Path: config.QueryPath.Path, Operations: config.QueryPath.Operations, Visibility: config.QueryPath.Visibility, State: config.QueryPath.State, ConfigVersion: config.QueryPath.ConfigVersion, AssetIDs: []string{}, AllowedBrainIDs: []string{}, AllowedServiceIDs: []string{}}
	for _, asset := range config.Assets {
		response.AssetIDs = append(response.AssetIDs, asset.ID)
	}
	for _, brain := range config.AllowedBrains {
		response.AllowedBrainIDs = append(response.AllowedBrainIDs, brain.CanonicalID)
	}
	for _, service := range config.AllowedServices {
		response.AllowedServiceIDs = append(response.AllowedServiceIDs, service.CanonicalID)
	}
	if config.Route != nil {
		route := &routes.RouteConfig{ServiceHops: []string{}, Terminal: "caller"}
		if config.Route.TerminalMode == domain.TerminalModeFixed && config.Route.DestinationBrainID != nil {
			brain, err := a.store.GetBrain(r.Context(), *config.Route.DestinationBrainID)
			if err != nil {
				return queryPathResponse{}, databaseError(err)
			}
			route.Terminal = brain.CanonicalID
		}
		for _, hop := range config.Hops {
			service, err := a.store.GetService(r.Context(), hop.ServiceID)
			if err != nil {
				return queryPathResponse{}, databaseError(err)
			}
			route.ServiceHops = append(route.ServiceHops, service.CanonicalID)
		}
		response.Route, response.RouteID = route, &config.Route.ID
	}
	return response, nil
}

func (a *API) requestFromConfig(r *http.Request, config store.QueryPathConfig) (queryPathRequest, error) {
	response, err := a.queryPathResponse(r, config)
	if err != nil {
		return queryPathRequest{}, err
	}
	return queryPathRequest{Path: response.Path, AssetIDs: response.AssetIDs, Operations: response.Operations, Visibility: response.Visibility, AllowedBrainIDs: response.AllowedBrainIDs, AllowedServiceIDs: response.AllowedServiceIDs, Route: response.Route, State: response.State}, nil
}
