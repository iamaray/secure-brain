package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"secure-brain/internal/domain"
)

func integrationStore(t *testing.T) (*Store, func()) {
	t.Helper()
	databaseURL := os.Getenv("DATABASE_TEST_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_TEST_URL is not set; skipping PostgreSQL store integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open DATABASE_TEST_URL: %v", err)
	}
	if err := New(pool).CheckSchemaVersion(ctx); err != nil {
		pool.Close()
		t.Fatalf("DATABASE_TEST_URL must contain SecureBrain schema v%s: %v", SchemaVersion, err)
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		pool.Close()
		t.Fatalf("begin integration transaction: %v", err)
	}
	return &Store{pool: pool, db: tx}, func() {
		_ = tx.Rollback(ctx)
		pool.Close()
	}
}

func TestQueryPathConfigRoundTripAndVersionConflictIntegration(t *testing.T) {
	db, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()

	source, err := db.GetBrainByCanonicalID(ctx, "brain.maya")
	if err != nil {
		t.Fatalf("get seeded source Brain: %v", err)
	}
	destination, err := db.GetBrainByCanonicalID(ctx, "brain.atlas")
	if err != nil {
		t.Fatalf("get seeded destination Brain: %v", err)
	}
	notion, err := db.GetServiceByCanonicalID(ctx, "service.notion")
	if err != nil {
		t.Fatalf("get seeded Service: %v", err)
	}
	obsidian, err := db.GetServiceByCanonicalID(ctx, "service.obsidian")
	if err != nil {
		t.Fatalf("get second seeded Service: %v", err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	checksum := "0000000000000000000000000000000000000000000000000000000000000000"
	asset, err := db.InsertAsset(ctx, AssetInput{
		BrainID: source.ID, ObjectKey: "store-test-" + suffix + ".txt",
		StoragePath: "store-tests/" + suffix, OriginalFilename: "store-test.txt",
		MediaType: "text/plain", ByteSize: 4, SHA256: &checksum,
		Format: domain.AssetFormatText, ProcessingState: domain.AssetStateReady,
	})
	if err != nil {
		t.Fatalf("insert asset: %v", err)
	}

	created, err := db.CreateQueryPath(ctx, QueryPathConfigInput{
		BrainID: source.ID, Path: "/store-test-" + suffix,
		Visibility: domain.VisibilityPrivate, State: domain.QueryPathStateEnabled,
		Operations: []domain.Operation{domain.OperationRawRead}, AssetIDs: []string{asset.ID},
		AllowedBrainIDs: []string{destination.ID}, AllowedServiceIDs: []string{notion.ID},
		Route: &RouteInput{TerminalMode: domain.TerminalModeFixed,
			DestinationBrainID: &destination.ID, ServiceIDs: []string{notion.ID, notion.ID}},
	})
	if err != nil {
		t.Fatalf("create full query path config: %v", err)
	}
	if len(created.Hops) != 2 || created.Hops[0].ServiceID != notion.ID || created.Hops[1].ServiceID != notion.ID {
		t.Fatalf("repeated hops not preserved: %#v", created.Hops)
	}

	replacement := QueryPathConfigInput{
		BrainID: source.ID, Path: created.QueryPath.Path,
		Visibility: domain.VisibilityPrivate, State: domain.QueryPathStateEnabled,
		Operations: []domain.Operation{domain.OperationRawRead}, AssetIDs: []string{asset.ID},
		AllowedBrainIDs: []string{destination.ID}, AllowedServiceIDs: []string{notion.ID, obsidian.ID},
		Route: &RouteInput{TerminalMode: domain.TerminalModeFixed,
			DestinationBrainID: &destination.ID, ServiceIDs: []string{notion.ID, obsidian.ID, notion.ID}},
	}
	updated, err := db.ReplaceQueryPath(ctx, created.QueryPath.ID, created.QueryPath.ConfigVersion, replacement)
	if err != nil {
		t.Fatalf("replace query path config: %v", err)
	}
	if updated.QueryPath.ConfigVersion != created.QueryPath.ConfigVersion+1 || len(updated.Hops) != 3 {
		t.Fatalf("unexpected replacement: version=%d hops=%#v", updated.QueryPath.ConfigVersion, updated.Hops)
	}
	_, err = db.ReplaceQueryPath(ctx, created.QueryPath.ID, created.QueryPath.ConfigVersion, replacement)
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale version error = %v, want ErrVersionConflict", err)
	}
	reloaded, err := db.LoadQueryPathConfig(ctx, created.QueryPath.ID)
	if err != nil {
		t.Fatalf("reload query path config: %v", err)
	}
	if reloaded.QueryPath.ConfigVersion != updated.QueryPath.ConfigVersion || len(reloaded.Hops) != 3 {
		t.Fatalf("stale update mutated config: version=%d hops=%#v", reloaded.QueryPath.ConfigVersion, reloaded.Hops)
	}
}
