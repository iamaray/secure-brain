package httpapi

import (
	"net/http"
	"strings"
	"sync"

	"secure-brain/internal/domain"
)

var defaultOffCanvasServices = map[domain.Principal]struct{}{
	"service.pii-scan":      {},
	"service.policy-check":  {},
	"service.deduplication": {},
}

// networkCanvasState records local demo canvas choices without changing the
// durable node catalog or the executable route graph.
type networkCanvasState struct {
	mu            sync.RWMutex
	addedByUser   map[domain.RecordID]map[domain.Principal]struct{}
	removedByUser map[domain.RecordID]map[domain.Principal]struct{}
}

func newNetworkCanvasState() *networkCanvasState {
	return &networkCanvasState{
		addedByUser:   make(map[domain.RecordID]map[domain.Principal]struct{}),
		removedByUser: make(map[domain.RecordID]map[domain.Principal]struct{}),
	}
}

func (s *networkCanvasState) onCanvas(userID domain.RecordID, nodeType string, canonicalID domain.Principal) bool {
	if nodeType != "brain" && nodeType != "service" {
		return true
	}
	s.mu.RLock()
	_, removed := s.removedByUser[userID][canonicalID]
	_, added := s.addedByUser[userID][canonicalID]
	s.mu.RUnlock()
	if removed {
		return false
	}
	if added {
		return true
	}
	_, startsOffCanvas := defaultOffCanvasServices[canonicalID]
	return nodeType != "service" || !startsOffCanvas
}

func (s *networkCanvasState) add(userID domain.RecordID, canonicalID domain.Principal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	added := s.addedByUser[userID]
	if added == nil {
		added = make(map[domain.Principal]struct{})
		s.addedByUser[userID] = added
	}
	added[canonicalID] = struct{}{}
	delete(s.removedByUser[userID], canonicalID)
}

func (s *networkCanvasState) remove(userID domain.RecordID, canonicalID domain.Principal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := s.removedByUser[userID]
	if removed == nil {
		removed = make(map[domain.Principal]struct{})
		s.removedByUser[userID] = removed
	}
	removed[canonicalID] = struct{}{}
	delete(s.addedByUser[userID], canonicalID)
}

type canvasNode struct {
	CanonicalID domain.Principal
	DisplayName string
	Type        string
	OwnerUserID domain.RecordID
}

func (a *API) resolveCanvasNode(r *http.Request, canonicalID string) (canvasNode, error) {
	canonicalID = strings.TrimSpace(canonicalID)
	switch {
	case strings.HasPrefix(canonicalID, "brain."):
		id, parseErr := domain.ParseBrainID(canonicalID)
		brain, err := a.store.GetBrainByCanonicalID(r.Context(), id)
		if parseErr != nil {
			return canvasNode{}, domain.NewError(domain.CodeNodeNotFound, "The requested resource does not exist.")
		}
		if err != nil {
			return canvasNode{}, databaseError(err)
		}
		return canvasNode{CanonicalID: brain.CanonicalID.Principal(), DisplayName: brain.DisplayName, Type: "brain", OwnerUserID: brain.OwnerUserID}, nil
	case strings.HasPrefix(canonicalID, "service."):
		id, parseErr := domain.ParseServiceID(canonicalID)
		service, err := a.store.GetServiceByCanonicalID(r.Context(), id)
		if parseErr != nil {
			return canvasNode{}, domain.NewError(domain.CodeNodeNotFound, "The requested resource does not exist.")
		}
		if err != nil {
			return canvasNode{}, databaseError(err)
		}
		return canvasNode{CanonicalID: service.CanonicalID.Principal(), DisplayName: service.DisplayName, Type: "service", OwnerUserID: service.OwnerUserID}, nil
	default:
		return canvasNode{}, domain.NewError(domain.CodeInvalidRequest, "node_id must be a Brain or Service canonical ID.")
	}
}

func removeCanvasNodeForUser(state *networkCanvasState, userID domain.RecordID, node canvasNode) error {
	if node.OwnerUserID == userID {
		return domain.NewError(domain.CodeNotAuthorized, "Owned nodes cannot be removed from your canvas.")
	}
	state.remove(userID, node.CanonicalID)
	return nil
}

func (a *API) addNetworkNode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		NodeID string `json:"node_id"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	node, err := a.resolveCanvasNode(r, body.NodeID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	userID := activeUser(r.Context()).ID
	a.networkCanvas.add(userID, node.CanonicalID)
	writeData(w, r, http.StatusOK, canvasNodeDTO{
		NodeID: string(node.CanonicalID), DisplayName: node.DisplayName,
		Type: node.Type, OnCanvas: true,
	})
}

func (a *API) removeNetworkNode(w http.ResponseWriter, r *http.Request) {
	node, err := a.resolveCanvasNode(r, r.PathValue("nodeId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := removeCanvasNodeForUser(a.networkCanvas, activeUser(r.Context()).ID, node); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
