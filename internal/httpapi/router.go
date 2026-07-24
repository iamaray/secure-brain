package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"secure-brain/internal/application"
	"secure-brain/internal/domain"
	openaiapi "secure-brain/internal/openai"
	"secure-brain/internal/storage"
	"secure-brain/internal/store"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type API struct {
	store                *store.Store
	sessionSecret        []byte
	frontendOrigin       string
	logger               *slog.Logger
	now                  func() time.Time
	objects              storage.ObjectStore
	maxFileBytes         int64
	maxPreviewBytes      int64
	maxCSVRows           int
	maxRoutePayloadBytes int
	maxRouteHops         int
	transferTTL          time.Duration
	chat                 openaiapi.ChatClient
	chatModel            string
	chatHistoryMessages  int
	chatMaxOutputTokens  int
	chatDisabled         bool
	appConnections       *appConnectionState
	networkCanvas        *networkCanvasState
}

type Options struct {
	SessionSecret        []byte
	FrontendOrigin       string
	Logger               *slog.Logger
	MaxFileBytes         int64
	MaxPreviewBytes      int64
	MaxCSVRows           int
	MaxRoutePayloadBytes int
	MaxRouteHops         int
	TransferTTL          time.Duration
	Chat                 openaiapi.ChatClient
	ChatModel            string
	ChatHistoryMessages  int
	ChatMaxOutputTokens  int
	ChatDisabled         bool
}

