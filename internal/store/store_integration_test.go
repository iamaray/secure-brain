package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"secure-brain/internal/domain"
)

var disposableDatabase struct {
	once     sync.Once
	pool     *pgxpool.Pool
	admin    *pgx.Conn
	name     string
	created  bool
	initErr  error
	cleanErr error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if disposableDatabase.pool != nil {
		disposableDatabase.pool.Close()
	}
	if disposableDatabase.admin != nil {
		if disposableDatabase.created {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, _ = disposableDatabase.admin.Exec(ctx, `
				select pg_terminate_backend(pid)
				from pg_stat_activity
				where datname = $1 and pid <> pg_backend_pid()
			`, disposableDatabase.name)
			_, disposableDatabase.cleanErr = disposableDatabase.admin.Exec(
				ctx, `drop database `+quoteIdentifier(disposableDatabase.name),
			)
			cancel()
		}
		_ = disposableDatabase.admin.Close(context.Background())
	}
	if disposableDatabase.cleanErr != nil {
		fmt.Fprintf(os.Stderr, "drop disposable integration database: %v\n", disposableDatabase.cleanErr)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func integrationStore(t *testing.T) (*Store, func()) {
	t.Helper()
	if os.Getenv("DATABASE_TEST_URL") == "" {
		t.Skip("DATABASE_TEST_URL is not set; skipping PostgreSQL store integration test")
	}
	disposableDatabase.once.Do(initDisposableDatabase)
	if disposableDatabase.initErr != nil {
		t.Fatalf("initialize disposable integration database: %v", disposableDatabase.initErr)
	}
	ctx := context.Background()
	tx, err := disposableDatabase.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin integration transaction: %v", err)
	}
	return &Store{pool: disposableDatabase.pool, db: tx}, func() {
		_ = tx.Rollback(ctx)
	}
}

func initDisposableDatabase() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	adminConfig, err := pgx.ParseConfig(os.Getenv("DATABASE_TEST_URL"))
	if err != nil {
		disposableDatabase.initErr = fmt.Errorf("parse DATABASE_TEST_URL: %w", err)
		return
	}
	admin, err := pgx.ConnectConfig(ctx, adminConfig)
	if err != nil {
		disposableDatabase.initErr = fmt.Errorf("connect DATABASE_TEST_URL: %w", err)
		return
	}
	disposableDatabase.admin = admin
	disposableDatabase.name = fmt.Sprintf("securebrain_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := admin.Exec(ctx, `create database `+quoteIdentifier(disposableDatabase.name)); err != nil {
		disposableDatabase.initErr = fmt.Errorf("create database (the DATABASE_TEST_URL user needs CREATEDB): %w", err)
		return
	}
	disposableDatabase.created = true

	poolConfig, err := pgxpool.ParseConfig(os.Getenv("DATABASE_TEST_URL"))
	if err != nil {
		disposableDatabase.initErr = fmt.Errorf("parse disposable database config: %w", err)
		return
	}
	poolConfig.ConnConfig.Database = disposableDatabase.name
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		disposableDatabase.initErr = fmt.Errorf("connect disposable database: %w", err)
		return
	}
	disposableDatabase.pool = pool
	if err := installSchema(ctx, pool); err != nil {
		disposableDatabase.initErr = err
		return
	}
	if err := New(pool).CheckSchemaVersion(ctx); err != nil {
		disposableDatabase.initErr = err
	}
}

func installSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return errors.New("locate store integration test")
	}
	schemaPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "db", "schema.sql")
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("read db/schema.sql: %w", err)
	}
	// Supabase owns these deployment objects. The disposable PostgreSQL database
	// supplies the smallest compatible bucket table and omits grants to roles
	// which do not exist in a stock local PostgreSQL installation.
	if _, err := pool.Exec(ctx, `
		create schema storage;
		create table storage.buckets (
			id text primary key,
			name text not null,
			public boolean not null,
			file_size_limit bigint,
			allowed_mime_types text[]
		)
	`); err != nil {
		return fmt.Errorf("prepare storage schema: %w", err)
	}
	sql := strings.ReplaceAll(string(schema),
		"revoke all on all tables in schema public from anon, authenticated;", "")
	sql = strings.ReplaceAll(sql,
		"revoke all on all sequences in schema public from anon, authenticated;", "")
	if _, err := pool.Exec(ctx, sql); err != nil {
		return fmt.Errorf("execute db/schema.sql: %w", err)
	}
	return nil
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
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
