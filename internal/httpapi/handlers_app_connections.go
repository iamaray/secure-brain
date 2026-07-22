package httpapi

import (
	"net/http"
	"strings"
	"sync"

	"secure-brain/internal/domain"
)

// appConnectionState intentionally keeps this prototype-only preference in
// process. Routes remain the durable source of truth for executable wiring.
type appConnectionState struct {
	mu          sync.RWMutex
	byBrain     map[string]map[string]struct{}
	initialized map[string]bool
}

func newAppConnectionState() *appConnectionState {
	return &appConnectionState{
		byBrain:     make(map[string]map[string]struct{}),
		initialized: make(map[string]bool),
	}
}

func (s *appConnectionState) seedOnce(brainID string, serviceIDs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initialized[brainID] {
		return
	}
	connected := make(map[string]struct{}, len(serviceIDs))
	for _, serviceID := range serviceIDs {
		connected[serviceID] = struct{}{}
	}
	s.byBrain[brainID] = connected
	s.initialized[brainID] = true
}

func (s *appConnectionState) connect(brainID, serviceID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	connected := s.byBrain[brainID]
	if connected == nil {
		connected = make(map[string]struct{})
		s.byBrain[brainID] = connected
	}
	_, existed := connected[serviceID]
	connected[serviceID] = struct{}{}
	s.initialized[brainID] = true
	return !existed
}

func (s *appConnectionState) disconnect(brainID, serviceID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	connected := s.byBrain[brainID]
	_, existed := connected[serviceID]
	delete(connected, serviceID)
	s.initialized[brainID] = true
	return existed
}

func (s *appConnectionState) contains(brainID, serviceID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.byBrain[brainID][serviceID]
	return ok
}

type appConnectionResponse struct {
	ID          string `json:"id"`
	ServiceID   string `json:"service_id"`
	CanonicalID string `json:"canonical_id"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	IconPath    string `json:"icon_path"`
	Connected   bool   `json:"connected"`
}

type appConnectionRequest struct {
	ServiceID string `json:"service_id"`
}

type appDefinition struct {
	ID, CanonicalID, DisplayName, Description, IconPath string
}

var localAppCatalog = []appDefinition{
	{ID: "notion", CanonicalID: "service.notion", DisplayName: "Notion", Description: "Pages and databases", IconPath: "/app-icons/notion.svg"},
	{ID: "obsidian", CanonicalID: "service.obsidian", DisplayName: "Obsidian", Description: "Local knowledge vault", IconPath: "/app-icons/obsidian.svg"},
	{ID: "google-drive", CanonicalID: "app.google-drive", DisplayName: "Google Drive", Description: "Documents and files", IconPath: "/app-icons/google-drive.svg"},
	{ID: "dropbox", CanonicalID: "app.dropbox", DisplayName: "Dropbox", Description: "Cloud files", IconPath: "/app-icons/dropbox.svg"},
	{ID: "slack", CanonicalID: "app.slack", DisplayName: "Slack", Description: "Messages and channels", IconPath: "/app-icons/slack.svg"},
	{ID: "github", CanonicalID: "app.github", DisplayName: "GitHub", Description: "Code and projects", IconPath: "/app-icons/github.svg"},
}

func (a *API) seedAppConnections(r *http.Request, brainID string) error {
	routes, err := a.store.ListNetworkRoutes(r.Context())
	if err != nil {
		return err
	}
	serviceIDs := make([]string, 0)
	for _, route := range routes {
		participates := route.SourceBrainID == brainID || (route.DestinationBrainID != nil && *route.DestinationBrainID == brainID)
		if !participates {
			continue
		}
		for _, hop := range route.Hops {
			serviceIDs = append(serviceIDs, hop.CanonicalID)
		}
	}
	a.appConnections.seedOnce(brainID, serviceIDs)
	return nil
}

func (a *API) appCatalog(brainID string) []appConnectionResponse {
	apps := make([]appConnectionResponse, 0, len(localAppCatalog))
	for _, app := range localAppCatalog {
		apps = append(apps, appConnectionResponse{
			ID: app.ID, ServiceID: app.CanonicalID, CanonicalID: app.CanonicalID,
			DisplayName: app.DisplayName, Description: app.Description, IconPath: app.IconPath,
			Connected: a.appConnections.contains(brainID, app.CanonicalID),
		})
	}
	return apps
}

func (a *API) listAppConnections(w http.ResponseWriter, r *http.Request) {
	brain, err := a.ownerBrain(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := a.seedAppConnections(r, brain.ID); err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	writeData(w, r, http.StatusOK, a.appCatalog(brain.ID))
}

func (a *API) createAppConnection(w http.ResponseWriter, r *http.Request) {
	brain, err := a.ownerBrain(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var body appConnectionRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	app, err := resolveCatalogApp(body.ServiceID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := a.seedAppConnections(r, brain.ID); err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	created := a.appConnections.connect(brain.ID, app.CanonicalID)
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeData(w, r, status, appConnectionResponse{
		ID: app.ID, ServiceID: app.CanonicalID, CanonicalID: app.CanonicalID,
		DisplayName: app.DisplayName, Description: app.Description, IconPath: app.IconPath, Connected: true,
	})
}

func (a *API) deleteAppConnection(w http.ResponseWriter, r *http.Request) {
	brain, err := a.ownerBrain(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	serviceID := strings.TrimSpace(r.PathValue("serviceId"))
	if serviceID == "" {
		var body appConnectionRequest
		if err := decodeJSON(w, r, &body); err != nil {
			writeError(w, r, err)
			return
		}
		serviceID = body.ServiceID
	}
	app, err := resolveCatalogApp(serviceID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := a.seedAppConnections(r, brain.ID); err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	a.appConnections.disconnect(brain.ID, app.CanonicalID)
	w.WriteHeader(http.StatusNoContent)
}

func resolveCatalogApp(identifier string) (appDefinition, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return appDefinition{}, domain.NewError(domain.CodeInvalidRequest, "service_id is required.")
	}
	for _, app := range localAppCatalog {
		if identifier == app.ID || identifier == app.CanonicalID {
			return app, nil
		}
	}
	return appDefinition{}, domain.NewError(domain.CodeNodeNotFound, "The app does not exist.")
}