func New(store *store.Store, objects storage.ObjectStore, options Options) http.Handler {
	api := &API{store: store, objects: objects, sessionSecret: options.SessionSecret, frontendOrigin: options.FrontendOrigin, logger: options.Logger, now: time.Now, maxFileBytes: options.MaxFileBytes, maxPreviewBytes: options.MaxPreviewBytes, maxCSVRows: options.MaxCSVRows, maxRoutePayloadBytes: options.MaxRoutePayloadBytes, maxRouteHops: options.MaxRouteHops, transferTTL: options.TransferTTL, chat: options.Chat, chatModel: options.ChatModel, chatHistoryMessages: options.ChatHistoryMessages, chatMaxOutputTokens: options.ChatMaxOutputTokens, chatDisabled: options.ChatDisabled, appConnections: newAppConnectionState(), networkCanvas: newNetworkCanvasState()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", api.health)
	mux.HandleFunc("GET /readyz", api.ready)
	mux.HandleFunc("GET /api/users", api.listUsers)
	mux.HandleFunc("POST /api/session", api.createSession)
	mux.Handle("GET /api/session", api.requireSession(http.HandlerFunc(api.getSession)))
	mux.Handle("DELETE /api/session", api.requireSession(http.HandlerFunc(api.deleteSession)))
	mux.Handle("GET /api/brains", api.requireSession(http.HandlerFunc(api.listBrains)))
	mux.Handle("POST /api/brains", api.requireSession(http.HandlerFunc(api.createBrain)))
	mux.Handle("GET /api/brains/{brainId}", api.requireSession(http.HandlerFunc(api.getBrain)))
	mux.Handle("DELETE /api/brains/{brainId}", api.requireSession(http.HandlerFunc(api.deleteBrain)))
	mux.Handle("GET /api/brains/{brainId}/app-connections", api.requireSession(http.HandlerFunc(api.listAppConnections)))
	mux.Handle("POST /api/brains/{brainId}/app-connections", api.requireSession(http.HandlerFunc(api.createAppConnection)))
	mux.Handle("DELETE /api/brains/{brainId}/app-connections", api.requireSession(http.HandlerFunc(api.deleteAppConnection)))
	mux.Handle("DELETE /api/brains/{brainId}/app-connections/{serviceId}", api.requireSession(http.HandlerFunc(api.deleteAppConnection)))
	mux.Handle("GET /api/services", api.requireSession(http.HandlerFunc(api.listServices)))
	mux.Handle("POST /api/services", api.requireSession(http.HandlerFunc(api.createService)))
	mux.Handle("GET /api/services/{serviceId}", api.requireSession(http.HandlerFunc(api.getService)))
	mux.Handle("DELETE /api/services/{serviceId}", api.requireSession(http.HandlerFunc(api.deleteService)))
	mux.Handle("GET /api/brains/{brainId}/query-paths", api.requireSession(http.HandlerFunc(api.listQueryPaths)))
	mux.Handle("POST /api/brains/{brainId}/query-paths", api.requireSession(http.HandlerFunc(api.createQueryPath)))
	mux.Handle("GET /api/brains/{brainId}/query-paths/{queryPathId}", api.requireSession(http.HandlerFunc(api.getQueryPath)))
	mux.Handle("PATCH /api/brains/{brainId}/query-paths/{queryPathId}", api.requireSession(http.HandlerFunc(api.patchQueryPath)))
	mux.Handle("DELETE /api/brains/{brainId}/query-paths/{queryPathId}", api.requireSession(http.HandlerFunc(api.deleteQueryPath)))
	mux.Handle("POST /api/brains/{brainId}/query-paths/{queryPathId}/validate", api.requireSession(http.HandlerFunc(api.validateQueryPath)))
	mux.Handle("GET /api/brains/{brainId}/assets", api.requireSession(http.HandlerFunc(api.listAssets)))
	mux.Handle("POST /api/brains/{brainId}/assets", api.requireSession(http.HandlerFunc(api.uploadAsset)))
	mux.Handle("GET /api/brains/{brainId}/assets/{assetId}", api.requireSession(http.HandlerFunc(api.getAsset)))
	mux.Handle("GET /api/brains/{brainId}/assets/{assetId}/content", api.requireSession(http.HandlerFunc(api.assetContent)))
	mux.Handle("DELETE /api/brains/{brainId}/assets/{assetId}", api.requireSession(http.HandlerFunc(api.deleteAsset)))
	mux.Handle("POST /q/{sourceBrainId}/{queryPath...}", api.requireSession(http.HandlerFunc(api.pullQueryPath)))
	mux.Handle("POST /api/brains/{brainId}/query-paths/{queryPathId}/send", api.requireSession(http.HandlerFunc(api.sendQueryPath)))
	mux.Handle("GET /api/executions/{executionId}", api.requireSession(http.HandlerFunc(api.getExecution)))
	mux.Handle("GET /api/executions/{executionId}/trace", api.requireSession(http.HandlerFunc(api.getExecutionTrace)))
	mux.Handle("GET /api/brains/{brainId}/transfers", api.requireSession(http.HandlerFunc(api.listTransfers)))
	mux.Handle("GET /api/transfers/{transferId}", api.requireSession(http.HandlerFunc(api.getTransfer)))
	mux.Handle("POST /api/transfers/{transferId}/accept", api.requireSession(http.HandlerFunc(api.acceptTransfer)))
	mux.Handle("POST /api/transfers/{transferId}/reject", api.requireSession(http.HandlerFunc(api.rejectTransfer)))
	mux.Handle("GET /api/network", api.requireSession(http.HandlerFunc(api.getNetwork)))
	mux.Handle("GET /api/network/search", api.requireSession(http.HandlerFunc(api.searchNetwork)))
	mux.Handle("POST /api/network/nodes", api.requireSession(http.HandlerFunc(api.addNetworkNode)))
	mux.Handle("DELETE /api/network/nodes/{nodeId}", api.requireSession(http.HandlerFunc(api.removeNetworkNode)))
	mux.Handle("GET /api/audit-events", api.requireSession(http.HandlerFunc(api.listAuditEvents)))
	mux.Handle("GET /api/brains/{brainId}/chat", api.requireSession(http.HandlerFunc(api.getChat)))
	mux.Handle("POST /api/brains/{brainId}/chat", api.requireSession(http.HandlerFunc(api.postChat)))
	mux.Handle("DELETE /api/brains/{brainId}/chat", api.requireSession(http.HandlerFunc(api.clearChat)))
	return withMiddleware(mux, options.Logger, options.FrontendOrigin)
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	writeData(w, r, http.StatusOK, statusDTO{Status: "ok"})
}

func (a *API) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.store.Ping(ctx); err != nil {
		writeData(w, r, http.StatusServiceUnavailable, statusDTO{Status: "unavailable"})
		return
	}
	writeData(w, r, http.StatusOK, statusDTO{Status: "ready"})
}

func (a *API) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			writeError(w, r, domain.NewError(domain.CodeNotAuthenticated, "A mock session is required."))
			return
		}
		hash, err := verifySessionToken(cookie.Value, a.sessionSecret)
		if err != nil {
			writeError(w, r, domain.NewError(domain.CodeNotAuthenticated, "The mock session is invalid."))
			return
		}
		session, err := a.store.GetSessionByTokenHash(r.Context(), hash)
		if err != nil {
			writeError(w, r, domain.NewError(domain.CodeNotAuthenticated, "The mock session is invalid or expired."))
			return
		}
		if a.now().Sub(session.LastSeenAt) >= time.Minute {
			_, _ = a.store.TouchSession(r.Context(), session.ID, a.now())
		}
		ctx := context.WithValue(r.Context(), userKey, session.User)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func activeUser(ctx context.Context) domain.User {
	user, _ := ctx.Value(userKey).(domain.User)
	return user
}

