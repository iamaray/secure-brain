package routes

import (
	"fmt"
	"strings"

	"secure-brain/internal/application"
	"secure-brain/internal/domain"
)

type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// RouteConfig is the owner-facing representation. Terminal is either caller
// or one registered Brain canonical ID.
type RouteConfig struct {
	ServiceHops []string `json:"service_hops"`
	Terminal    string   `json:"terminal"`
}

type Configuration struct {
	QueryPathID       string
	Path              string
	AssetIDs          []string
	Operations        []domain.Operation
	Visibility        domain.Visibility
	AllowedBrainIDs   []string
	AllowedServiceIDs []string
	Route             *RouteConfig
	State             domain.QueryPathState
}

// ValidationContext contains resolved current state and makes validation pure.
// ExistingPaths maps a normalized path to its query-path ID.
type ValidationContext struct {
	ActorUserID   domain.RecordID
	SourceBrain   domain.Brain
	Assets        map[string]domain.Asset
	Brains        map[string]domain.Brain
	Services      map[string]domain.Service
	ExistingPaths map[string]string
	MaxHops       int
}

// ValidatePath applies all normalized, reserved-prefix, and traversal rules.
func ValidatePath(path string) error {
	if _, err := domain.ParseQueryPath(path); err != nil {
		message := err.Error()
		return domain.NewError(domain.CodeRouteInvalid, strings.ToUpper(message[:1])+message[1:]+".")
	}
	return nil
}

// ValidateConfiguration returns every owner-actionable field error in stable
// validation order. It performs no mutation.
func ValidateConfiguration(cfg Configuration, ctx ValidationContext) []FieldError {
	var fields []FieldError
	add := func(field, code, message string) {
		fields = append(fields, FieldError{Field: field, Code: code, Message: message})
	}

	if ctx.SourceBrain.ID == "" {
		add("source_brain_id", "not_found", "Source Brain does not exist.")
	} else if ctx.SourceBrain.OwnerUserID != ctx.ActorUserID {
		add("source_brain_id", "not_owned", "The active user does not own the source Brain.")
	}
	if err := ValidatePath(cfg.Path); err != nil {
		add("path", "invalid", err.Error())
	} else if existingID, exists := ctx.ExistingPaths[cfg.Path]; exists && existingID != cfg.QueryPathID {
		add("path", "not_unique", "The query path already exists in this Brain.")
	}

	seenAssets := make(map[string]struct{}, len(cfg.AssetIDs))
	if len(cfg.AssetIDs) == 0 {
		add("asset_ids", "required", "At least one scoped asset is required.")
	}
	for i, id := range cfg.AssetIDs {
		field := fmt.Sprintf("asset_ids[%d]", i)
		if _, duplicate := seenAssets[id]; duplicate {
			add(field, "duplicate", "Scoped assets must be distinct.")
			continue
		}
		seenAssets[id] = struct{}{}
		asset, exists := ctx.Assets[id]
		if !exists {
			add(field, "not_found", "Scoped asset does not exist.")
			continue
		}
		if ctx.SourceBrain.ID != "" && asset.BrainID != ctx.SourceBrain.ID {
			add(field, "wrong_brain", "Scoped asset does not belong to the source Brain.")
		}
	}

	seenOperations := make(map[domain.Operation]struct{}, len(cfg.Operations))
	if len(cfg.Operations) == 0 {
		add("operations", "required", "At least one operation is required.")
	}
	for i, operation := range cfg.Operations {
		field := fmt.Sprintf("operations[%d]", i)
		if _, duplicate := seenOperations[operation]; duplicate {
			add(field, "duplicate", "Operations must be distinct.")
			continue
		}
		seenOperations[operation] = struct{}{}
		if !operation.Valid() {
			add(field, "unknown", "Unknown query operation.")
			continue
		}
		compatible := false
		for _, id := range cfg.AssetIDs {
			if asset, ok := ctx.Assets[id]; ok && operationCompatible(operation, asset) {
				compatible = true
				break
			}
		}
		if !compatible {
			add(field, "incompatible", "Operation is incompatible with every scoped asset.")
		}
	}

	if !cfg.Visibility.Valid() {
		add("visibility", "invalid", "Visibility must be public or private.")
	}
	brainGrants := validateBrainGrants(cfg.AllowedBrainIDs, ctx.Brains, add)
	serviceGrants := validateServiceGrants(cfg.AllowedServiceIDs, ctx.Services, add)

	if !cfg.State.Valid() {
		add("state", "invalid", "State must be draft, enabled, or disabled.")
	}
	if cfg.State == domain.QueryPathStateEnabled && cfg.Route == nil {
		add("route", "required", "An enabled query path requires a route.")
	}
	if cfg.Route != nil {
		maxHops := ctx.MaxHops
		if maxHops <= 0 || maxHops > application.MaxRouteHops {
			maxHops = application.MaxRouteHops
		}
		if len(cfg.Route.ServiceHops) > maxHops {
			add("route.service_hops", "too_long", fmt.Sprintf("Route cannot contain more than %d Service hops.", maxHops))
		}
		distinctHops := make(map[string]struct{})
		for i, id := range cfg.Route.ServiceHops {
			field := fmt.Sprintf("route.service_hops[%d]", i)
			if _, exists := ctx.Services[id]; !exists {
				add(field, "not_found", "Route Service does not exist.")
			}
			distinctHops[id] = struct{}{}
		}
		terminalValid := false
		if cfg.Route.Terminal == string(domain.TerminalModeCaller) {
			terminalValid = true
		} else if _, exists := ctx.Brains[cfg.Route.Terminal]; exists && strings.HasPrefix(cfg.Route.Terminal, "brain.") {
			terminalValid = true
		} else {
			add("route.terminal", "invalid", "Terminal must be caller or an existing Brain canonical ID.")
		}

		if cfg.Visibility == domain.VisibilityPrivate && cfg.State == domain.QueryPathStateEnabled {
			for serviceID := range distinctHops {
				if _, granted := serviceGrants[serviceID]; !granted {
					add("allowed_service_ids", "missing_route_service", "Every distinct route Service must be granted on a private path: "+serviceID)
				}
			}
			if terminalValid && cfg.Route.Terminal != string(domain.TerminalModeCaller) && cfg.Route.Terminal != string(ctx.SourceBrain.CanonicalID) {
				if _, granted := brainGrants[cfg.Route.Terminal]; !granted {
					add("allowed_brain_ids", "missing_destination", "The fixed destination must be granted on a private path.")
				}
			}
		}
	}

	if cfg.State == domain.QueryPathStateEnabled {
		for i, id := range cfg.AssetIDs {
			asset, exists := ctx.Assets[id]
			if !exists {
				continue
			}
			switch asset.ProcessingState {
			case domain.AssetStateReady:
			case domain.AssetStateParseFailed:
				if len(cfg.Operations) != 1 || cfg.Operations[0] != domain.OperationRawRead {
					add(fmt.Sprintf("asset_ids[%d]", i), "parse_failed", "A parse-failed asset can be enabled only on a raw-read-only path.")
				}
			default:
				add(fmt.Sprintf("asset_ids[%d]", i), "unavailable", "Enabled paths cannot use an unavailable asset.")
			}
		}
	}
	return fields
}

