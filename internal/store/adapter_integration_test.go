package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"secure-brain/internal/domain"
)

const (
	mayaUserID  = "00000000-0000-4000-8000-000000000001"
	anishUserID = "00000000-0000-4000-8000-000000000002"
	atlasUserID = "00000000-0000-4000-8000-000000000003"
)

type integrationSeeds struct {
	maya     domain.Brain
	anish    domain.Brain
	atlas    domain.Brain
	notion   domain.Service
	obsidian domain.Service
}

func loadIntegrationSeeds(t *testing.T, db *Store) integrationSeeds {
	t.Helper()
	ctx := context.Background()
	getBrain := func(id string) domain.Brain {
		t.Helper()
		value, err := db.GetBrainByCanonicalID(ctx, id)
		if err != nil {
			t.Fatalf("get seeded Brain %s: %v", id, err)
		}
		return value
	}
	getService := func(id string) domain.Service {
		t.Helper()
		value, err := db.GetServiceByCanonicalID(ctx, id)
		if err != nil {
			t.Fatalf("get seeded Service %s: %v", id, err)
		}
		return value
	}
	return integrationSeeds{
		maya:     getBrain("brain.maya"),
		anish:    getBrain("brain.anish"),
		atlas:    getBrain("brain.atlas"),
		notion:   getService("service.notion"),
		obsidian: getService("service.obsidian"),
	}
}

func insertIntegrationAsset(t *testing.T, db *Store, brainID, key string) domain.Asset {
	t.Helper()
	checksum := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	parseError := "characterization parse error"
	asset, err := db.InsertAsset(context.Background(), AssetInput{
		BrainID:          brainID,
		ObjectKey:        key,
		StoragePath:      "integration/" + key,
		OriginalFilename: key,
		MediaType:        "text/csv",
		ByteSize:         17,
		SHA256:           &checksum,
		Format:           domain.AssetFormatCSV,
		ProcessingState:  domain.AssetStateParseFailed,
		ParseError:       &parseError,
	})
	if err != nil {
		t.Fatalf("insert integration asset: %v", err)
	}
	return asset
}

func insertIntegrationExecution(t *testing.T, db *Store, seeds integrationSeeds, suffix string) domain.RouteExecution {
	t.Helper()
	sourceID, destinationID := seeds.maya.ID, seeds.atlas.ID
	actorID := mayaUserID
	destinationCanonicalID := seeds.atlas.CanonicalID
	execution, err := db.InsertExecution(context.Background(), ExecutionInput{
		Mode:                   domain.ExecutionModePush,
		ActorUserID:            &actorID,
		InitiatingBrainID:      &sourceID,
		SourceBrainID:          &sourceID,
		DestinationBrainID:     &destinationID,
		SourceCanonicalID:      seeds.maya.CanonicalID,
		SourcePath:             "/integration-" + suffix,
		DestinationCanonicalID: &destinationCanonicalID,
		Operation:              domain.OperationRawRead,
		State:                  domain.ExecutionStateCreated,
		RouteSnapshot:          json.RawMessage(`{"services":["service.notion"]}`),
		ResultMetadata:         json.RawMessage(`{"phase":"created"}`),
	})
	if err != nil {
		t.Fatalf("insert integration execution: %v", err)
	}
	return execution
}

func insertIntegrationTransfer(t *testing.T, db *Store, seeds integrationSeeds, suffix string, expiresAt time.Time) domain.Transfer {
	t.Helper()
	execution := insertIntegrationExecution(t, db, seeds, suffix)
	sourceID, destinationID := seeds.maya.ID, seeds.atlas.ID
	transfer, err := db.InsertTransfer(context.Background(), TransferInput{
		ExecutionID:            execution.ID,
		SourceBrainID:          &sourceID,
		DestinationBrainID:     &destinationID,
		SourceCanonicalID:      seeds.maya.CanonicalID,
		DestinationCanonicalID: seeds.atlas.CanonicalID,
		StoragePath:            "transfers/" + suffix,
		SuggestedObjectKey:     suffix + ".txt",
		SuggestedFilename:      suffix + ".txt",
		MediaType:              "text/plain",
		ByteSize:               7,
		SHA256:                 "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		ExpiresAt:              expiresAt,
	})
	if err != nil {
		t.Fatalf("insert integration transfer: %v", err)
	}
	return transfer
}