func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.store.ListUsers(r.Context())
	if err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	writeData(w, r, http.StatusOK, userListResponse(users))
}

func (a *API) createSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID string `json:"user_id"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	userID, parseErr := domain.ParseRecordID(body.UserID)
	user, err := a.store.GetUser(r.Context(), userID)
	if parseErr != nil || err != nil {
		writeError(w, r, domain.NewError(domain.CodeNodeNotFound, "The selected demo user does not exist."))
		return
	}
	cookieValue, hash, err := newSessionToken(a.sessionSecret)
	if err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	session, err := a.store.CreateSession(r.Context(), hash, user.ID, a.now().Add(12*time.Hour))
	if err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	_, _ = a.store.InsertAuditEvent(r.Context(), application.AuditRecordCommand{
		ID: newUUID(), EventType: "session.started", ActorUserID: &user.ID,
		ResourceType: "session", ResourceID: &session.ID,
		Status:        domain.AuditStatusSucceeded,
		Metadata:      application.AuditMetadata{"mock_auth": true},
		ViewerUserIDs: []domain.RecordID{user.ID},
	})
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: cookieValue, Path: "/", MaxAge: 43200, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
	writeData(w, r, http.StatusCreated, sessionDTO{
		User: userResponse(user), MockAuth: true,
		Disclosure: "Local demo authentication only.",
	})
}

func (a *API) getSession(w http.ResponseWriter, r *http.Request) {
	writeData(w, r, http.StatusOK, sessionDTO{
		User: userResponse(activeUser(r.Context())), MockAuth: true,
		Disclosure: "Local demo authentication only.",
	})
}

func (a *API) deleteSession(w http.ResponseWriter, r *http.Request) {
	cookie, _ := r.Cookie(sessionCookieName)
	hash, err := verifySessionToken(cookie.Value, a.sessionSecret)
	if err == nil {
		_, _ = a.store.DeleteSession(r.Context(), hash)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
	w.WriteHeader(http.StatusNoContent)
}

type nodeCreateRequest struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
}

func validateNodeRequest(body *nodeCreateRequest) error {
	body.Slug = strings.TrimSpace(body.Slug)
	body.DisplayName = strings.TrimSpace(body.DisplayName)
	if !slugPattern.MatchString(body.Slug) {
		return domain.NewError(domain.CodeInvalidRequest, "The slug must use lower-case letters, digits, and internal hyphens.")
	}
	if body.DisplayName == "" {
		body.DisplayName = body.Slug
	}
	if len([]rune(body.DisplayName)) > 120 {
		return domain.NewError(domain.CodeInvalidRequest, "The display name is too long.")
	}
	return nil
}

func ownedScope(r *http.Request) (*domain.RecordID, error) {
	scope := r.URL.Query().Get("scope")
	if scope == "" || scope == "owned" {
		id := activeUser(r.Context()).ID
		return &id, nil
	}
	if scope == "network" {
		return nil, nil
	}
	return nil, domain.NewError(domain.CodeInvalidRequest, "scope must be owned or network")
}

func (a *API) listBrains(w http.ResponseWriter, r *http.Request) {
	owner, err := ownedScope(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	brains, err := a.store.ListBrains(r.Context(), owner)
	if err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	writeData(w, r, http.StatusOK, brainListResponse(brains, owner != nil))
}

func (a *API) createBrain(w http.ResponseWriter, r *http.Request) {
	var body nodeCreateRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	if err := validateNodeRequest(&body); err != nil {
		writeError(w, r, err)
		return
	}
	brain, err := a.store.CreateBrain(r.Context(), activeUser(r.Context()).ID, body.Slug, body.DisplayName)
	if err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	a.audit(r, "brain.created", "brain", brain.ID, &brain.ID, nil, nil, domain.AuditStatusSucceeded, application.AuditMetadata{"canonical_id": brain.CanonicalID}, []domain.RecordID{brain.OwnerUserID})
	writeData(w, r, http.StatusCreated, brainResponse(brain, true))
}

func (a *API) getBrain(w http.ResponseWriter, r *http.Request) {
	brainID, parseErr := domain.ParseBrainID(r.PathValue("brainId"))
	brain, err := a.store.GetBrainByCanonicalID(r.Context(), brainID)
	if parseErr != nil || err != nil {
		writeError(w, r, domain.NewError(domain.CodeNodeNotFound, "The Brain does not exist."))
		return
	}
	writeData(w, r, http.StatusOK, brainResponse(brain, brain.OwnerUserID == activeUser(r.Context()).ID))
}

func (a *API) deleteBrain(w http.ResponseWriter, r *http.Request) {
	canonicalID := r.PathValue("brainId")
	if r.URL.Query().Get("confirm_id") != canonicalID {
		writeError(w, r, domain.NewError(domain.CodeInvalidRequest, "confirm_id must exactly match the Brain ID."))
		return
	}
	brainID, parseErr := domain.ParseBrainID(canonicalID)
	brain, err := a.store.GetBrainByCanonicalID(r.Context(), brainID)
	if parseErr != nil || err != nil {
		writeError(w, r, domain.NewError(domain.CodeNodeNotFound, "The Brain does not exist."))
		return
	}
	if brain.OwnerUserID != activeUser(r.Context()).ID {
		writeError(w, r, domain.NewError(domain.CodeNotAuthorized, "Only the Brain owner may delete it."))
		return
	}
	if _, err := a.store.DeleteBrain(r.Context(), brain.ID); err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	a.audit(r, "brain.deleted", "brain", brain.ID, nil, nil, nil, domain.AuditStatusSucceeded, application.AuditMetadata{"canonical_id": brain.CanonicalID}, []domain.RecordID{brain.OwnerUserID})
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) listServices(w http.ResponseWriter, r *http.Request) {
	owner, err := ownedScope(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	services, err := a.store.ListServices(r.Context(), owner)
	if err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	writeData(w, r, http.StatusOK, serviceListResponse(services, owner != nil))
}

func (a *API) createService(w http.ResponseWriter, r *http.Request) {
	var body nodeCreateRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	if err := validateNodeRequest(&body); err != nil {
		writeError(w, r, err)
		return
	}
	service, err := a.store.CreateService(r.Context(), activeUser(r.Context()).ID, body.Slug, body.DisplayName)
	if err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	a.audit(r, "service.created", "service", service.ID, nil, &service.ID, nil, domain.AuditStatusSucceeded, application.AuditMetadata{"canonical_id": service.CanonicalID}, []domain.RecordID{service.OwnerUserID})
	writeData(w, r, http.StatusCreated, serviceResponse(service, true))
}

func (a *API) getService(w http.ResponseWriter, r *http.Request) {
	serviceID, parseErr := domain.ParseServiceID(r.PathValue("serviceId"))
	service, err := a.store.GetServiceByCanonicalID(r.Context(), serviceID)
	if parseErr != nil || err != nil {
		writeError(w, r, domain.NewError(domain.CodeNodeNotFound, "The Service does not exist."))
		return
	}
	writeData(w, r, http.StatusOK, serviceResponse(service, service.OwnerUserID == activeUser(r.Context()).ID))
}

func (a *API) deleteService(w http.ResponseWriter, r *http.Request) {
	canonicalID := r.PathValue("serviceId")
	if r.URL.Query().Get("confirm_id") != canonicalID {
		writeError(w, r, domain.NewError(domain.CodeInvalidRequest, "confirm_id must exactly match the Service ID."))
		return
	}
	serviceID, parseErr := domain.ParseServiceID(canonicalID)
	service, err := a.store.GetServiceByCanonicalID(r.Context(), serviceID)
	if parseErr != nil || err != nil {
		writeError(w, r, domain.NewError(domain.CodeNodeNotFound, "The Service does not exist."))
		return
	}
	if service.OwnerUserID != activeUser(r.Context()).ID {
		writeError(w, r, domain.NewError(domain.CodeNotAuthorized, "Only the Service owner may delete it."))
		return
	}
	if _, err := a.store.DeleteService(r.Context(), service.ID); err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	a.audit(r, "service.deleted", "service", service.ID, nil, nil, nil, domain.AuditStatusSucceeded, application.AuditMetadata{"canonical_id": service.CanonicalID}, []domain.RecordID{service.OwnerUserID})
	w.WriteHeader(http.StatusNoContent)
}

func databaseError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NewError(domain.CodeNodeNotFound, "The requested resource does not exist.")
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return domain.NewError(domain.CodeNameAlreadyExists, "That name is already in use.")
		case "P0001":
			return domain.NewError(domain.CodeResourceInUse, "The resource is referenced by an active route.")
		}
	}
	return domain.NewError(domain.CodeInvalidRequest, "The request could not be persisted.")
}
