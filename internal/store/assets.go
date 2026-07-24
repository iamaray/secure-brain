package store

import (
	"context"
	"fmt"

	"secure-brain/internal/domain"
)

func scanAsset(row interface{ Scan(...any) error }) (domain.Asset, error) {
	var asset domain.Asset
	var sha256 *string
	err := row.Scan(&asset.ID, &asset.BrainID, &asset.ObjectKey, &asset.StoragePath,
		&asset.OriginalFilename, &asset.MediaType, &asset.ByteSize, &sha256,
		&asset.Format, &asset.ProcessingState, &asset.ParseError,
		&asset.CreatedAt, &asset.UpdatedAt)
	if sha256 != nil {
		asset.SHA256 = *sha256
	}
	return asset, err
}

func (s *Store) InsertAsset(ctx context.Context, input AssetInput) (domain.Asset, error) {
	asset, err := scanAsset(s.db.QueryRow(ctx, `
		insert into public.assets (
			id, brain_id, object_key, storage_path, original_filename, media_type,
			byte_size, sha256, format, processing_state, parse_error
		) values (
			coalesce($1::uuid, gen_random_uuid()), $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11
		)
		returning id, brain_id, object_key, storage_path, original_filename, media_type,
		          byte_size, sha256, format, processing_state, parse_error, created_at, updated_at
	`, nilIfEmpty(input.ID), input.BrainID, input.ObjectKey, input.StoragePath,
		input.OriginalFilename, input.MediaType, input.ByteSize, input.SHA256,
		input.Format, input.ProcessingState, input.ParseError))
	if err != nil {
		return domain.Asset{}, fmt.Errorf("insert asset: %w", err)
	}
	return asset, nil
}

// UpdateAsset atomically replaces the mutable metadata of one existing asset.
// The ID and Brain ID are used only to identify the row and cannot be changed.
func (s *Store) UpdateAsset(ctx context.Context, input AssetInput) (domain.Asset, error) {
	asset, err := scanAsset(s.db.QueryRow(ctx, `
		update public.assets
		set object_key = $3,
		    storage_path = $4,
		    original_filename = $5,
		    media_type = $6,
		    byte_size = $7,
		    sha256 = $8,
		    format = $9,
		    processing_state = $10,
		    parse_error = $11
		where id = $1 and brain_id = $2
		returning id, brain_id, object_key, storage_path, original_filename, media_type,
		          byte_size, sha256, format, processing_state, parse_error, created_at, updated_at
	`, input.ID, input.BrainID, input.ObjectKey, input.StoragePath,
		input.OriginalFilename, input.MediaType, input.ByteSize, input.SHA256,
		input.Format, input.ProcessingState, input.ParseError))
	if err != nil {
		return domain.Asset{}, fmt.Errorf("update asset: %w", err)
	}
	return asset, nil
}

func (s *Store) GetAsset(ctx context.Context, id domain.RecordID) (domain.Asset, error) {
	asset, err := scanAsset(s.db.QueryRow(ctx, `
		select id, brain_id, object_key, storage_path, original_filename, media_type,
		       byte_size, sha256, format, processing_state, parse_error, created_at, updated_at
		from public.assets where id = $1
	`, id))
	if err != nil {
		return domain.Asset{}, fmt.Errorf("get asset: %w", err)
	}
	return asset, nil
}

func (s *Store) GetAssetInBrain(ctx context.Context, brainID, assetID domain.RecordID) (domain.Asset, error) {
	asset, err := scanAsset(s.db.QueryRow(ctx, `
		select id, brain_id, object_key, storage_path, original_filename, media_type,
		       byte_size, sha256, format, processing_state, parse_error, created_at, updated_at
		from public.assets where id = $2 and brain_id = $1
	`, brainID, assetID))
	if err != nil {
		return domain.Asset{}, fmt.Errorf("get asset in brain: %w", err)
	}
	return asset, nil
}

func (s *Store) GetAssetByObjectKey(ctx context.Context, brainID domain.RecordID, objectKey domain.ObjectKey) (domain.Asset, error) {
	asset, err := scanAsset(s.db.QueryRow(ctx, `
		select id, brain_id, object_key, storage_path, original_filename, media_type,
		       byte_size, sha256, format, processing_state, parse_error, created_at, updated_at
		from public.assets where brain_id = $1 and object_key = $2
	`, brainID, objectKey))
	if err != nil {
		return domain.Asset{}, fmt.Errorf("get asset by object key: %w", err)
	}
	return asset, nil
}

func (s *Store) ListAssets(ctx context.Context, brainID domain.RecordID, limit int) ([]domain.Asset, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		select id, brain_id, object_key, storage_path, original_filename, media_type,
		       byte_size, sha256, format, processing_state, parse_error, created_at, updated_at
		from public.assets
		where brain_id = $1
		order by created_at desc, id desc
		limit $2
	`, brainID, limit)
	if err != nil {
		return nil, fmt.Errorf("list assets: %w", err)
	}
	defer rows.Close()
	assets := make([]domain.Asset, 0)
	for rows.Next() {
		asset, err := scanAsset(rows)
		if err != nil {
			return nil, fmt.Errorf("list assets scan: %w", err)
		}
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list assets rows: %w", err)
	}
	return assets, nil
}

func (s *Store) DeleteAsset(ctx context.Context, brainID, assetID domain.RecordID) (bool, error) {
	tag, err := s.db.Exec(ctx, `delete from public.assets where id = $2 and brain_id = $1`, brainID, assetID)
	if err != nil {
		return false, fmt.Errorf("delete asset: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) AssetReferencedByEnabledPath(ctx context.Context, assetID domain.RecordID) (bool, error) {
	var inUse bool
	err := s.db.QueryRow(ctx, `
		select exists (
			select 1
			from public.query_path_assets qpa
			join public.query_paths qp on qp.id = qpa.query_path_id
			where qpa.asset_id = $1 and qp.state = 'enabled'
		)
	`, assetID).Scan(&inUse)
	if err != nil {
		return false, fmt.Errorf("check enabled asset reference: %w", err)
	}
	return inUse, nil
}

func nilIfEmpty(value domain.RecordID) any {
	if value == "" {
		return nil
	}
	return value
}
