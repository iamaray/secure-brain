package routes

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"secure-brain/internal/domain"
)

func routeErrorCode(t *testing.T, err error) domain.Code {
	t.Helper()
	var appErr *domain.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected domain error, got %T: %v", err, err)
	}
	return appErr.Code
}

func TestValidatePath(t *testing.T) {
	valid := []string{"/a", "/research/share", "/v1/data-set_2"}
	for _, path := range valid {
		t.Run("valid_"+strings.ReplaceAll(path, "/", "_"), func(t *testing.T) {
			if err := ValidatePath(path); err != nil {
				t.Fatalf("ValidatePath(%q): %v", path, err)
			}
		})
	}
	invalid := []string{
		"", "research", "/Research", "/a/", "/a//b", "/a/./b", "/a/../b",
		"/%2f", "/a%2Fb", "/api", "/api/private", "/q", "/q/x",
		"/healthz", "/healthz/deep", "/readyz", "/readyz/deep",
	}
	for _, path := range invalid {
		t.Run("invalid_"+strings.ReplaceAll(path, "/", "_"), func(t *testing.T) {
			if err := ValidatePath(path); routeErrorCode(t, err) != domain.CodeRouteInvalid {
				t.Fatalf("ValidatePath(%q) = %v", path, err)
			}
		})
	}
}

func validValidationFixture() (Configuration, ValidationContext) {
	source := domain.Brain{ID: "source-uuid", OwnerUserID: "user-1", CanonicalID: "brain.source"}
	dest := domain.Brain{ID: "dest-uuid", OwnerUserID: "user-2", CanonicalID: "brain.dest"}
	service := domain.Service{ID: "service-uuid", CanonicalID: "service.one"}
	return Configuration{
			Path: "/research/share", AssetIDs: []string{"asset-1"},
			Operations:      []domain.Operation{domain.OperationRawRead, domain.OperationTextSearch},
			Visibility:      domain.VisibilityPrivate,
			AllowedBrainIDs: []string{"brain.dest"}, AllowedServiceIDs: []string{"service.one"},
			Route: &RouteConfig{ServiceHops: []string{"service.one", "service.one"}, Terminal: "brain.dest"},
			State: domain.QueryPathStateEnabled,
		}, ValidationContext{
			ActorUserID: "user-1", SourceBrain: source,
			Assets:        map[string]domain.Asset{"asset-1": {ID: "asset-1", BrainID: "source-uuid", Format: domain.AssetFormatText, ProcessingState: domain.AssetStateReady}},
			Brains:        map[string]domain.Brain{"brain.source": source, "brain.dest": dest},
			Services:      map[string]domain.Service{"service.one": service},
			ExistingPaths: map[string]string{}, MaxHops: 20,
		}
}

func TestValidateConfigurationAcceptsRepeatedHops(t *testing.T) {
	cfg, ctx := validValidationFixture()
	if fields := ValidateConfiguration(cfg, ctx); len(fields) != 0 {
		t.Fatalf("unexpected validation errors: %#v", fields)
	}
}

func TestValidateConfigurationReturnsAllFieldErrorsInOrder(t *testing.T) {
	cfg, ctx := validValidationFixture()
	ctx.ActorUserID = "other-user"
	ctx.ExistingPaths[cfg.Path] = "other-path-id"
	cfg.AssetIDs = []string{"asset-1", "asset-1", "missing"}
	cfg.Operations = []domain.Operation{domain.Operation("bad"), domain.OperationCSVQuery, domain.OperationCSVQuery}
	cfg.Visibility = domain.Visibility("friends")
	cfg.AllowedBrainIDs = []string{"brain.missing", "brain.missing"}
	cfg.AllowedServiceIDs = []string{"service.missing"}
	cfg.Route = &RouteConfig{ServiceHops: []string{"service.missing", "service.one", "service.one"}, Terminal: "service.wrong"}
	ctx.MaxHops = 2
	cfg.State = domain.QueryPathState("bad")

	fields := ValidateConfiguration(cfg, ctx)
	if len(fields) < 12 {
		t.Fatalf("expected accumulated validation errors, got %#v", fields)
	}
	wantPrefix := []string{"source_brain_id", "path", "asset_ids[1]", "asset_ids[2]", "operations[0]", "operations[1]", "operations[2]", "visibility"}
	gotPrefix := make([]string, len(wantPrefix))
	for i := range gotPrefix {
		gotPrefix[i] = fields[i].Field
	}
	if !reflect.DeepEqual(gotPrefix, wantPrefix) {
		t.Fatalf("validation order = %#v, want prefix %#v; all=%#v", gotPrefix, wantPrefix, fields)
	}
}