func validateBrainGrants(ids []string, brains map[string]domain.Brain, add func(string, string, string)) map[string]struct{} {
	grants := make(map[string]struct{}, len(ids))
	for i, id := range ids {
		field := fmt.Sprintf("allowed_brain_ids[%d]", i)
		if _, duplicate := grants[id]; duplicate {
			add(field, "duplicate", "Brain grants must be distinct.")
			continue
		}
		grants[id] = struct{}{}
		if _, exists := brains[id]; !exists || !strings.HasPrefix(id, "brain.") {
			add(field, "not_found", "Brain grant does not resolve to a registered Brain.")
		}
	}
	return grants
}

func validateServiceGrants(ids []string, services map[string]domain.Service, add func(string, string, string)) map[string]struct{} {
	grants := make(map[string]struct{}, len(ids))
	for i, id := range ids {
		field := fmt.Sprintf("allowed_service_ids[%d]", i)
		if _, duplicate := grants[id]; duplicate {
			add(field, "duplicate", "Service grants must be distinct.")
			continue
		}
		grants[id] = struct{}{}
		if _, exists := services[id]; !exists || !strings.HasPrefix(id, "service.") {
			add(field, "not_found", "Service grant does not resolve to a registered Service.")
		}
	}
	return grants
}

func operationCompatible(operation domain.Operation, asset domain.Asset) bool {
	if operation == domain.OperationRawRead {
		// Raw compatibility is a format property; availability is a separate
		// enable-time rule below. This lets an owner prepare a draft while an
		// asset is still uploading without making an unavailable path invocable.
		return true
	}
	if asset.ProcessingState == domain.AssetStateParseFailed {
		return false
	}
	if operation == domain.OperationTextSearch {
		return asset.Format == domain.AssetFormatText || asset.Format == domain.AssetFormatMarkdown
	}
	return operation == domain.OperationCSVQuery && asset.Format == domain.AssetFormatCSV
}

