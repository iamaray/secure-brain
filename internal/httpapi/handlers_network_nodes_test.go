package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"secure-brain/internal/domain"
	"secure-brain/internal/store"
)

func TestNetworkCanvasStateDefaultsIsolationAndConcurrency(t *testing.T) {
	state := newNetworkCanvasState()
	if state.onCanvas("user-a", "service", "service.pii-scan") {
		t.Fatal("PII scan should start off-canvas")
	}
	if !state.onCanvas("user-a", "service", "service.notion") {
		t.Fatal("Services outside the backend default set should start on-canvas")
	}
	if !state.onCanvas("user-a", "brain", "brain.maya") {
		t.Fatal("Brains should always start on-canvas")
	}

	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			state.add("user-a", "service.pii-scan")
			_ = state.onCanvas("user-a", "service", "service.pii-scan")
		}()
	}
	group.Wait()
	if !state.onCanvas("user-a", "service", "service.pii-scan") {
		t.Fatal("added Service should be on the user's canvas")
	}
	if state.onCanvas("user-b", "service", "service.pii-scan") {
		t.Fatal("canvas membership leaked between users")
	}
}

func TestNetworkCanvasRemoveAddRestoreAndIsolation(t *testing.T) {
	state := newNetworkCanvasState()
	node := canvasNode{CanonicalID: "brain.remote", Type: "brain", OwnerUserID: "user-b"}
	if err := removeCanvasNodeForUser(state, "user-a", node); err != nil {
		t.Fatalf("remove non-owned node: %v", err)
	}
	if state.onCanvas("user-a", node.Type, node.CanonicalID) {
		t.Fatal("removed non-owned Brain remains on canvas")
	}
	if !state.onCanvas("user-b", node.Type, node.CanonicalID) {
		t.Fatal("removal leaked to another user")
	}
	state.add("user-a", node.CanonicalID)
	if !state.onCanvas("user-a", node.Type, node.CanonicalID) {
		t.Fatal("POST-style add did not restore removed node")
	}
}

func TestNetworkCanvasRejectsOwnedNodeRemoval(t *testing.T) {
	state := newNetworkCanvasState()
	node := canvasNode{CanonicalID: "service.mine", Type: "service", OwnerUserID: "user-a"}
	err := removeCanvasNodeForUser(state, "user-a", node)
	if err == nil {
		t.Fatal("owned node removal succeeded")
	}
	if !state.onCanvas("user-a", node.Type, node.CanonicalID) {
		t.Fatal("owned node was hidden after rejected removal")
	}
}

func TestAddNetworkNodeRejectsNonCanonicalNodeBeforeLookup(t *testing.T) {
	api := &API{networkCanvas: newNetworkCanvasState()}
	req := httptest.NewRequest(http.MethodPost, "/api/network/nodes", bytes.NewBufferString(`{"node_id":"caller:route"}`))
	req = req.WithContext(context.WithValue(req.Context(), userKey, domain.User{ID: "user-a"}))
	response := httptest.NewRecorder()
	api.addNetworkNode(response, req)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestAddNetworkNodeHTTPIntegration(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_TEST_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_TEST_URL is not set; skipping network-node HTTP integration test")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer pool.Close()
	db := store.New(pool)
	users, err := db.ListUsers(context.Background())
	if err != nil || len(users) == 0 {
		t.Fatalf("list seeded users: %v", err)
	}
	services, err := db.ListServices(context.Background(), nil)
	if err != nil || len(services) == 0 {
		t.Fatalf("list seeded Services: %v", err)
	}
	user, service := users[0], services[0]
	api := &API{store: db, networkCanvas: newNetworkCanvasState()}
	body, _ := json.Marshal(map[string]string{"node_id": service.CanonicalID})
	req := httptest.NewRequest(http.MethodPost, "/api/network/nodes", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), userKey, user))
	response := httptest.NewRecorder()
	api.addNetworkNode(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data struct {
			NodeID   string `json:"node_id"`
			OnCanvas bool   `json:"on_canvas"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Data.NodeID != service.CanonicalID || !envelope.Data.OnCanvas {
		t.Fatalf("unexpected response: %#v", envelope.Data)
	}

	brains, err := db.ListBrains(context.Background(), nil)
	if err != nil {
		t.Fatalf("list Brains: %v", err)
	}
	var ownedBrain, nonOwnedBrain domain.Brain
	for _, brain := range brains {
		if brain.OwnerUserID == user.ID && ownedBrain.ID == "" {
			ownedBrain = brain
		}
		if brain.OwnerUserID != user.ID && nonOwnedBrain.ID == "" {
			nonOwnedBrain = brain
		}
	}
	if ownedBrain.ID == "" || nonOwnedBrain.ID == "" {
		t.Fatal("test database needs owned and non-owned Brains")
	}

	remove := func(nodeID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodDelete, "/api/network/nodes/"+nodeID, nil)
		req.SetPathValue("nodeId", nodeID)
		req = req.WithContext(context.WithValue(req.Context(), userKey, user))
		response := httptest.NewRecorder()
		api.removeNetworkNode(response, req)
		return response
	}
	if response := remove(ownedBrain.CanonicalID); response.Code != http.StatusForbidden {
		t.Fatalf("owned DELETE status = %d body=%s", response.Code, response.Body.String())
	}
	if response := remove(nonOwnedBrain.CanonicalID); response.Code != http.StatusNoContent {
		t.Fatalf("non-owned DELETE status = %d body=%s", response.Code, response.Body.String())
	}
	if api.networkCanvas.onCanvas(user.ID, "brain", nonOwnedBrain.CanonicalID) {
		t.Fatal("non-owned DELETE did not hide the Brain")
	}
	restoreBody, _ := json.Marshal(map[string]string{"node_id": nonOwnedBrain.CanonicalID})
	restoreReq := httptest.NewRequest(http.MethodPost, "/api/network/nodes", bytes.NewReader(restoreBody))
	restoreReq = restoreReq.WithContext(context.WithValue(restoreReq.Context(), userKey, user))
	restored := httptest.NewRecorder()
	api.addNetworkNode(restored, restoreReq)
	if restored.Code != http.StatusOK || !api.networkCanvas.onCanvas(user.ID, "brain", nonOwnedBrain.CanonicalID) {
		t.Fatalf("restore status = %d body=%s", restored.Code, restored.Body.String())
	}
}