func TestValidateConfigurationPrivatePolicyAndEnablement(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Configuration, *ValidationContext)
		field  string
		code   string
	}{
		{"missing route", func(c *Configuration, _ *ValidationContext) { c.Route = nil }, "route", "required"},
		{"missing service grant", func(c *Configuration, _ *ValidationContext) { c.AllowedServiceIDs = nil }, "allowed_service_ids", "missing_route_service"},
		{"missing destination grant", func(c *Configuration, _ *ValidationContext) { c.AllowedBrainIDs = nil }, "allowed_brain_ids", "missing_destination"},
		{"too many hops", func(c *Configuration, x *ValidationContext) { x.MaxHops = 1 }, "route.service_hops", "too_long"},
		{"unavailable asset", func(_ *Configuration, x *ValidationContext) {
			a := x.Assets["asset-1"]
			a.ProcessingState = domain.AssetStateUploading
			x.Assets["asset-1"] = a
		}, "asset_ids[0]", "unavailable"},
		{"parse failed structured path", func(_ *Configuration, x *ValidationContext) {
			a := x.Assets["asset-1"]
			a.ProcessingState = domain.AssetStateParseFailed
			x.Assets["asset-1"] = a
		}, "asset_ids[0]", "parse_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, ctx := validValidationFixture()
			tt.mutate(&cfg, &ctx)
			fields := ValidateConfiguration(cfg, ctx)
			for _, field := range fields {
				if field.Field == tt.field && field.Code == tt.code {
					return
				}
			}
			t.Fatalf("missing %s/%s in %#v", tt.field, tt.code, fields)
		})
	}
}

func TestValidateConfigurationAllowsSourceDestinationWithoutBrainGrant(t *testing.T) {
	cfg, ctx := validValidationFixture()
	cfg.Route.Terminal = "brain.source"
	cfg.AllowedBrainIDs = nil
	if fields := ValidateConfiguration(cfg, ctx); len(fields) != 0 {
		t.Fatalf("source destination should be implicit: %#v", fields)
	}
}

func TestValidateConfigurationDraftCanHaveNoRoute(t *testing.T) {
	cfg, ctx := validValidationFixture()
	cfg.State = domain.QueryPathStateDraft
	cfg.Route = nil
	a := ctx.Assets["asset-1"]
	a.ProcessingState = domain.AssetStateUploading
	ctx.Assets["asset-1"] = a
	if fields := ValidateConfiguration(cfg, ctx); len(fields) != 0 {
		t.Fatalf("draft may await its asset and route: %#v", fields)
	}
}

func TestValidateConfigurationDraftMayHaveIncompletePolicy(t *testing.T) {
	cfg, ctx := validValidationFixture()
	cfg.State = domain.QueryPathStateDraft
	cfg.AllowedBrainIDs = nil
	cfg.AllowedServiceIDs = nil
	if fields := ValidateConfiguration(cfg, ctx); len(fields) != 0 {
		t.Fatalf("draft should preserve an incomplete private policy: %#v", fields)
	}
}

func TestResolveTerminal(t *testing.T) {
	tests := []struct {
		name                   string
		mode                   domain.ExecutionMode
		terminal, source, init string
		want                   string
		code                   domain.Code
	}{
		{"pull caller", domain.ExecutionModePull, "caller", "brain.source", "brain.caller", "brain.caller", ""},
		{"pull fixed", domain.ExecutionModePull, "brain.dest", "brain.source", "brain.dest", "brain.dest", ""},
		{"pull fixed mismatch", domain.ExecutionModePull, "brain.dest", "brain.source", "brain.other", "", domain.CodeDestinationMismatch},
		{"push fixed", domain.ExecutionModePush, "brain.dest", "brain.source", "brain.source", "brain.dest", ""},
		{"push caller", domain.ExecutionModePush, "caller", "brain.source", "brain.source", "", domain.CodeRouteInvalid},
		{"push not source", domain.ExecutionModePush, "brain.dest", "brain.source", "brain.other", "", domain.CodeNotAuthorized},
		{"bad terminal", domain.ExecutionModePull, "service.one", "brain.source", "brain.source", "", domain.CodeRouteInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveTerminal(tt.mode, tt.terminal, domain.BrainID(tt.source), domain.BrainID(tt.init))
			if tt.code == "" {
				if err != nil || string(got) != tt.want {
					t.Fatalf("got (%q,%v), want (%q,nil)", got, err, tt.want)
				}
				return
			}
			if code := routeErrorCode(t, err); code != tt.code {
				t.Fatalf("code = %s, want %s", code, tt.code)
			}
		})
	}
}