type AuthorizationInput struct {
	Mode                domain.ExecutionMode
	Visibility          domain.Visibility
	SourceBrainID       domain.BrainID
	InitiatingBrainID   domain.BrainID
	InitiatorOwned      bool
	InitiatorRegistered bool
	Terminal            string
	BrainGrants         []domain.BrainID
	ServiceGrants       []domain.ServiceID
	ServiceHops         []domain.ServiceID
	MaxHops             int
}

// ResolveTerminal binds caller at execution time and enforces fixed-terminal
// pull and push rules without accepting a caller-supplied destination.
func ResolveTerminal(mode domain.ExecutionMode, terminal string, sourceBrainID, initiatingBrainID domain.BrainID) (domain.BrainID, error) {
	if mode != domain.ExecutionModePull && mode != domain.ExecutionModePush {
		return "", domain.NewError(domain.CodeRouteInvalid, "Unknown route execution mode.")
	}
	if terminal == string(domain.TerminalModeCaller) {
		if mode == domain.ExecutionModePush {
			return "", domain.NewError(domain.CodeRouteInvalid, "Push requires a fixed Brain terminal.")
		}
		if initiatingBrainID == "" {
			return "", domain.NewError(domain.CodeRouteInvalid, "Caller terminal requires an initiating Brain.")
		}
		return initiatingBrainID, nil
	}
	fixedID, parseErr := domain.ParseBrainID(terminal)
	if parseErr != nil {
		return "", domain.NewError(domain.CodeRouteInvalid, "Route terminal is invalid.")
	}
	if mode == domain.ExecutionModePull && initiatingBrainID != fixedID {
		return "", domain.NewError(domain.CodeDestinationMismatch, "The initiating Brain does not match the fixed route destination.")
	}
	if mode == domain.ExecutionModePush && initiatingBrainID != sourceBrainID {
		return "", domain.NewError(domain.CodeNotAuthorized, "Only the source Brain may initiate a push.")
	}
	return fixedID, nil
}

// Authorize is the single reusable public/private policy function for pull and
// push. Callers should validate current node existence separately.
func Authorize(in AuthorizationInput) (domain.BrainID, error) {
	if !in.InitiatorOwned {
		return "", domain.NewError(domain.CodeInitiatorNotOwned, "The active user does not own the initiating Brain.")
	}
	if !in.InitiatorRegistered {
		return "", domain.NewError(domain.CodeNodeNotFound, "The initiating Brain is not registered.")
	}
	maxHops := in.MaxHops
	if maxHops <= 0 || maxHops > application.MaxRouteHops {
		maxHops = application.MaxRouteHops
	}
	if len(in.ServiceHops) > maxHops {
		return "", domain.NewError(domain.CodeRouteTooLong, "The saved route exceeds the configured Service-hop limit.")
	}
	destination, err := ResolveTerminal(in.Mode, in.Terminal, in.SourceBrainID, in.InitiatingBrainID)
	if err != nil {
		return "", err
	}
	if in.Visibility == domain.VisibilityPublic {
		return destination, nil
	}
	if in.Visibility != domain.VisibilityPrivate {
		return "", domain.NewError(domain.CodeRouteInvalid, "Query path visibility is invalid.")
	}
	brainGrants := valueSet(in.BrainGrants)
	if in.InitiatingBrainID != in.SourceBrainID {
		if _, allowed := brainGrants[in.InitiatingBrainID]; !allowed {
			return "", domain.NewError(domain.CodePrincipalNotAuthorized, "The initiating Brain is not authorized for this path.")
		}
	}
	if destination != in.SourceBrainID {
		if _, allowed := brainGrants[destination]; !allowed {
			return "", domain.NewError(domain.CodePrincipalNotAuthorized, "The destination Brain is not authorized for this path.")
		}
	}
	serviceGrants := valueSet(in.ServiceGrants)
	checked := make(map[domain.ServiceID]struct{}, len(in.ServiceHops))
	for _, serviceID := range in.ServiceHops {
		if _, seen := checked[serviceID]; seen {
			continue
		}
		checked[serviceID] = struct{}{}
		if _, allowed := serviceGrants[serviceID]; !allowed {
			return "", domain.NewError(domain.CodePrincipalNotAuthorized, "A route Service is not authorized for this path.")
		}
	}
	return destination, nil
}

func valueSet[T comparable](values []T) map[T]struct{} {
	set := make(map[T]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}
