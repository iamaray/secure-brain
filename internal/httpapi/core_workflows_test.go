package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"secure-brain/internal/application"
	"secure-brain/internal/domain"
	openaiapi "secure-brain/internal/openai"
	"secure-brain/internal/query"
	"secure-brain/internal/routes"
	"secure-brain/internal/storage"
	"secure-brain/internal/store"
)

type recordedObject struct {
	body      []byte
	mediaType string
}

type recordingObjectStore struct {
	mu          sync.Mutex
	objects     map[string]recordedObject
	calls       []string
	failNextPut error
}

func newRecordingObjectStore() *recordingObjectStore {
	return &recordingObjectStore{objects: make(map[string]recordedObject)}
}

func (s *recordingObjectStore) Put(_ context.Context, path, mediaType string, body io.Reader, size int64, upsert bool) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, fmt.Sprintf("put %s %s %d upsert=%t", path, mediaType, size, upsert))
	if s.failNextPut != nil {
		err := s.failNextPut
		s.failNextPut = nil
		return err
	}
	s.objects[path] = recordedObject{body: append([]byte(nil), data...), mediaType: mediaType}
	return nil
}

func (s *recordingObjectStore) Get(_ context.Context, path string) (io.ReadCloser, storage.ObjectMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "get "+path)
	object, ok := s.objects[path]
	if !ok {
		return nil, storage.ObjectMetadata{}, domain.NewError(domain.CodeStorageProviderError, "Storage is temporarily unavailable.")
	}
	return io.NopCloser(bytes.NewReader(object.body)), storage.ObjectMetadata{
		MediaType: object.mediaType,
		Size:      int64(len(object.body)),
	}, nil
}

func (s *recordingObjectStore) Delete(_ context.Context, paths []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "delete "+strings.Join(paths, ","))
	for _, path := range paths {
		delete(s.objects, path)
	}
	return nil
}

func (s *recordingObjectStore) takeCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := append([]string(nil), s.calls...)
	s.calls = nil
	return result
}

type recordingChatClient struct {
	mu        sync.Mutex
	calls     []openaiapi.Request
	responses []string
	err       error
}

func (c *recordingChatClient) Respond(_ context.Context, request openaiapi.Request) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	copyRequest := request
	copyRequest.Input = append([]openaiapi.Message(nil), request.Input...)
	c.calls = append(c.calls, copyRequest)
	if c.err != nil {
		return "", c.err
	}
	if len(c.responses) == 0 {
		return "recorded assistant response", nil
	}
	response := c.responses[0]
	c.responses = c.responses[1:]
	return response, nil
}

type workflowFixture struct {
	pool        *pgxpool.Pool
	db          *store.Store
	api         *API
	objects     *recordingObjectStore
	chat        *recordingChatClient
	user        domain.User
	source      domain.Brain
	destination domain.Brain
	service     domain.Service
	suffix      string
}