func TestPersistedRecordsAndRelationOrderingIntegration(t *testing.T) {
	db, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	seeds := loadIntegrationSeeds(t, db)

	users, err := db.ListUsers(ctx)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) != 3 || users[0].Handle != "anish" || users[1].Handle != "atlas" || users[2].Handle != "maya" {
		t.Fatalf("seeded user scan/order = %#v", users)
	}
	if got, err := db.GetUser(ctx, mayaUserID); err != nil || got.Handle != "maya" {
		t.Fatalf("get user = %#v, %v", got, err)
	}

	token := bytes.Repeat([]byte{0x2a}, 32)
	session, err := db.CreateSession(ctx, token, mayaUserID, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	loadedSession, err := db.GetSessionByTokenHash(ctx, token)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if loadedSession.ID != session.ID || loadedSession.User.Handle != "maya" || !bytes.Equal(loadedSession.TokenHash, token) {
		t.Fatalf("session scan = %#v", loadedSession)
	}

	first := insertIntegrationAsset(t, db, seeds.maya.ID, "ordered-first.csv")
	second := insertIntegrationAsset(t, db, seeds.maya.ID, "ordered-second.csv")
	if loaded, err := db.GetAsset(ctx, first.ID); err != nil || loaded.SHA256 == "" || loaded.ParseError == nil {
		t.Fatalf("asset scan = %#v, %v", loaded, err)
	}
	if loaded, err := db.GetAssetByObjectKey(ctx, seeds.maya.ID, second.ObjectKey); err != nil || loaded.ID != second.ID {
		t.Fatalf("asset by object key = %#v, %v", loaded, err)
	}

	config, err := db.CreateQueryPath(ctx, QueryPathConfigInput{
		BrainID:           seeds.maya.ID,
		Path:              "/scanner-ordering",
		Visibility:        domain.VisibilityPrivate,
		State:             domain.QueryPathStateEnabled,
		Operations:        []domain.Operation{domain.OperationCSVQuery, domain.OperationRawRead},
		AssetIDs:          []string{second.ID, first.ID},
		AllowedBrainIDs:   []string{seeds.atlas.ID, seeds.anish.ID},
		AllowedServiceIDs: []string{seeds.notion.ID, seeds.obsidian.ID},
		Route: &RouteInput{
			TerminalMode:       domain.TerminalModeFixed,
			DestinationBrainID: &seeds.atlas.ID,
			ServiceIDs:         []string{seeds.obsidian.ID, seeds.notion.ID, seeds.obsidian.ID},
		},
	})
	if err != nil {
		t.Fatalf("create ordered query path: %v", err)
	}
	if got := []string{config.Assets[0].ID, config.Assets[1].ID}; !slices.Equal(got, []string{second.ID, first.ID}) {
		t.Fatalf("asset relation order = %v", got)
	}
	if got := []string{config.AllowedBrains[0].CanonicalID, config.AllowedBrains[1].CanonicalID}; !slices.Equal(got, []string{"brain.anish", "brain.atlas"}) {
		t.Fatalf("Brain grant order = %v", got)
	}
	if got := []string{config.AllowedServices[0].CanonicalID, config.AllowedServices[1].CanonicalID}; !slices.Equal(got, []string{"service.notion", "service.obsidian"}) {
		t.Fatalf("Service grant order = %v", got)
	}
	if config.Route == nil || config.Route.TerminalMode != domain.TerminalModeFixed {
		t.Fatalf("route scan = %#v", config.Route)
	}
	if got := []string{config.Hops[0].ServiceID, config.Hops[1].ServiceID, config.Hops[2].ServiceID}; !slices.Equal(got, []string{seeds.obsidian.ID, seeds.notion.ID, seeds.obsidian.ID}) {
		t.Fatalf("hop relation order = %v", got)
	}
	resolved, err := db.ResolveEnabledQueryPath(ctx, seeds.maya.CanonicalID, config.QueryPath.Path)
	if err != nil || resolved.QueryPath.ID != config.QueryPath.ID {
		t.Fatalf("resolve enabled query path = %#v, %v", resolved, err)
	}

	execution := insertIntegrationExecution(t, db, seeds, "scanner")
	startedAt := time.Now().UTC().Truncate(time.Microsecond)
	completedAt := startedAt.Add(time.Second)
	errorCode := domain.CodeInvalidRequest
	errorMessage := "characterized failure"
	execution, err = db.UpdateExecutionState(ctx, execution.ID, ExecutionUpdate{
		State:          domain.ExecutionStateFailed,
		ResultMetadata: json.RawMessage(`{"attempts":1}`),
		ErrorCode:      &errorCode,
		ErrorMessage:   &errorMessage,
		StartedAt:      &startedAt,
		CompletedAt:    &completedAt,
	})
	if err != nil {
		t.Fatalf("update execution: %v", err)
	}
	if execution.ErrorCode == nil || *execution.ErrorCode != errorCode || execution.ResultMetadata["attempts"] != float64(1) {
		t.Fatalf("execution scan = %#v", execution)
	}
	for index, service := range []domain.Service{seeds.obsidian, seeds.notion} {
		hopCode := domain.Code("")
		var hopCodePointer *domain.Code
		status := domain.HopStatusCompleted
		if index == 1 {
			hopCode = domain.CodeInvalidRequest
			hopCodePointer = &hopCode
			status = domain.HopStatusFailed
		}
		if _, err := db.InsertExecutionHop(ctx, ExecutionHopInput{
			ExecutionID:        execution.ID,
			HopIndex:           index,
			ServiceID:          &service.ID,
			ServiceCanonicalID: service.CanonicalID,
			Status:             status,
			InputSHA256:        fmt.Sprintf("%064d", index+1),
			OutputSHA256:       fmt.Sprintf("%064d", index+2),
			DurationMS:         index + 3,
			ErrorCode:          hopCodePointer,
		}); err != nil {
			t.Fatalf("insert execution hop %d: %v", index, err)
		}
	}
	hops, err := db.ListExecutionHops(ctx, execution.ID)
	if err != nil || len(hops) != 2 || hops[0].HopIndex != 0 || hops[1].ErrorCode == nil {
		t.Fatalf("execution hop scans/order = %#v, %v", hops, err)
	}

	transfer := insertIntegrationTransfer(t, db, seeds, "scanner", time.Now().Add(time.Hour))
	if loaded, err := db.GetTransfer(ctx, transfer.ID); err != nil || loaded.StoragePath != "transfers/scanner" {
		t.Fatalf("transfer scan = %#v, %v", loaded, err)
	}

	auditID := "40000000-0000-4000-8000-000000000001"
	event, err := db.InsertAuditEvent(ctx, AuditEventInput{
		ID:            auditID,
		EventType:     "integration.scanner",
		ActorUserID:   &users[2].ID,
		ResourceType:  "brain",
		ResourceID:    &seeds.maya.ID,
		BrainID:       &seeds.maya.ID,
		Status:        domain.AuditStatusSucceeded,
		Metadata:      json.RawMessage(`{"scanner":true}`),
		ViewerUserIDs: []string{mayaUserID, mayaUserID, atlasUserID},
	})
	if err != nil || event.Metadata["scanner"] != true {
		t.Fatalf("insert audit event = %#v, %v", event, err)
	}
	for _, viewer := range []string{mayaUserID, atlasUserID} {
		events, err := db.ListAuditEvents(ctx, viewer, AuditFilter{NodeID: seeds.maya.CanonicalID})
		if err != nil || len(events) != 1 || events[0].ID != auditID {
			t.Fatalf("audit viewer %s = %#v, %v", viewer, events, err)
		}
	}
	if events, err := db.ListAuditEvents(ctx, anishUserID, AuditFilter{}); err != nil || len(events) != 0 {
		t.Fatalf("non-viewer audit events = %#v, %v", events, err)
	}

	pair, err := db.InsertChatPair(ctx, seeds.maya.ID, mayaUserID, "question", "answer", "gpt-test")
	if err != nil || len(pair) != 2 || pair[0].Role != domain.ChatRoleUser || pair[1].Role != domain.ChatRoleAssistant {
		t.Fatalf("chat pair scans = %#v, %v", pair, err)
	}
	messages, err := db.ListChatMessages(ctx, seeds.maya.ID, mayaUserID, 20)
	if err != nil || len(messages) != 2 || messages[0].Content != "question" || messages[1].Content != "answer" {
		t.Fatalf("chat message scans/order = %#v, %v", messages, err)
	}

	record, err := db.CreateIdempotencyRecord(ctx, mayaUserID, "integration", "scanner-key", fmt.Sprintf("%064d", 9), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("create idempotency record: %v", err)
	}
	record, err = db.CompleteIdempotencyRecord(ctx, record.ID, 201, []byte(`{"id":"result"}`))
	if err != nil || record.ResponseStatus == nil || *record.ResponseStatus != 201 {
		t.Fatalf("complete idempotency scan = %#v, %v", record, err)
	}
	loadedRecord, err := db.GetIdempotencyRecord(ctx, mayaUserID, "integration", "scanner-key")
	var responseBody map[string]string
	if err == nil {
		err = json.Unmarshal(loadedRecord.ResponseBody, &responseBody)
	}
	if err != nil || responseBody["id"] != "result" {
		t.Fatalf("idempotency scan = %#v, %v", loadedRecord, err)
	}

	routes, err := db.ListNetworkRoutes(ctx)
	if err != nil {
		t.Fatalf("list network routes: %v", err)
	}
	var networkRoute *NetworkRoute
	for i := range routes {
		if routes[i].QueryPathID == config.QueryPath.ID {
			networkRoute = &routes[i]
		}
	}
	if networkRoute == nil ||
		!slices.Equal(networkRoute.AllowedBrainOwnerUserIDs, []string{anishUserID, atlasUserID}) ||
		len(networkRoute.Hops) != 3 ||
		networkRoute.Hops[0].CanonicalID != seeds.obsidian.CanonicalID {
		t.Fatalf("network route scans/order = %#v", networkRoute)
	}
	nodes, err := db.SearchNodes(ctx, "notion", 10)
	if err != nil || len(nodes) != 1 || nodes[0].ID != "service.notion" || nodes[0].OwnerUserID != mayaUserID {
		t.Fatalf("network node scan = %#v, %v", nodes, err)
	}
}

