package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"secure-brain/internal/domain"
)

func scanTransfer(row interface{ Scan(...any) error }) (domain.Transfer, error) {
	var transfer domain.Transfer
	err := row.Scan(
		&transfer.ID, &transfer.ExecutionID, &transfer.SourceBrainID,
		&transfer.DestinationBrainID, &transfer.SourceCanonicalID,
		&transfer.DestinationCanonicalID, &transfer.Status, &transfer.StoragePath,
		&transfer.SuggestedObjectKey, &transfer.SuggestedFilename, &transfer.MediaType,
		&transfer.ByteSize, &transfer.SHA256, &transfer.AcceptedAssetID,
		&transfer.CreatedAt, &transfer.ExpiresAt, &transfer.ResolvedAt,
	)
	return transfer, err
}

const transferColumns = `
	id, execution_id, source_brain_id, destination_brain_id, source_canonical_id,
	destination_canonical_id, status, storage_path, suggested_object_key,
	suggested_filename, media_type, byte_size, sha256, accepted_asset_id,
	created_at, expires_at, resolved_at`

func (s *Store) InsertTransfer(ctx context.Context, input TransferInput) (domain.Transfer, error) {
	transfer, err := scanTransfer(s.db.QueryRow(ctx, `
		insert into public.transfers (
			id, execution_id, source_brain_id, destination_brain_id,
			source_canonical_id, destination_canonical_id, storage_path,
			suggested_object_key, suggested_filename, media_type, byte_size, sha256, expires_at
		) values (
			coalesce($1::uuid, gen_random_uuid()), $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12, $13
		)
		returning `+transferColumns+`
	`, nilIfEmpty(input.ID), input.ExecutionID, input.SourceBrainID,
		input.DestinationBrainID, input.SourceCanonicalID, input.DestinationCanonicalID,
		input.StoragePath, input.SuggestedObjectKey, input.SuggestedFilename,
		input.MediaType, input.ByteSize, input.SHA256, input.ExpiresAt.UTC()))
	if err != nil {
		return domain.Transfer{}, fmt.Errorf("insert transfer: %w", err)
	}
	return transfer, nil
}

func (s *Store) GetTransfer(ctx context.Context, transferID domain.RecordID) (domain.Transfer, error) {
	transfer, err := scanTransfer(s.db.QueryRow(ctx, `select `+transferColumns+` from public.transfers where id = $1`, transferID))
	if err != nil {
		return domain.Transfer{}, fmt.Errorf("get transfer: %w", err)
	}
	return transfer, nil
}