func TestAuthorizeMatrix(t *testing.T) {
	base := AuthorizationInput{
		Mode: domain.ExecutionModePull, SourceBrainID: "brain.source",
		InitiatingBrainID: "brain.caller", InitiatorOwned: true, InitiatorRegistered: true,
		Terminal: "caller", BrainGrants: []domain.BrainID{"brain.caller", "brain.dest"},
		ServiceGrants: []domain.ServiceID{"service.one"}, ServiceHops: []domain.ServiceID{"service.one", "service.one"},
	}
	tests := []struct {
		name   string
		mutate func(*AuthorizationInput)
		want   string
		code   domain.Code
	}{
		{"public caller", func(i *AuthorizationInput) { i.Visibility = domain.VisibilityPublic }, "brain.caller", ""},
		{"public fixed destination", func(i *AuthorizationInput) { i.Visibility = domain.VisibilityPublic; i.Terminal = "brain.caller" }, "brain.caller", ""},
		{"public fixed mismatch", func(i *AuthorizationInput) { i.Visibility = domain.VisibilityPublic; i.Terminal = "brain.dest" }, "", domain.CodeDestinationMismatch},
		{"private caller", func(i *AuthorizationInput) { i.Visibility = domain.VisibilityPrivate }, "brain.caller", ""},
		{"private missing initiator", func(i *AuthorizationInput) { i.Visibility = domain.VisibilityPrivate; i.BrainGrants = nil }, "", domain.CodePrincipalNotAuthorized},
		{"private missing service distinct", func(i *AuthorizationInput) { i.Visibility = domain.VisibilityPrivate; i.ServiceGrants = nil }, "", domain.CodePrincipalNotAuthorized},
		{"source initiator implicit", func(i *AuthorizationInput) {
			i.Visibility = domain.VisibilityPrivate
			i.InitiatingBrainID = "brain.source"
			i.BrainGrants = nil
		}, "brain.source", ""},
		{"source destination implicit push", func(i *AuthorizationInput) {
			i.Visibility = domain.VisibilityPrivate
			i.Mode = domain.ExecutionModePush
			i.InitiatingBrainID = "brain.source"
			i.Terminal = "brain.source"
			i.BrainGrants = nil
		}, "brain.source", ""},
		{"private push destination granted", func(i *AuthorizationInput) {
			i.Visibility = domain.VisibilityPrivate
			i.Mode = domain.ExecutionModePush
			i.InitiatingBrainID = "brain.source"
			i.Terminal = "brain.dest"
		}, "brain.dest", ""},
		{"private push destination missing", func(i *AuthorizationInput) {
			i.Visibility = domain.VisibilityPrivate
			i.Mode = domain.ExecutionModePush
			i.InitiatingBrainID = "brain.source"
			i.Terminal = "brain.dest"
			i.BrainGrants = nil
		}, "", domain.CodePrincipalNotAuthorized},
		{"initiator not owned", func(i *AuthorizationInput) { i.Visibility = domain.VisibilityPublic; i.InitiatorOwned = false }, "", domain.CodeInitiatorNotOwned},
		{"initiator unregistered", func(i *AuthorizationInput) { i.Visibility = domain.VisibilityPublic; i.InitiatorRegistered = false }, "", domain.CodeNodeNotFound},
		{"route too long", func(i *AuthorizationInput) { i.Visibility = domain.VisibilityPublic; i.MaxHops = 1 }, "", domain.CodeRouteTooLong},
		{"invalid visibility", func(i *AuthorizationInput) { i.Visibility = domain.Visibility("secret") }, "", domain.CodeRouteInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := base
			in.BrainGrants = append([]domain.BrainID(nil), base.BrainGrants...)
			in.ServiceGrants = append([]domain.ServiceID(nil), base.ServiceGrants...)
			in.ServiceHops = append([]domain.ServiceID(nil), base.ServiceHops...)
			tt.mutate(&in)
			got, err := Authorize(in)
			if tt.code == "" {
				if err != nil || string(got) != tt.want {
					t.Fatalf("got (%q,%v), want (%q,nil)", got, err, tt.want)
				}
				return
			}
			if code := routeErrorCode(t, err); code != tt.code {
				t.Fatalf("code = %s, want %s", code, tt.code)
			}
		})
	}
}