func TestOwnershipQueriesAndConfigurationConflictsIntegration(t *testing.T) {
	db, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	seeds := loadIntegrationSeeds(t, db)

	mayaBrains, err := db.ListBrains(ctx, &seeds.maya.OwnerUserID)
	if err != nil {
		t.Fatalf("list Maya Brains: %v", err)
	}
	for _, brain := range mayaBrains {
		if brain.OwnerUserID != mayaUserID {
			t.Fatalf("owner-filtered Brain escaped filter: %#v", brain)
		}
	}
	anishServices, err := db.ListServices(ctx, &seeds.anish.OwnerUserID)
	if err != nil {
		t.Fatalf("list Anish Services: %v", err)
	}
	for _, service := range anishServices {
		if service.OwnerUserID != anishUserID {
			t.Fatalf("owner-filtered Service escaped filter: %#v", service)
		}
	}

	asset := insertIntegrationAsset(t, db, seeds.maya.ID, "ownership.csv")
	if _, err := db.GetAssetInBrain(ctx, seeds.anish.ID, asset.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-Brain asset lookup error = %v, want pgx.ErrNoRows", err)
	}

	input := QueryPathConfigInput{
		BrainID:    seeds.maya.ID,
		Path:       "/configuration-conflict",
		Visibility: domain.VisibilityPrivate,
		State:      domain.QueryPathStateDraft,
		Operations: []domain.Operation{domain.OperationRawRead},
		AssetIDs:   []string{asset.ID},
	}
	created, err := db.CreateQueryPath(ctx, input)
	if err != nil {
		t.Fatalf("create query path: %v", err)
	}
	if _, err := db.db.Exec(ctx, "savepoint duplicate_configuration"); err != nil {
		t.Fatalf("create duplicate-configuration savepoint: %v", err)
	}
	if _, err := db.CreateQueryPath(ctx, input); err == nil {
		t.Fatal("duplicate Brain/path configuration unexpectedly succeeded")
	}
	if _, err := db.db.Exec(ctx, "rollback to savepoint duplicate_configuration"); err != nil {
		t.Fatalf("rollback duplicate configuration: %v", err)
	}
	input.Visibility = domain.VisibilityPublic
	updated, err := db.ReplaceQueryPath(ctx, created.QueryPath.ID, created.QueryPath.ConfigVersion, input)
	if err != nil {
		t.Fatalf("replace query path: %v", err)
	}
	if _, err := db.ReplaceQueryPath(ctx, created.QueryPath.ID, created.QueryPath.ConfigVersion, input); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale replacement error = %v, want ErrVersionConflict", err)
	}
	if err := db.DeleteQueryPath(ctx, seeds.maya.ID, updated.QueryPath.ID, created.QueryPath.ConfigVersion); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale delete error = %v, want ErrVersionConflict", err)
	}
}

