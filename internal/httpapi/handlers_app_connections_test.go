package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"secure-brain/internal/domain"
	"secure-brain/internal/store"
)

func TestAppConnectionStateSeedConnectDisconnect(t *testing.T) {
	state := newAppConnectionState()
	state.seedOnce("brain-1", []string{"service-1", "service-1"})
	state.seedOnce("brain-1", []string{"service-2"})
	if !state.contains("brain-1", "service-1") || state.contains("brain-1", "service-2") {
		t.Fatalf("seedOnce did not preserve the first seed")
	}
	if !state.connect("brain-1", "service-2") {
		t.Fatalf("first connect should report a new connection")
	}
	if state.connect("brain-1", "service-2") {
		t.Fatalf("second connect should be idempotent")
	}
	if !state.disconnect("brain-1", "service-2") || state.contains("brain-1", "service-2") {
		t.Fatalf("disconnect did not remove the connection")
	}
	if state.disconnect("brain-1", "service-2") {
		t.Fatalf("second disconnect should be idempotent")
	}
}

func TestLocalAppCatalogIncludesBundledIconMetadata(t *testing.T) {
	api := &API{appConnections: newAppConnectionState()}
	apps := api.appCatalog("brain-1")
	if len(apps) != 6 {
		t.Fatalf("catalog length = %d, want 6", len(apps))
	}
	seen := map[string]bool{}
	for _, app := range apps {
		if app.CanonicalID == "" || app.DisplayName == "" || app.IconPath == "" || !strings.HasPrefix(app.IconPath, "/app-icons/") {
			t.Fatalf("incomplete catalog entry: %#v", app)
		}
		seen[app.DisplayName] = true
	}
	for _, name := range []string{"Notion", "Obsidian", "Google Drive", "Dropbox", "Slack", "GitHub"} {
		if !seen[name] {
			t.Fatalf("missing app %q", name)
		}
	}
}

func TestAppConnectionHTTPIntegration(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_TEST_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_TEST_URL is not set; skipping app-connection HTTP integration test")
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
	var user domain.User
	var brain domain.Brain
	for _, candidate := range users {
		brains, listErr := db.ListBrains(context.Background(), &candidate.ID)
		if listErr != nil {
			t.Fatalf("list candidate Brains: %v", listErr)
		}
		if len(brains) > 0 {
			user, brain = candidate, brains[0]
			break
		}
	}
	if brain.ID == "" {
		t.Fatal("test database has no owned Brain")
	}

	handler := newContractRouter(db)
	sessionCookie := signInContractUser(t, handler, string(user.ID))
	request := func(method, path string, body []byte) *httptest.ResponseRecorder {
		return contractRequest(handler, method, path, body, http.Header{"Content-Type": {"application/json"}}, sessionCookie)
	}

	listed := request(http.MethodGet, "/api/brains/"+string(brain.CanonicalID)+"/app-connections", nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("GET status = %d body=%s", listed.Code, listed.Body.String())
	}
	var envelope struct {
		Data []appConnectionResponse `json:"data"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &envelope); err != nil || len(envelope.Data) == 0 {
		t.Fatalf("decode app catalog: %v body=%s", err, listed.Body.String())
	}
	var app appConnectionResponse
	for _, candidate := range envelope.Data {
		if candidate.CanonicalID == "app.google-drive" {
			app = candidate
			break
		}
	}
	if app.ServiceID != app.CanonicalID || app.ID == "" || app.DisplayName == "" {
		t.Fatalf("unexpected catalog shape: %#v", app)
	}

	payload, _ := json.Marshal(appConnectionRequest{ServiceID: app.CanonicalID})
	connected := request(http.MethodPost, "/api/brains/"+string(brain.CanonicalID)+"/app-connections", payload)
	if connected.Code != http.StatusCreated {
		t.Fatalf("POST status = %d body=%s", connected.Code, connected.Body.String())
	}

	deleted := request(http.MethodDelete, "/api/brains/"+string(brain.CanonicalID)+"/app-connections/"+app.CanonicalID, nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d body=%s", deleted.Code, deleted.Body.String())
	}
	relisted := request(http.MethodGet, "/api/brains/"+string(brain.CanonicalID)+"/app-connections", nil)
	if relisted.Code != http.StatusOK {
		t.Fatalf("second GET status = %d body=%s", relisted.Code, relisted.Body.String())
	}
	envelope.Data = nil
	if err := json.Unmarshal(relisted.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode second app catalog: %v body=%s", err, relisted.Body.String())
	}
	for _, candidate := range envelope.Data {
		if candidate.CanonicalID == app.CanonicalID && candidate.Connected {
			t.Fatal("DELETE left the app connected")
		}
	}
}
