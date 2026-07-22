// Package store contains the application's explicit PostgreSQL persistence layer.
// It intentionally contains no authorization or other business policy.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const SchemaVersion = "1"

var (
	ErrVersionConflict     = errors.New("store: config version conflict")
	ErrTransactionRequired = errors.New("store: transaction required")
	ErrIdempotencyExists   = errors.New("store: idempotency record exists")
)

type dbTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Store is safe for concurrent use when constructed with New. A Store passed to
// an InTx callback is bound to that transaction and must not escape the callback.
type Store struct {
	pool *pgxpool.Pool
	db   dbTX
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, db: pool}
}

func (s *Store) Ping(ctx context.Context) error {
	if s == nil || s.pool == nil {
		return errors.New("store ping: nil database pool")
	}
	if err := s.pool.Ping(ctx); err != nil {
		return fmt.Errorf("store ping: %w", err)
	}
	return nil
}

func (s *Store) CheckSchemaVersion(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("check schema version: nil store")
	}
	var version string
	if err := s.db.QueryRow(ctx, `
		select value
		from public.app_meta
		where key = 'schema_version'
	`).Scan(&version); err != nil {
		return fmt.Errorf("check schema version: %w", err)
	}
	if version != SchemaVersion {
		return fmt.Errorf("check schema version: got %q, want %q", version, SchemaVersion)
	}
	return nil
}

// InTx executes fn in a PostgreSQL transaction. Returning an error rolls the
// transaction back. Commit failures are returned to the caller and are never
// retried because their outcome may be ambiguous.
func (s *Store) InTx(ctx context.Context, fn func(*Store) error) (err error) {
	if s == nil || s.pool == nil {
		return errors.New("store transaction: nil database pool")
	}
	if fn == nil {
		return errors.New("store transaction: nil callback")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin store transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = fn(&Store{pool: s.pool, db: tx}); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit store transaction: %w", err)
	}
	return nil
}

func (s *Store) withinTx(ctx context.Context, fn func(*Store) error) error {
	if _, ok := s.db.(pgx.Tx); ok {
		return fn(s)
	}
	return s.InTx(ctx, fn)
}

func affectedOne(tag pgconn.CommandTag, op string) error {
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%s: expected one affected row, got %d", op, tag.RowsAffected())
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