func TestTransferLifecycleAndIdempotencyCollisionsIntegration(t *testing.T) {
	db, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	seeds := loadIntegrationSeeds(t, db)
	now := time.Now().UTC().Truncate(time.Microsecond)

	accepted := insertIntegrationTransfer(t, db, seeds, "accepted", now.Add(time.Hour))
	acceptedAsset := insertIntegrationAsset(t, db, seeds.atlas.ID, "accepted.txt")
	acceptedResult, err := db.MarkTransferAccepted(ctx, accepted.ID, acceptedAsset.ID, now)
	if err != nil || acceptedResult.Status != domain.TransferStatusAccepted || acceptedResult.AcceptedAssetID == nil || *acceptedResult.AcceptedAssetID != acceptedAsset.ID {
		t.Fatalf("accepted transition = %#v, %v", acceptedResult, err)
	}
	if _, err := db.MarkTransferRejected(ctx, accepted.ID, now.Add(time.Second)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("accepted-to-rejected error = %v, want pgx.ErrNoRows", err)
	}

	rejected := insertIntegrationTransfer(t, db, seeds, "rejected", now.Add(time.Hour))
	rejectedResult, err := db.MarkTransferRejected(ctx, rejected.ID, now)
	if err != nil || rejectedResult.Status != domain.TransferStatusRejected || rejectedResult.ResolvedAt == nil {
		t.Fatalf("rejected transition = %#v, %v", rejectedResult, err)
	}
	if _, err := db.MarkTransferExpired(ctx, rejected.ID, now.Add(time.Second)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("rejected-to-expired error = %v, want pgx.ErrNoRows", err)
	}

	expired := insertIntegrationTransfer(t, db, seeds, "expired", now.Add(time.Hour))
	expiredResult, err := db.MarkTransferExpired(ctx, expired.ID, now)
	if err != nil || expiredResult.Status != domain.TransferStatusExpired {
		t.Fatalf("expired transition = %#v, %v", expiredResult, err)
	}

	lazy := insertIntegrationTransfer(t, db, seeds, "lazy-expired", now.Add(time.Hour))
	if _, err := db.db.Exec(ctx, `
		update public.transfers
		set created_at = $2 - interval '2 hours', expires_at = $2 - interval '1 hour'
		where id = $1
	`, lazy.ID, now); err != nil {
		t.Fatalf("age lazy transfer: %v", err)
	}
	lazilyExpired, err := db.ExpirePendingTransfers(ctx, now)
	if err != nil {
		t.Fatalf("expire pending transfers: %v", err)
	}
	if len(lazilyExpired) != 1 || lazilyExpired[0].ID != lazy.ID || lazilyExpired[0].Status != domain.TransferStatusExpired {
		t.Fatalf("lazy expiry = %#v", lazilyExpired)
	}

	hashA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hashB := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	first, err := db.CreateIdempotencyRecord(ctx, mayaUserID, "transfer:accept", "collision-key", hashA, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("create idempotency record: %v", err)
	}
	for name, hash := range map[string]string{"same request": hashA, "different request": hashB} {
		t.Run(name, func(t *testing.T) {
			if _, err := db.CreateIdempotencyRecord(ctx, mayaUserID, "transfer:accept", "collision-key", hash, now.Add(time.Hour)); !errors.Is(err, ErrIdempotencyExists) {
				t.Fatalf("collision error = %v, want ErrIdempotencyExists", err)
			}
		})
	}
	if _, err := db.CompleteIdempotencyRecord(ctx, first.ID, 200, []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("complete idempotency record: %v", err)
	}
	if _, err := db.CompleteIdempotencyRecord(ctx, first.ID, 201, []byte(`{"ok":false}`)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("second completion error = %v, want pgx.ErrNoRows", err)
	}
	if _, err := db.CreateIdempotencyRecord(ctx, mayaUserID, "transfer:reject", "collision-key", hashB, now.Add(time.Hour)); err != nil {
		t.Fatalf("same key in another scope should succeed: %v", err)
	}
}