func newWorkflowFixture(t *testing.T) *workflowFixture {
	t.Helper()
	databaseURL := os.Getenv("DATABASE_TEST_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_TEST_URL is not set; skipping core workflow characterization integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open DATABASE_TEST_URL: %v", err)
	}
	db := store.New(pool)
	if err := db.CheckSchemaVersion(ctx); err != nil {
		pool.Close()
		t.Fatalf("DATABASE_TEST_URL must contain SecureBrain schema v%s: %v", store.SchemaVersion, err)
	}

	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	var user domain.User
	err = pool.QueryRow(ctx, `
		insert into public.app_users (handle, display_name)
		values ($1, $2)
		returning id, handle, display_name, created_at
	`, "workflow-"+suffix, "Workflow "+suffix).Scan(&user.ID, &user.Handle, &user.DisplayName, &user.CreatedAt)
	if err != nil {
		pool.Close()
		t.Fatalf("create isolated workflow user: %v", err)
	}

	fixture := &workflowFixture{pool: pool, db: db, user: user, suffix: suffix}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		statements := []string{
			`delete from public.audit_events where actor_user_id = $1`,
			`delete from public.idempotency_records where user_id = $1`,
			`delete from public.transfers where source_brain_id in (select id from public.brains where owner_user_id = $1) or destination_brain_id in (select id from public.brains where owner_user_id = $1)`,
			`delete from public.route_executions where actor_user_id = $1`,
			`delete from public.query_paths where brain_id in (select id from public.brains where owner_user_id = $1)`,
			`delete from public.chat_messages where user_id = $1`,
			`delete from public.assets where brain_id in (select id from public.brains where owner_user_id = $1)`,
			`delete from public.brains where owner_user_id = $1`,
			`delete from public.services where owner_user_id = $1`,
			`delete from public.app_users where id = $1`,
		}
		for _, statement := range statements {
			if _, cleanupErr := pool.Exec(cleanupCtx, statement, user.ID); cleanupErr != nil {
				t.Errorf("workflow cleanup failed: %v", cleanupErr)
			}
		}
		pool.Close()
	})

	fixture.source, err = db.CreateBrain(ctx, user.ID, "source-"+suffix, "Workflow Source")
	if err != nil {
		t.Fatalf("create source Brain: %v", err)
	}
	fixture.destination, err = db.CreateBrain(ctx, user.ID, "dest-"+suffix, "Workflow Destination")
	if err != nil {
		t.Fatalf("create destination Brain: %v", err)
	}
	fixture.service, err = db.CreateService(ctx, user.ID, "identity-"+suffix, "Workflow Identity")
	if err != nil {
		t.Fatalf("create Service: %v", err)
	}
	fixture.objects = newRecordingObjectStore()
	fixture.chat = &recordingChatClient{responses: []string{"first answer", "second answer"}}
	fixture.api = &API{
		store: fixture.db, objects: fixture.objects, now: time.Now,
		maxFileBytes: 10 << 20, maxPreviewBytes: 256 << 10, maxCSVRows: 500,
		maxRoutePayloadBytes: 25 << 20, maxRouteHops: 20, transferTTL: 24 * time.Hour,
		chat: fixture.chat, chatModel: "workflow-model", chatHistoryMessages: 20,
		chatMaxOutputTokens: 64, appConnections: newAppConnectionState(),
		networkCanvas: newNetworkCanvasState(),
	}
	return fixture
}

func workflowRequest(method, target string, user domain.User, body any, pathValues map[string]string) *http.Request {
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	request := httptest.NewRequest(method, target, reader)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range pathValues {
		request.SetPathValue(name, value)
	}
	return request.WithContext(context.WithValue(request.Context(), userKey, user))
}

func workflowUploadRequest(t *testing.T, fixture *workflowFixture, objectKey, filename, mediaType string, data []byte, overwrite bool) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("object_key", objectKey); err != nil {
		t.Fatal(err)
	}
	if overwrite {
		if err := writer.WriteField("overwrite", "true"); err != nil {
			t.Fatal(err)
		}
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	header.Set("Content-Type", mediaType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/brains/"+string(fixture.source.CanonicalID)+"/assets", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.SetPathValue("brainId", string(fixture.source.CanonicalID))
	return request.WithContext(context.WithValue(request.Context(), userKey, fixture.user))
}

func decodeWorkflowData[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response envelope: %v body=%s", err, response.Body.String())
	}
	var result T
	if err := json.Unmarshal(envelope.Data, &result); err != nil {
		t.Fatalf("decode response data: %v data=%s", err, envelope.Data)
	}
	return result
}

func requireWorkflowStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, want, response.Body.String())
	}
}

func requireCallSequence(t *testing.T, got []string, operations ...string) {
	t.Helper()
	if len(got) != len(operations) {
		t.Fatalf("calls = %#v, want operations %#v", got, operations)
	}
	for i, operation := range operations {
		if !strings.HasPrefix(got[i], operation+" ") {
			t.Fatalf("call %d = %q, want %q operation; all=%#v", i, got[i], operation, got)
		}
	}
}

