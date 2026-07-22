package store

import (
	"context"
	"fmt"

	"secure-brain/internal/domain"
)

func scanBrain(row interface{ Scan(...any) error }) (domain.Brain, error) {
	var brain domain.Brain
	err := row.Scan(&brain.ID, &brain.OwnerUserID, &brain.Slug, &brain.CanonicalID,
		&brain.DisplayName, &brain.Status, &brain.CreatedAt, &brain.UpdatedAt)
	return brain, err
}

func scanService(row interface{ Scan(...any) error }) (domain.Service, error) {
	var service domain.Service
	err := row.Scan(&service.ID, &service.OwnerUserID, &service.Slug, &service.CanonicalID,
		&service.DisplayName, &service.Status, &service.CapabilityTags,
		&service.CreatedAt, &service.UpdatedAt)
	return service, err
}

func (s *Store) CreateBrain(ctx context.Context, ownerUserID, slug, displayName string) (domain.Brain, error) {
	brain, err := scanBrain(s.db.QueryRow(ctx, `
		insert into public.brains (owner_user_id, slug, display_name)
		values ($1, $2, $3)
		returning id, owner_user_id, slug, canonical_id, display_name, status, created_at, updated_at
	`, ownerUserID, slug, displayName))
	if err != nil {
		return domain.Brain{}, fmt.Errorf("create brain: %w", err)
	}
	return brain, nil
}

// ListBrains returns all Brains when ownerUserID is nil, or the owner's Brains otherwise.
func (s *Store) ListBrains(ctx context.Context, ownerUserID *string) ([]domain.Brain, error) {
	rows, err := s.db.Query(ctx, `
		select id, owner_user_id, slug, canonical_id, display_name, status, created_at, updated_at
		from public.brains
		where ($1::uuid is null or owner_user_id = $1)
		order by created_at desc, id desc
	`, ownerUserID)
	if err != nil {
		return nil, fmt.Errorf("list brains: %w", err)
	}
	defer rows.Close()
	brains := make([]domain.Brain, 0)
	for rows.Next() {
		brain, err := scanBrain(rows)
		if err != nil {
			return nil, fmt.Errorf("list brains scan: %w", err)
		}
		brains = append(brains, brain)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list brains rows: %w", err)
	}
	return brains, nil
}

func (s *Store) GetBrain(ctx context.Context, id string) (domain.Brain, error) {
	brain, err := scanBrain(s.db.QueryRow(ctx, `
		select id, owner_user_id, slug, canonical_id, display_name, status, created_at, updated_at
		from public.brains where id = $1
	`, id))
	if err != nil {
		return domain.Brain{}, fmt.Errorf("get brain: %w", err)
	}
	return brain, nil
}

func (s *Store) GetBrainByCanonicalID(ctx context.Context, canonicalID string) (domain.Brain, error) {
	brain, err := scanBrain(s.db.QueryRow(ctx, `
		select id, owner_user_id, slug, canonical_id, display_name, status, created_at, updated_at
		from public.brains where canonical_id = $1
	`, canonicalID))
	if err != nil {
		return domain.Brain{}, fmt.Errorf("get brain by canonical id: %w", err)
	}
	return brain, nil
}

func (s *Store) DeleteBrain(ctx context.Context, id string) (bool, error) {
	tag, err := s.db.Exec(ctx, `delete from public.brains where id = $1`, id)
	if err != nil {
		return false, fmt.Errorf("delete brain: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) CreateService(ctx context.Context, ownerUserID, slug, displayName string) (domain.Service, error) {
	service, err := scanService(s.db.QueryRow(ctx, `
		insert into public.services (owner_user_id, slug, display_name)
		values ($1, $2, $3)
		returning id, owner_user_id, slug, canonical_id, display_name, status, capability_tags, created_at, updated_at
	`, ownerUserID, slug, displayName))
	if err != nil {
		return domain.Service{}, fmt.Errorf("create service: %w", err)
	}
	return service, nil
}

func (s *Store) ListServices(ctx context.Context, ownerUserID *string) ([]domain.Service, error) {
	rows, err := s.db.Query(ctx, `
		select id, owner_user_id, slug, canonical_id, display_name, status, capability_tags, created_at, updated_at
		from public.services
		where ($1::uuid is null or owner_user_id = $1)
		order by created_at desc, id desc
	`, ownerUserID)
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	defer rows.Close()
	services := make([]domain.Service, 0)
	for rows.Next() {
		service, err := scanService(rows)
		if err != nil {
			return nil, fmt.Errorf("list services scan: %w", err)
		}
		services = append(services, service)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list services rows: %w", err)
	}
	return services, nil
}

func (s *Store) GetService(ctx context.Context, id string) (domain.Service, error) {
	service, err := scanService(s.db.QueryRow(ctx, `
		select id, owner_user_id, slug, canonical_id, display_name, status, capability_tags, created_at, updated_at
		from public.services where id = $1
	`, id))
	if err != nil {
		return domain.Service{}, fmt.Errorf("get service: %w", err)
	}
	return service, nil
}

func (s *Store) GetServiceByCanonicalID(ctx context.Context, canonicalID string) (domain.Service, error) {
	service, err := scanService(s.db.QueryRow(ctx, `
		select id, owner_user_id, slug, canonical_id, display_name, status, capability_tags, created_at, updated_at
		from public.services where canonical_id = $1
	`, canonicalID))
	if err != nil {
		return domain.Service{}, fmt.Errorf("get service by canonical id: %w", err)
	}
	return service, nil
}

func (s *Store) DeleteService(ctx context.Context, id string) (bool, error) {
	tag, err := s.db.Exec(ctx, `delete from public.services where id = $1`, id)
	if err != nil {
		return false, fmt.Errorf("delete service: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