func (s *Store) ListTransfers(ctx context.Context, filter TransferFilter) ([]domain.Transfer, error) {
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	var query string
	switch filter.Direction {
	case "incoming":
		query = `select ` + transferColumns + ` from public.transfers
			where destination_brain_id = $1
			  and ($2::text = '' or status = $2)
			  and ($3::timestamptz is null or created_at < $3)
			order by created_at desc, id desc limit $4`
	case "outgoing":
		query = `select ` + transferColumns + ` from public.transfers
			where source_brain_id = $1
			  and ($2::text = '' or status = $2)
			  and ($3::timestamptz is null or created_at < $3)
			order by created_at desc, id desc limit $4`
	default:
		return nil, fmt.Errorf("list transfers: unsupported direction %q", filter.Direction)
	}
	rows, err := s.db.Query(ctx, query, filter.BrainID, string(filter.Status), filter.Before, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list transfers: %w", err)
	}
	defer rows.Close()
	transfers := make([]domain.Transfer, 0)
	for rows.Next() {
		transfer, err := scanTransfer(rows)
		if err != nil {
			return nil, fmt.Errorf("list transfers scan: %w", err)
		}
		transfers = append(transfers, transfer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list transfers rows: %w", err)
	}
	return transfers, nil
}

// LockTransfer must be called on the Store supplied to an InTx callback.
func (s *Store) LockTransfer(ctx context.Context, transferID domain.RecordID) (domain.Transfer, error) {
	if _, ok := s.db.(pgx.Tx); !ok {
		return domain.Transfer{}, ErrTransactionRequired
	}
	transfer, err := scanTransfer(s.db.QueryRow(ctx, `
		select `+transferColumns+` from public.transfers where id = $1 for update
	`, transferID))
	if err != nil {
		return domain.Transfer{}, fmt.Errorf("lock transfer: %w", err)
	}
	return transfer, nil
}

func (s *Store) MarkTransferAccepted(ctx context.Context, transferID, acceptedAssetID domain.RecordID, resolvedAt time.Time) (domain.Transfer, error) {
	transfer, err := scanTransfer(s.db.QueryRow(ctx, `
		update public.transfers
		set status = 'accepted', accepted_asset_id = $2, resolved_at = $3
		where id = $1 and status = 'pending'
		returning `+transferColumns+`
	`, transferID, acceptedAssetID, resolvedAt.UTC()))
	if err != nil {
		return domain.Transfer{}, fmt.Errorf("mark transfer accepted: %w", err)
	}
	return transfer, nil
}

func (s *Store) MarkTransferRejected(ctx context.Context, transferID domain.RecordID, resolvedAt time.Time) (domain.Transfer, error) {
	return s.markTransferTerminal(ctx, transferID, domain.TransferStatusRejected, resolvedAt)
}

func (s *Store) MarkTransferExpired(ctx context.Context, transferID domain.RecordID, resolvedAt time.Time) (domain.Transfer, error) {
	return s.markTransferTerminal(ctx, transferID, domain.TransferStatusExpired, resolvedAt)
}

func (s *Store) markTransferTerminal(ctx context.Context, transferID domain.RecordID, status domain.TransferStatus, resolvedAt time.Time) (domain.Transfer, error) {
	transfer, err := scanTransfer(s.db.QueryRow(ctx, `
		update public.transfers
		set status = $2, resolved_at = $3
		where id = $1 and status = 'pending'
		returning `+transferColumns+`
	`, transferID, status, resolvedAt.UTC()))
	if err != nil {
		return domain.Transfer{}, fmt.Errorf("mark transfer %s: %w", status, err)
	}
	return transfer, nil
}

// ExpirePendingTransfers performs the lazy pending-to-expired transition and
// returns every changed row so the application can create viewer-scoped audits.
func (s *Store) ExpirePendingTransfers(ctx context.Context, now time.Time) ([]domain.Transfer, error) {
	rows, err := s.db.Query(ctx, `
		update public.transfers
		set status = 'expired', resolved_at = $1
		where status = 'pending' and expires_at <= $1
		returning `+transferColumns+`
	`, now.UTC())
	if err != nil {
		return nil, fmt.Errorf("expire pending transfers: %w", err)
	}
	defer rows.Close()
	transfers := make([]domain.Transfer, 0)
	for rows.Next() {
		transfer, err := scanTransfer(rows)
		if err != nil {
			return nil, fmt.Errorf("expire pending transfers scan: %w", err)
		}
		transfers = append(transfers, transfer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("expire pending transfers rows: %w", err)
	}
	return transfers, nil
}

func (s *Store) GetIdempotencyRecord(ctx context.Context, userID domain.RecordID, scope string, key domain.IdempotencyKey) (IdempotencyRecord, error) {
	record, err := scanIdempotency(s.db.QueryRow(ctx, `
		select id, user_id, scope, idempotency_key, request_hash,
		       response_status, response_body, created_at, expires_at
		from public.idempotency_records
		where user_id = $1 and scope = $2 and idempotency_key = $3
		  and expires_at > now()
	`, userID, scope, key))
	if err != nil {
		return IdempotencyRecord{}, fmt.Errorf("get idempotency record: %w", err)
	}
	return record, nil
}

func scanIdempotency(row interface{ Scan(...any) error }) (IdempotencyRecord, error) {
	var record IdempotencyRecord
	err := row.Scan(&record.ID, &record.UserID, &record.Scope, &record.IdempotencyKey,
		&record.RequestHash, &record.ResponseStatus, &record.ResponseBody,
		&record.CreatedAt, &record.ExpiresAt)
	return record, err
}

func (s *Store) CreateIdempotencyRecord(ctx context.Context, userID domain.RecordID, scope string, key domain.IdempotencyKey, requestHash string, expiresAt time.Time) (IdempotencyRecord, error) {
	record, err := scanIdempotency(s.db.QueryRow(ctx, `
		insert into public.idempotency_records (
			user_id, scope, idempotency_key, request_hash, expires_at
		) values ($1, $2, $3, $4, $5)
		on conflict (user_id, scope, idempotency_key) do update
		set request_hash = excluded.request_hash,
		    response_status = null,
		    response_body = null,
		    created_at = now(),
		    expires_at = excluded.expires_at
		where idempotency_records.expires_at <= now()
		returning id, user_id, scope, idempotency_key, request_hash,
		          response_status, response_body, created_at, expires_at
	`, userID, scope, key, requestHash, expiresAt.UTC()))
	if errors.Is(err, pgx.ErrNoRows) {
		return IdempotencyRecord{}, ErrIdempotencyExists
	}
	if err != nil {
		return IdempotencyRecord{}, fmt.Errorf("create idempotency record: %w", err)
	}
	return record, nil
}

func (s *Store) CompleteIdempotencyRecord(ctx context.Context, id domain.RecordID, responseStatus int, responseBody []byte) (IdempotencyRecord, error) {
	record, err := scanIdempotency(s.db.QueryRow(ctx, `
		update public.idempotency_records
		set response_status = $2, response_body = $3
		where id = $1 and response_status is null
		returning id, user_id, scope, idempotency_key, request_hash,
		          response_status, response_body, created_at, expires_at
	`, id, responseStatus, responseBody))
	if err != nil {
		return IdempotencyRecord{}, fmt.Errorf("complete idempotency record: %w", err)
	}
	return record, nil
}