func TestAuditAndTransferPaginationTiesIntegration(t *testing.T) {
	db, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	seeds := loadIntegrationSeeds(t, db)
	tiedAt := time.Now().UTC().Truncate(time.Microsecond)

	auditIDs := []string{
		"41000000-0000-4000-8000-000000000001",
		"41000000-0000-4000-8000-000000000002",
		"41000000-0000-4000-8000-000000000003",
	}
	for _, id := range auditIDs {
		if _, err := db.InsertAuditEvent(ctx, AuditEventInput{
			ID:            id,
			EventType:     "pagination.tie",
			ResourceType:  "brain",
			BrainID:       &seeds.maya.ID,
			Status:        domain.AuditStatusSucceeded,
			ViewerUserIDs: []string{mayaUserID},
		}); err != nil {
			t.Fatalf("insert tied audit %s: %v", id, err)
		}
	}
	if _, err := db.db.Exec(ctx, `update public.audit_events set created_at = $1 where id = any($2::uuid[])`, tiedAt, auditIDs); err != nil {
		t.Fatalf("tie audit timestamps: %v", err)
	}
	events, err := db.ListAuditEvents(ctx, mayaUserID, AuditFilter{EventType: "pagination.tie", Limit: 2})
	if err != nil {
		t.Fatalf("list tied audits: %v", err)
	}
	if got := []string{events[0].ID, events[1].ID}; !slices.Equal(got, []string{auditIDs[2], auditIDs[1]}) {
		t.Fatalf("audit tie order = %v", got)
	}
	events, err = db.ListAuditEvents(ctx, mayaUserID, AuditFilter{EventType: "pagination.tie", Before: &tiedAt, Limit: 10})
	if err != nil || len(events) != 0 {
		t.Fatalf("timestamp-only audit cursor at tie = %#v, %v", events, err)
	}

	transfers := make([]domain.Transfer, 3)
	for i := range transfers {
		transfers[i] = insertIntegrationTransfer(t, db, seeds, fmt.Sprintf("pagination-%d", i), tiedAt.Add(time.Hour))
	}
	transferIDs := []string{transfers[0].ID, transfers[1].ID, transfers[2].ID}
	if _, err := db.db.Exec(ctx, `update public.transfers set created_at = $1 where id = any($2::uuid[])`, tiedAt, transferIDs); err != nil {
		t.Fatalf("tie transfer timestamps: %v", err)
	}
	listed, err := db.ListTransfers(ctx, TransferFilter{BrainID: seeds.atlas.ID, Direction: "incoming", Limit: 3})
	if err != nil {
		t.Fatalf("list tied transfers: %v", err)
	}
	wantTransferIDs := append([]string(nil), transferIDs...)
	slices.Sort(wantTransferIDs)
	slices.Reverse(wantTransferIDs)
	if got := []string{listed[0].ID, listed[1].ID, listed[2].ID}; !slices.Equal(got, wantTransferIDs) {
		t.Fatalf("transfer tie order = %v, want %v", got, wantTransferIDs)
	}
	listed, err = db.ListTransfers(ctx, TransferFilter{BrainID: seeds.atlas.ID, Direction: "incoming", Before: &tiedAt, Limit: 10})
	if err != nil || len(listed) != 0 {
		t.Fatalf("timestamp-only transfer cursor at tie = %#v, %v", listed, err)
	}
}