func TestCoreWorkflowCharacterizationIntegration(t *testing.T) {
	fixture := newWorkflowFixture(t)
	ctx := context.Background()
	objectKey := "workflow/" + fixture.suffix + "/notes.txt"
	var asset domain.Asset

	t.Run("asset upload overwrite preview download and failure", func(t *testing.T) {
		response := httptest.NewRecorder()
		fixture.api.uploadAsset(response, workflowUploadRequest(t, fixture, objectKey, "notes.txt", "text/plain", []byte("first version"), false))
		requireWorkflowStatus(t, response, http.StatusCreated)
		asset = decodeWorkflowData[domain.Asset](t, response)
		persisted, err := fixture.db.GetAssetInBrain(ctx, fixture.source.ID, asset.ID)
		if err != nil {
			t.Fatalf("load uploaded asset: %v", err)
		}
		if persisted.ObjectKey != domain.ObjectKey(objectKey) || persisted.ProcessingState != domain.AssetStateReady {
			t.Fatalf("unexpected persisted upload: %#v", persisted)
		}
		requireCallSequence(t, fixture.objects.takeCalls(), "put")

		response = httptest.NewRecorder()
		fixture.api.uploadAsset(response, workflowUploadRequest(t, fixture, objectKey, "notes.txt", "text/plain", []byte("second version"), true))
		requireWorkflowStatus(t, response, http.StatusOK)
		overwritten := decodeWorkflowData[domain.Asset](t, response)
		if overwritten.ID != asset.ID {
			t.Fatalf("overwrite changed asset identity: got %s want %s", overwritten.ID, asset.ID)
		}
		persisted, err = fixture.db.GetAssetInBrain(ctx, fixture.source.ID, asset.ID)
		if err != nil {
			t.Fatal(err)
		}
		asset = persisted
		requireCallSequence(t, fixture.objects.takeCalls(), "put", "delete")

		previewRequest := workflowRequest(http.MethodGet, "/api/brains/x/assets/y/content", fixture.user, nil, map[string]string{
			"brainId": string(fixture.source.CanonicalID),
			"assetId": string(asset.ID),
		})
		response = httptest.NewRecorder()
		fixture.api.assetContent(response, previewRequest)
		requireWorkflowStatus(t, response, http.StatusOK)
		preview := decodeWorkflowData[struct {
			Preview struct {
				Kind string `json:"kind"`
				Text string `json:"text"`
			} `json:"preview"`
		}](t, response)
		if preview.Preview.Kind != "text" || preview.Preview.Text != "second version" {
			t.Fatalf("unexpected preview: %#v", preview.Preview)
		}
		requireCallSequence(t, fixture.objects.takeCalls(), "get")

		downloadRequest := workflowRequest(http.MethodGet, "/api/brains/x/assets/y/content?download=true", fixture.user, nil, map[string]string{
			"brainId": string(fixture.source.CanonicalID),
			"assetId": string(asset.ID),
		})
		response = httptest.NewRecorder()
		fixture.api.assetContent(response, downloadRequest)
		requireWorkflowStatus(t, response, http.StatusOK)
		if response.Body.String() != "second version" || !strings.Contains(response.Header().Get("Content-Disposition"), "notes.txt") {
			t.Fatalf("unexpected download: headers=%v body=%q", response.Header(), response.Body.String())
		}
		requireCallSequence(t, fixture.objects.takeCalls(), "get")

		failedKey := "workflow/" + fixture.suffix + "/failed.txt"
		fixture.objects.failNextPut = domain.NewError(domain.CodeStorageProviderError, "Storage is temporarily unavailable.")
		response = httptest.NewRecorder()
		fixture.api.uploadAsset(response, workflowUploadRequest(t, fixture, failedKey, "failed.txt", "text/plain", []byte("not stored"), false))
		requireWorkflowStatus(t, response, http.StatusBadGateway)
		failed, err := fixture.db.GetAssetByObjectKey(ctx, fixture.source.ID, domain.ObjectKey(failedKey))
		if err != nil {
			t.Fatalf("load failed upload record: %v", err)
		}
		if failed.ProcessingState != domain.AssetStateUploadFailed {
			t.Fatalf("failed upload state = %s", failed.ProcessingState)
		}
		requireCallSequence(t, fixture.objects.takeCalls(), "put")

		events, err := fixture.db.ListAuditEvents(ctx, fixture.user.ID, application.AuditQuery{Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		eventTypes := make(map[string]bool)
		for _, event := range events {
			eventTypes[event.EventType] = true
		}
		if !eventTypes["asset.uploaded"] || !eventTypes["asset.overwritten"] {
			t.Fatalf("asset audit attempts missing: %#v", eventTypes)
		}
	})

	path := "/workflow-" + fixture.suffix
	var queryPath queryPathResponse
	t.Run("query path save validate enable disable", func(t *testing.T) {
		body := queryPathRequest{
			Path: path, AssetIDs: []string{string(asset.ID)},
			Operations:        []domain.Operation{domain.OperationRawRead, domain.OperationTextSearch},
			Visibility:        domain.VisibilityPrivate,
			AllowedBrainIDs:   []string{string(fixture.destination.CanonicalID)},
			AllowedServiceIDs: []string{string(fixture.service.CanonicalID)},
			Route: &routeDTO{
				ServiceHops: []string{string(fixture.service.CanonicalID), string(fixture.service.CanonicalID)},
				Terminal:    string(fixture.destination.CanonicalID),
			},
			State: domain.QueryPathStateDraft,
		}
		request := workflowRequest(http.MethodPost, "/api/brains/x/query-paths", fixture.user, body, map[string]string{"brainId": string(fixture.source.CanonicalID)})
		response := httptest.NewRecorder()
		fixture.api.createQueryPath(response, request)
		requireWorkflowStatus(t, response, http.StatusCreated)
		queryPath = decodeWorkflowData[queryPathResponse](t, response)
		config, err := fixture.db.LoadQueryPathConfig(ctx, domain.RecordID(queryPath.ID))
		if err != nil {
			t.Fatal(err)
		}
		if config.QueryPath.State != domain.QueryPathStateDraft || len(config.Route.Hops) != 2 ||
			config.Route.Hops[0].ServiceID != fixture.service.ID || config.Route.Hops[1].ServiceID != fixture.service.ID {
			t.Fatalf("saved configuration drifted: %#v", config)
		}

		validateRequest := workflowRequest(http.MethodPost, "/api/brains/x/query-paths/y/validate", fixture.user, body, map[string]string{
			"brainId": string(fixture.source.CanonicalID), "queryPathId": queryPath.ID,
		})
		response = httptest.NewRecorder()
		fixture.api.validateQueryPath(response, validateRequest)
		requireWorkflowStatus(t, response, http.StatusOK)
		validation := decodeWorkflowData[struct {
			Valid  bool                `json:"valid"`
			Fields []routes.FieldError `json:"fields"`
		}](t, response)
		if !validation.Valid || len(validation.Fields) != 0 {
			t.Fatalf("valid configuration rejected: %#v", validation)
		}

		patchState := func(state domain.QueryPathState, version int64) queryPathResponse {
			t.Helper()
			request := workflowRequest(http.MethodPatch, "/api/brains/x/query-paths/y", fixture.user, map[string]any{"state": state}, map[string]string{
				"brainId": string(fixture.source.CanonicalID), "queryPathId": queryPath.ID,
			})
			request.Header.Set("If-Match", strconv.FormatInt(version, 10))
			response := httptest.NewRecorder()
			fixture.api.patchQueryPath(response, request)
			requireWorkflowStatus(t, response, http.StatusOK)
			return decodeWorkflowData[queryPathResponse](t, response)
		}
		queryPath = patchState(domain.QueryPathStateEnabled, queryPath.ConfigVersion)
		queryPath = patchState(domain.QueryPathStateDisabled, queryPath.ConfigVersion)
		queryPath = patchState(domain.QueryPathStateEnabled, queryPath.ConfigVersion)
		config, err = fixture.db.LoadQueryPathConfig(ctx, domain.RecordID(queryPath.ID))
		if err != nil {
			t.Fatal(err)
		}
		if config.QueryPath.State != domain.QueryPathStateEnabled || config.QueryPath.ConfigVersion != 4 || len(config.Route.Hops) != 2 {
			t.Fatalf("unexpected final configuration: %#v", config)
		}

		events, err := fixture.db.ListAuditEvents(ctx, fixture.user.ID, application.AuditQuery{Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		eventTypes := make(map[string]bool)
		for _, event := range events {
			eventTypes[event.EventType] = true
		}
		for _, wanted := range []string{"query_path.created", "route.validated", "query_path.enabled", "query_path.disabled"} {
			if !eventTypes[wanted] {
				t.Fatalf("missing %q audit attempt in %#v", wanted, eventTypes)
			}
		}
	})

	var pullExecutionID domain.RecordID
	t.Run("pull execution preserves repeated trace", func(t *testing.T) {
		request := workflowRequest(http.MethodPost, "/q/x/y", fixture.user, invocationRequest{
			InitiatingBrainID: string(fixture.source.CanonicalID),
			Query:             queryDTO{Operation: string(query.OperationRawRead)},
		}, map[string]string{
			"sourceBrainId": string(fixture.source.CanonicalID),
			"queryPath":     strings.TrimPrefix(path, "/"),
		})
		response := httptest.NewRecorder()
		fixture.api.pullQueryPath(response, request)
		requireWorkflowStatus(t, response, http.StatusOK)
		result := decodeWorkflowData[struct {
			ExecutionID domain.RecordID `json:"execution_id"`
			Outcome     string          `json:"outcome"`
			Text        string          `json:"text"`
		}](t, response)
		pullExecutionID = result.ExecutionID
		if result.Outcome != "delivered" || result.Text != "second version" {
			t.Fatalf("unexpected pull result: %#v", result)
		}
		requireCallSequence(t, fixture.objects.takeCalls(), "get")
		execution, err := fixture.db.GetExecution(ctx, pullExecutionID)
		if err != nil {
			t.Fatal(err)
		}
		hops, err := fixture.db.ListExecutionHops(ctx, pullExecutionID)
		if err != nil {
			t.Fatal(err)
		}
		if execution.State != domain.ExecutionStateDelivered || len(hops) != 2 {
			t.Fatalf("unexpected execution trace: execution=%#v hops=%#v", execution, hops)
		}
		for index, hop := range hops {
			if hop.HopIndex != index || hop.ServiceCanonicalID != fixture.service.CanonicalID ||
				hop.Status != domain.HopStatusCompleted || hop.InputSHA256 != hop.OutputSHA256 {
				t.Fatalf("hop %d drifted: %#v", index, hop)
			}
		}
		traceRequest := workflowRequest(http.MethodGet, "/api/executions/x/trace", fixture.user, nil,
			map[string]string{"executionId": string(pullExecutionID)})
		response = httptest.NewRecorder()
		fixture.api.getExecutionTrace(response, traceRequest)
		requireWorkflowStatus(t, response, http.StatusOK)
		trace := decodeWorkflowData[struct {
			Execution domain.RouteExecution `json:"execution"`
			Hops      []domain.ExecutionHop `json:"hops"`
		}](t, response)
		if trace.Execution.ID != pullExecutionID || len(trace.Hops) != 2 ||
			trace.Hops[0].HopIndex != 0 || trace.Hops[1].HopIndex != 1 {
			t.Fatalf("unexpected execution trace response: %#v", trace)
		}
	})

	type sendResult struct {
		ExecutionID domain.RecordID `json:"execution_id"`
		Outcome     string          `json:"outcome"`
		Transfer    domain.Transfer `json:"transfer"`
	}
	send := func(t *testing.T, key string) sendResult {
		t.Helper()
		request := workflowRequest(http.MethodPost, "/api/brains/x/query-paths/y/send", fixture.user, map[string]any{
			"query": query.Request{Operation: query.OperationRawRead},
		}, map[string]string{"brainId": string(fixture.source.CanonicalID), "queryPathId": queryPath.ID})
		request.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		fixture.api.sendQueryPath(response, request)
		requireWorkflowStatus(t, response, http.StatusCreated)
		return decodeWorkflowData[sendResult](t, response)
	}

	var acceptedSend, rejectedSend sendResult
	t.Run("send replay records no duplicate effects", func(t *testing.T) {
		key := "send-accepted-" + fixture.suffix
		acceptedSend = send(t, key)
		if acceptedSend.Outcome != "delivered" || acceptedSend.Transfer.Status != domain.TransferStatusPending {
			t.Fatalf("unexpected send result: %#v", acceptedSend)
		}
		requireCallSequence(t, fixture.objects.takeCalls(), "get", "put")
		replayed := send(t, key)
		if replayed.ExecutionID != acceptedSend.ExecutionID || replayed.Transfer.ID != acceptedSend.Transfer.ID {
			t.Fatalf("idempotency replay changed result: first=%#v replay=%#v", acceptedSend, replayed)
		}
		if calls := fixture.objects.takeCalls(); len(calls) != 0 {
			t.Fatalf("replayed send repeated external effects: %#v", calls)
		}
		mismatchedRequest := workflowRequest(http.MethodPost, "/api/brains/x/query-paths/y/send", fixture.user, map[string]any{
			"query": query.Request{Operation: query.OperationTextSearch, Query: "different"},
		}, map[string]string{"brainId": string(fixture.source.CanonicalID), "queryPathId": queryPath.ID})
		mismatchedRequest.Header.Set("Idempotency-Key", key)
		mismatchedResponse := httptest.NewRecorder()
		fixture.api.sendQueryPath(mismatchedResponse, mismatchedRequest)
		requireWorkflowStatus(t, mismatchedResponse, http.StatusConflict)
		if calls := fixture.objects.takeCalls(); len(calls) != 0 {
			t.Fatalf("reused key with different content caused external effects: %#v", calls)
		}
		record, err := fixture.db.GetIdempotencyRecord(ctx, fixture.user.ID, "send:"+queryPath.ID, domain.IdempotencyKey(key))
		if err != nil || record.ResponseStatus == nil || *record.ResponseStatus != http.StatusCreated {
			t.Fatalf("send idempotency record = %#v err=%v", record, err)
		}

		rejectedSend = send(t, "send-rejected-"+fixture.suffix)
		requireCallSequence(t, fixture.objects.takeCalls(), "get", "put")
	})

	t.Run("transfer accept reject and replay", func(t *testing.T) {
		acceptKey := "accept-transfer-" + fixture.suffix
		acceptedObjectKey := "accepted/" + fixture.suffix + "/notes.txt"
		accept := func() *httptest.ResponseRecorder {
			request := workflowRequest(http.MethodPost, "/api/transfers/x/accept", fixture.user, map[string]string{
				"object_key": acceptedObjectKey,
			}, map[string]string{"transferId": string(acceptedSend.Transfer.ID)})
			request.Header.Set("Idempotency-Key", acceptKey)
			response := httptest.NewRecorder()
			fixture.api.acceptTransfer(response, request)
			return response
		}
		response := accept()
		requireWorkflowStatus(t, response, http.StatusOK)
		accepted := decodeWorkflowData[struct {
			TransferID domain.RecordID `json:"transfer_id"`
			Status     string          `json:"status"`
			Asset      domain.Asset    `json:"asset"`
		}](t, response)
		if accepted.TransferID != acceptedSend.Transfer.ID || accepted.Status != "accepted" ||
			accepted.Asset.BrainID != fixture.destination.ID {
			t.Fatalf("unexpected acceptance: %#v", accepted)
		}
		requireCallSequence(t, fixture.objects.takeCalls(), "get", "put", "delete")
		persisted, err := fixture.db.GetTransfer(ctx, acceptedSend.Transfer.ID)
		if err != nil || persisted.Status != domain.TransferStatusAccepted ||
			persisted.AcceptedAssetID == nil || *persisted.AcceptedAssetID != accepted.Asset.ID {
			t.Fatalf("persisted acceptance = %#v err=%v", persisted, err)
		}
		response = accept()
		requireWorkflowStatus(t, response, http.StatusOK)
		replay := decodeWorkflowData[struct {
			TransferID domain.RecordID `json:"transfer_id"`
			Status     string          `json:"status"`
		}](t, response)
		if replay.TransferID != acceptedSend.Transfer.ID || replay.Status != "accepted" {
			t.Fatalf("unexpected accept replay: %#v", replay)
		}
		if calls := fixture.objects.takeCalls(); len(calls) != 0 {
			t.Fatalf("replayed accept repeated external effects: %#v", calls)
		}

		rejectKey := "reject-transfer-" + fixture.suffix
		reject := func() *httptest.ResponseRecorder {
			request := workflowRequest(http.MethodPost, "/api/transfers/x/reject", fixture.user, nil, map[string]string{"transferId": string(rejectedSend.Transfer.ID)})
			request.Header.Set("Idempotency-Key", rejectKey)
			response := httptest.NewRecorder()
			fixture.api.rejectTransfer(response, request)
			return response
		}
		response = reject()
		requireWorkflowStatus(t, response, http.StatusOK)
		rejected := decodeWorkflowData[struct {
			TransferID domain.RecordID `json:"transfer_id"`
			Status     string          `json:"status"`
		}](t, response)
		if rejected.TransferID != rejectedSend.Transfer.ID || rejected.Status != "rejected" {
			t.Fatalf("unexpected rejection: %#v", rejected)
		}
		requireCallSequence(t, fixture.objects.takeCalls(), "delete")
		persisted, err = fixture.db.GetTransfer(ctx, rejectedSend.Transfer.ID)
		if err != nil || persisted.Status != domain.TransferStatusRejected {
			t.Fatalf("persisted rejection = %#v err=%v", persisted, err)
		}
		response = reject()
		requireWorkflowStatus(t, response, http.StatusOK)
		if calls := fixture.objects.takeCalls(); len(calls) != 0 {
			t.Fatalf("replayed reject repeated external effects: %#v", calls)
		}
	})

	t.Run("network app canvas and audit visibility", func(t *testing.T) {
		response := httptest.NewRecorder()
		fixture.api.getNetwork(response, workflowRequest(http.MethodGet, "/api/network", fixture.user, nil, nil))
		requireWorkflowStatus(t, response, http.StatusOK)
		network := decodeWorkflowData[struct {
			Routes []struct {
				QueryPathID string   `json:"query_path_id"`
				ServiceHops []string `json:"service_hops"`
			} `json:"routes"`
		}](t, response)
		found := false
		for _, route := range network.Routes {
			if route.QueryPathID == queryPath.ID {
				found = true
				if len(route.ServiceHops) != 2 || route.ServiceHops[0] != string(fixture.service.CanonicalID) ||
					route.ServiceHops[1] != string(fixture.service.CanonicalID) {
					t.Fatalf("network lost repeated hops: %#v", route)
				}
			}
		}
		if !found {
			t.Fatal("owner cannot see private workflow route")
		}

		users, err := fixture.db.ListUsers(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var outsider domain.User
		for _, user := range users {
			if user.ID != fixture.user.ID {
				outsider = user
				break
			}
		}
		if outsider.ID == "" {
			t.Fatal("no unrelated seeded user available")
		}
		response = httptest.NewRecorder()
		fixture.api.getNetwork(response, workflowRequest(http.MethodGet, "/api/network", outsider, nil, nil))
		requireWorkflowStatus(t, response, http.StatusOK)
		outsiderNetwork := decodeWorkflowData[struct {
			Routes []struct {
				QueryPathID string `json:"query_path_id"`
			} `json:"routes"`
		}](t, response)
		for _, route := range outsiderNetwork.Routes {
			if route.QueryPathID == queryPath.ID {
				t.Fatal("private route leaked to an unrelated user")
			}
		}

		connectRequest := workflowRequest(http.MethodPost, "/api/brains/x/app-connections", fixture.user,
			appConnectionRequest{ServiceID: "app.github"}, map[string]string{"brainId": string(fixture.source.CanonicalID)})
		response = httptest.NewRecorder()
		fixture.api.createAppConnection(response, connectRequest)
		requireWorkflowStatus(t, response, http.StatusCreated)
		connected := decodeWorkflowData[appConnectionResponse](t, response)
		if !connected.Connected || connected.CanonicalID != "app.github" ||
			!fixture.api.appConnections.contains(string(fixture.source.ID), "app.github") {
			t.Fatalf("unexpected app connection: %#v", connected)
		}
		connectRequest = workflowRequest(http.MethodPost, "/api/brains/x/app-connections", fixture.user,
			appConnectionRequest{ServiceID: "github"}, map[string]string{"brainId": string(fixture.source.CanonicalID)})
		response = httptest.NewRecorder()
		fixture.api.createAppConnection(response, connectRequest)
		requireWorkflowStatus(t, response, http.StatusOK)

		removeRequest := workflowRequest(http.MethodDelete, "/api/network/nodes/x", fixture.user, nil,
			map[string]string{"nodeId": "brain.maya"})
		response = httptest.NewRecorder()
		fixture.api.removeNetworkNode(response, removeRequest)
		requireWorkflowStatus(t, response, http.StatusNoContent)
		if fixture.api.networkCanvas.onCanvas(fixture.user.ID, "brain", "brain.maya") {
			t.Fatal("non-owned Brain remained on canvas after removal")
		}
		addRequest := workflowRequest(http.MethodPost, "/api/network/nodes", fixture.user,
			map[string]string{"node_id": "brain.maya"}, nil)
		response = httptest.NewRecorder()
		fixture.api.addNetworkNode(response, addRequest)
		requireWorkflowStatus(t, response, http.StatusOK)
		if !fixture.api.networkCanvas.onCanvas(fixture.user.ID, "brain", "brain.maya") {
			t.Fatal("canvas add did not restore the Brain")
		}

		events, err := fixture.db.ListAuditEvents(ctx, fixture.user.ID, application.AuditQuery{Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		eventTypes := make(map[string]bool)
		for _, event := range events {
			eventTypes[event.EventType] = true
		}
		for _, wanted := range []string{"route.execution_started", "route.hop_completed", "route.execution_completed", "transfer.created", "transfer.accepted", "transfer.rejected"} {
			if !eventTypes[wanted] {
				t.Fatalf("missing workflow audit %q in %#v", wanted, eventTypes)
			}
		}
		auditRequest := workflowRequest(http.MethodGet, "/api/audit-events?event_type=transfer.accepted", fixture.user, nil, nil)
		response = httptest.NewRecorder()
		fixture.api.listAuditEvents(response, auditRequest)
		requireWorkflowStatus(t, response, http.StatusOK)
		visibleAccepted := decodeWorkflowData[[]domain.AuditEvent](t, response)
		if len(visibleAccepted) != 1 || visibleAccepted[0].EventType != "transfer.accepted" {
			t.Fatalf("unexpected viewer-scoped audit response: %#v", visibleAccepted)
		}
		outsiderEvents, err := fixture.db.ListAuditEvents(ctx, outsider.ID, application.AuditQuery{Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range outsiderEvents {
			if event.ActorUserID != nil && *event.ActorUserID == fixture.user.ID {
				t.Fatalf("workflow audit leaked to outsider: %#v", event)
			}
		}
		auditRequest = workflowRequest(http.MethodGet, "/api/audit-events?event_type=transfer.accepted", outsider, nil, nil)
		response = httptest.NewRecorder()
		fixture.api.listAuditEvents(response, auditRequest)
		requireWorkflowStatus(t, response, http.StatusOK)
		if leaked := decodeWorkflowData[[]domain.AuditEvent](t, response); len(leaked) != 0 {
			t.Fatalf("viewer-scoped audit API leaked events: %#v", leaked)
		}
	})

	t.Run("chat provider ordering persistence failure and clear", func(t *testing.T) {
		post := func(message string) *httptest.ResponseRecorder {
			request := workflowRequest(http.MethodPost, "/api/brains/x/chat", fixture.user,
				map[string]string{"message": message}, map[string]string{"brainId": string(fixture.source.CanonicalID)})
			response := httptest.NewRecorder()
			fixture.api.postChat(response, request)
			return response
		}
		response := post("first question")
		requireWorkflowStatus(t, response, http.StatusOK)
		response = post("second question")
		requireWorkflowStatus(t, response, http.StatusOK)
		fixture.chat.mu.Lock()
		calls := append([]openaiapi.Request(nil), fixture.chat.calls...)
		fixture.chat.mu.Unlock()
		if len(calls) != 2 || len(calls[0].Input) != 1 || len(calls[1].Input) != 3 {
			t.Fatalf("unexpected provider calls: %#v", calls)
		}
		if calls[1].Input[0].Content != "first question" || calls[1].Input[1].Content != "first answer" ||
			calls[1].Input[2].Content != "second question" {
			t.Fatalf("chat history order drifted: %#v", calls[1].Input)
		}
		messages, err := fixture.db.ListChatMessages(ctx, fixture.source.ID, fixture.user.ID, 20)
		if err != nil || len(messages) != 4 {
			t.Fatalf("persisted chat = %#v err=%v", messages, err)
		}

		fixture.chat.mu.Lock()
		fixture.chat.err = domain.NewError(domain.CodeChatProviderError, "Chat is temporarily unavailable.")
		fixture.chat.mu.Unlock()
		response = post("provider failure")
		requireWorkflowStatus(t, response, http.StatusBadGateway)
		afterFailure, err := fixture.db.ListChatMessages(ctx, fixture.source.ID, fixture.user.ID, 20)
		if err != nil || len(afterFailure) != 4 {
			t.Fatalf("provider failure changed chat persistence: %#v err=%v", afterFailure, err)
		}
		events, err := fixture.db.ListAuditEvents(ctx, fixture.user.ID, application.AuditQuery{EventType: "chat.failed", Limit: 10})
		if err != nil || len(events) != 1 || events[0].Status != domain.AuditStatusFailed {
			t.Fatalf("chat failure audit = %#v err=%v", events, err)
		}

		clearRequest := workflowRequest(http.MethodDelete, "/api/brains/x/chat", fixture.user, nil,
			map[string]string{"brainId": string(fixture.source.CanonicalID)})
		response = httptest.NewRecorder()
		fixture.api.clearChat(response, clearRequest)
		requireWorkflowStatus(t, response, http.StatusNoContent)
		messages, err = fixture.db.ListChatMessages(ctx, fixture.source.ID, fixture.user.ID, 20)
		if err != nil || len(messages) != 0 {
			t.Fatalf("clear left chat messages: %#v err=%v", messages, err)
		}
	})

	if pullExecutionID == "" {
		t.Fatal("workflow did not create a pull execution")
	}
}

var _ storage.ObjectStore = (*recordingObjectStore)(nil)
var _ openaiapi.ChatClient = (*recordingChatClient)(nil)