func TestEnabledRouteDeleteGuardsIntegration(t *testing.T) {
	db, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()

	source, err := db.CreateBrain(ctx, mayaUserID, "guard-source", "Guard source")
	if err != nil {
		t.Fatalf("create guard source: %v", err)
	}
	destination, err := db.CreateBrain(ctx, atlasUserID, "guard-destination", "Guard destination")
	if err != nil {
		t.Fatalf("create guard destination: %v", err)
	}
	service, err := db.CreateService(ctx, anishUserID, "guard-service", "Guard service")
	if err != nil {
		t.Fatalf("create guard service: %v", err)
	}
	asset := insertIntegrationAsset(t, db, source.ID, "guard.csv")
	_, err = db.CreateQueryPath(ctx, QueryPathConfigInput{
		BrainID:           source.ID,
		Path:              "/delete-guards",
		Visibility:        domain.VisibilityPrivate,
		State:             domain.QueryPathStateEnabled,
		Operations:        []domain.Operation{domain.OperationRawRead},
		AssetIDs:          []string{asset.ID},
		AllowedBrainIDs:   []string{destination.ID},
		AllowedServiceIDs: []string{service.ID},
		Route: &RouteInput{
			TerminalMode:       domain.TerminalModeFixed,
			DestinationBrainID: &destination.ID,
			ServiceIDs:         []string{service.ID},
		},
	})
	if err != nil {
		t.Fatalf("create guarded route: %v", err)
	}
	inUse, err := db.AssetReferencedByEnabledPath(ctx, asset.ID)
	if err != nil || !inUse {
		t.Fatalf("enabled asset reference = %v, %v", inUse, err)
	}
	for name, deleteCall := range map[string]func() error{
		"asset": func() error {
			_, err := db.DeleteAsset(ctx, source.ID, asset.ID)
			return err
		},
		"source Brain": func() error {
			_, err := db.DeleteBrain(ctx, source.ID)
			return err
		},
		"destination Brain": func() error {
			_, err := db.DeleteBrain(ctx, destination.ID)
			return err
		},
		"Service": func() error {
			_, err := db.DeleteService(ctx, service.ID)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := db.db.Exec(ctx, "savepoint delete_guard"); err != nil {
				t.Fatalf("create delete-guard savepoint: %v", err)
			}
			deleteErr := deleteCall()
			if _, err := db.db.Exec(ctx, "rollback to savepoint delete_guard"); err != nil {
				t.Fatalf("rollback guarded delete: %v", err)
			}
			if deleteErr == nil || !strings.Contains(deleteErr.Error(), "RESOURCE_IN_USE") {
				t.Fatalf("delete guard error = %v", deleteErr)
			}
		})
	}
}

func TestTransferRowLockSerializesContendingWritersIntegration(t *testing.T) {
	if os.Getenv("DATABASE_TEST_URL") == "" {
		t.Skip("DATABASE_TEST_URL is not set; skipping PostgreSQL store integration test")
	}
	disposableDatabase.once.Do(initDisposableDatabase)
	if disposableDatabase.initErr != nil {
		t.Fatalf("initialize disposable integration database: %v", disposableDatabase.initErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db := New(disposableDatabase.pool)
	seeds := loadIntegrationSeeds(t, db)
	unique := time.Now().UnixNano()
	source, err := db.CreateBrain(ctx, mayaUserID, fmt.Sprintf("contention-source-%d", unique), "Contention source")
	if err != nil {
		t.Fatalf("create contention source: %v", err)
	}
	destination, err := db.CreateBrain(ctx, atlasUserID, fmt.Sprintf("contention-destination-%d", unique), "Contention destination")
	if err != nil {
		t.Fatalf("create contention destination: %v", err)
	}
	seeds.maya = source
	seeds.atlas = destination
	suffix := fmt.Sprintf("contention-%d", unique)
	transfer := insertIntegrationTransfer(t, db, seeds, suffix, time.Now().Add(time.Hour))

	firstTx, err := disposableDatabase.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin first transaction: %v", err)
	}
	defer firstTx.Rollback(context.Background())
	firstStore := &Store{pool: disposableDatabase.pool, db: firstTx}
	if _, err := firstStore.LockTransfer(ctx, transfer.ID); err != nil {
		t.Fatalf("first lock transfer: %v", err)
	}

	secondTx, err := disposableDatabase.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin second transaction: %v", err)
	}
	defer secondTx.Rollback(context.Background())
	secondStore := &Store{pool: disposableDatabase.pool, db: secondTx}
	started := make(chan struct{})
	result := make(chan struct {
		transfer domain.Transfer
		err      error
	}, 1)
	go func() {
		close(started)
		locked, lockErr := secondStore.LockTransfer(ctx, transfer.ID)
		result <- struct {
			transfer domain.Transfer
			err      error
		}{locked, lockErr}
	}()
	<-started
	if err := waitForContendingTransferLock(ctx); err != nil {
		select {
		case got := <-result:
			t.Fatalf("contending lock returned before first transaction completed: %#v, %v", got.transfer, got.err)
		default:
		}
		t.Fatal(err)
	}

	resolvedAt := time.Now().UTC()
	if _, err := firstStore.MarkTransferRejected(ctx, transfer.ID, resolvedAt); err != nil {
		t.Fatalf("resolve transfer while locked: %v", err)
	}
	if err := firstTx.Commit(ctx); err != nil {
		t.Fatalf("commit first transaction: %v", err)
	}
	got := <-result
	if got.err != nil || got.transfer.Status != domain.TransferStatusRejected {
		t.Fatalf("contending lock result = %#v, %v", got.transfer, got.err)
	}
	if err := secondTx.Commit(ctx); err != nil {
		t.Fatalf("commit second transaction: %v", err)
	}
}

func waitForContendingTransferLock(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		err := disposableDatabase.pool.QueryRow(ctx, `
			select exists (
				select 1
				from pg_stat_activity
				where datname = current_database()
				  and pid <> pg_backend_pid()
				  and wait_event_type = 'Lock'
				  and query like '%from public.transfers where id = $1 for update%'
			)
		`).Scan(&blocked)
		if err != nil {
			return fmt.Errorf("inspect contending transfer lock: %w", err)
		}
		if blocked {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for contending transfer lock: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestLockTransferRequiresTransactionIntegration(t *testing.T) {
	db, cleanup := integrationStore(t)
	defer cleanup()
	if _, err := (&Store{pool: db.pool, db: db.pool}).LockTransfer(context.Background(), "00000000-0000-4000-8000-000000000000"); !errors.Is(err, ErrTransactionRequired) {
		t.Fatalf("LockTransfer error = %v, want ErrTransactionRequired", err)
	}
}
