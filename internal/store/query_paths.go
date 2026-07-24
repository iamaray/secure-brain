package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"secure-brain/internal/application"
	"secure-brain/internal/domain"
)

func scanQueryPath(row interface{ Scan(...any) error }) (domain.QueryPath, error) {
	var path domain.QueryPath
	var operations []string
	err := row.Scan(&path.ID, &path.BrainID, &path.Path, &path.Visibility, &path.State,
		&operations, &path.ConfigVersion, &path.CreatedAt, &path.UpdatedAt)
	path.Operations = make([]domain.Operation, len(operations))
	for i := range operations {
		operation, parseErr := domain.ParseOperation(operations[i])
		if parseErr != nil {
			return domain.QueryPath{}, fmt.Errorf("scan query path operation: %w", parseErr)
		}
		path.Operations[i] = operation
	}
	return path, err
}

func scanRoute(row interface{ Scan(...any) error }) (domain.Route, error) {
	var route domain.Route
	err := row.Scan(&route.ID, &route.QueryPathID, &route.TerminalMode,
		&route.DestinationBrainID, &route.CreatedAt, &route.UpdatedAt)
	return route, err
}

func operationStrings(values []domain.Operation) []string {
	result := make([]string, len(values))
	for i := range values {
		result[i] = string(values[i])
	}
	return result
}

// CreateQueryPath persists the full scope, grants, optional route and ordered
// repeated hops in one transaction.
func (s *Store) CreateQueryPath(ctx context.Context, input application.QueryPathCommand) (application.QueryPathSnapshot, error) {
	var result application.QueryPathSnapshot
	err := s.withinTx(ctx, func(tx *Store) error {
		path, err := scanQueryPath(tx.db.QueryRow(ctx, `
			insert into public.query_paths (brain_id, path, visibility, state, operations)
			values ($1, $2, $3, $4, $5)
			returning id, brain_id, path, visibility, state, operations,
			          config_version, created_at, updated_at
		`, input.BrainID, input.Path, input.Visibility, input.State, operationStrings(input.Operations)))
		if err != nil {
			return fmt.Errorf("create query path: %w", err)
		}
		if err := tx.insertQueryPathRelations(ctx, path.ID, input); err != nil {
			return err
		}
		result, err = tx.LoadQueryPathConfig(ctx, path.ID)
		return err
	})
	return result, err
}

// ReplaceQueryPath replaces the complete persisted configuration and increments
// config_version only if expectedVersion matches.
func (s *Store) ReplaceQueryPath(ctx context.Context, id domain.RecordID, expectedVersion int64, input application.QueryPathCommand) (application.QueryPathSnapshot, error) {
	var result application.QueryPathSnapshot
	err := s.withinTx(ctx, func(tx *Store) error {
		_, err := scanQueryPath(tx.db.QueryRow(ctx, `
			update public.query_paths
			set path = $4, visibility = $5, state = $6, operations = $7,
			    config_version = config_version + 1
			where id = $1 and brain_id = $2 and config_version = $3
			returning id, brain_id, path, visibility, state, operations,
			          config_version, created_at, updated_at
		`, id, input.BrainID, expectedVersion, input.Path, input.Visibility,
			input.State, operationStrings(input.Operations)))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrVersionConflict
		}
		if err != nil {
			return fmt.Errorf("replace query path: %w", err)
		}
		if _, err := tx.db.Exec(ctx, `delete from public.query_path_assets where query_path_id = $1`, id); err != nil {
			return fmt.Errorf("replace query path assets: %w", err)
		}
		if _, err := tx.db.Exec(ctx, `delete from public.query_path_brain_grants where query_path_id = $1`, id); err != nil {
			return fmt.Errorf("replace query path brain grants: %w", err)
		}
		if _, err := tx.db.Exec(ctx, `delete from public.query_path_service_grants where query_path_id = $1`, id); err != nil {
			return fmt.Errorf("replace query path service grants: %w", err)
		}
		if _, err := tx.db.Exec(ctx, `delete from public.routes where query_path_id = $1`, id); err != nil {
			return fmt.Errorf("replace query path route: %w", err)
		}
		if err := tx.insertQueryPathRelations(ctx, id, input); err != nil {
			return err
		}
		result, err = tx.LoadQueryPathConfig(ctx, id)
		return err
	})
	return result, err
}

func (s *Store) insertQueryPathRelations(ctx context.Context, queryPathID domain.RecordID, input application.QueryPathCommand) error {
	for position, assetID := range input.AssetIDs {
		if _, err := s.db.Exec(ctx, `
			insert into public.query_path_assets (query_path_id, asset_id, position)
			values ($1, $2, $3)
		`, queryPathID, assetID, position); err != nil {
			return fmt.Errorf("insert query path asset: %w", err)
		}
	}
	for _, brainID := range input.AllowedBrainIDs {
		if _, err := s.db.Exec(ctx, `
			insert into public.query_path_brain_grants (query_path_id, brain_id)
			values ($1, $2)
		`, queryPathID, brainID); err != nil {
			return fmt.Errorf("insert query path brain grant: %w", err)
		}
	}
	for _, serviceID := range input.AllowedServiceIDs {
		if _, err := s.db.Exec(ctx, `
			insert into public.query_path_service_grants (query_path_id, service_id)
			values ($1, $2)
		`, queryPathID, serviceID); err != nil {
			return fmt.Errorf("insert query path service grant: %w", err)
		}
	}
	if input.Route == nil {
		return nil
	}
	var routeID domain.RecordID
	if err := s.db.QueryRow(ctx, `
		insert into public.routes (query_path_id, terminal_mode, destination_brain_id)
		values ($1, $2, $3)
		returning id
	`, queryPathID, input.Route.TerminalMode, input.Route.DestinationBrainID).Scan(&routeID); err != nil {
		return fmt.Errorf("insert query path route: %w", err)
	}
	for hopIndex, serviceID := range input.Route.ServiceIDs {
		if _, err := s.db.Exec(ctx, `
			insert into public.route_hops (route_id, hop_index, service_id)
			values ($1, $2, $3)
		`, routeID, hopIndex, serviceID); err != nil {
			return fmt.Errorf("insert query path route hop: %w", err)
		}
	}
	return nil
}

func (s *Store) LoadQueryPathConfig(ctx context.Context, queryPathID domain.RecordID) (application.QueryPathSnapshot, error) {
	path, err := scanQueryPath(s.db.QueryRow(ctx, `
		select id, brain_id, path, visibility, state, operations,
		       config_version, created_at, updated_at
		from public.query_paths where id = $1
	`, queryPathID))
	if err != nil {
		return application.QueryPathSnapshot{}, fmt.Errorf("load query path: %w", err)
	}
	config := application.QueryPathSnapshot{
		QueryPath: path,
		Assets:    []domain.Asset{},
		Policy: application.PolicySnapshot{
			AllowedBrains:   []domain.Brain{},
			AllowedServices: []domain.Service{},
		},
	}

	assetRows, err := s.db.Query(ctx, `
		select a.id, a.brain_id, a.object_key, a.storage_path, a.original_filename, a.media_type,
		       a.byte_size, a.sha256, a.format, a.processing_state, a.parse_error, a.created_at, a.updated_at
		from public.query_path_assets qpa
		join public.assets a on a.id = qpa.asset_id
		where qpa.query_path_id = $1
		order by qpa.position
	`, queryPathID)
	if err != nil {
		return application.QueryPathSnapshot{}, fmt.Errorf("load query path assets: %w", err)
	}
	for assetRows.Next() {
		asset, scanErr := scanAsset(assetRows)
		if scanErr != nil {
			assetRows.Close()
			return application.QueryPathSnapshot{}, fmt.Errorf("load query path asset scan: %w", scanErr)
		}
		config.Assets = append(config.Assets, asset)
	}
	err = assetRows.Err()
	assetRows.Close()
	if err != nil {
		return application.QueryPathSnapshot{}, fmt.Errorf("load query path asset rows: %w", err)
	}

	brainRows, err := s.db.Query(ctx, `
		select b.id, b.owner_user_id, b.slug, b.canonical_id, b.display_name, b.status, b.created_at, b.updated_at
		from public.query_path_brain_grants qpg
		join public.brains b on b.id = qpg.brain_id
		where qpg.query_path_id = $1
		order by b.canonical_id
	`, queryPathID)
	if err != nil {
		return application.QueryPathSnapshot{}, fmt.Errorf("load query path brain grants: %w", err)
	}
	for brainRows.Next() {
		brain, scanErr := scanBrain(brainRows)
		if scanErr != nil {
			brainRows.Close()
			return application.QueryPathSnapshot{}, fmt.Errorf("load query path brain grant scan: %w", scanErr)
		}
		config.Policy.AllowedBrains = append(config.Policy.AllowedBrains, brain)
	}
	err = brainRows.Err()
	brainRows.Close()
	if err != nil {
		return application.QueryPathSnapshot{}, fmt.Errorf("load query path brain grant rows: %w", err)
	}

	serviceRows, err := s.db.Query(ctx, `
		select sv.id, sv.owner_user_id, sv.slug, sv.canonical_id, sv.display_name, sv.status,
		       sv.capability_tags, sv.created_at, sv.updated_at
		from public.query_path_service_grants qpg
		join public.services sv on sv.id = qpg.service_id
		where qpg.query_path_id = $1
		order by sv.canonical_id
	`, queryPathID)
	if err != nil {
		return application.QueryPathSnapshot{}, fmt.Errorf("load query path service grants: %w", err)
	}
	for serviceRows.Next() {
		service, scanErr := scanService(serviceRows)
		if scanErr != nil {
			serviceRows.Close()
			return application.QueryPathSnapshot{}, fmt.Errorf("load query path service grant scan: %w", scanErr)
		}
		config.Policy.AllowedServices = append(config.Policy.AllowedServices, service)
	}
	err = serviceRows.Err()
	serviceRows.Close()
	if err != nil {
		return application.QueryPathSnapshot{}, fmt.Errorf("load query path service grant rows: %w", err)
	}

	route, err := scanRoute(s.db.QueryRow(ctx, `
		select id, query_path_id, terminal_mode, destination_brain_id, created_at, updated_at
		from public.routes where query_path_id = $1
	`, queryPathID))
	if errors.Is(err, pgx.ErrNoRows) {
		return config, nil
	}
	if err != nil {
		return application.QueryPathSnapshot{}, fmt.Errorf("load query path route: %w", err)
	}
	config.Route = &application.ConfiguredRouteSnapshot{
		Route: route,
		Hops:  []domain.RouteHop{},
	}
	hopRows, err := s.db.Query(ctx, `
		select route_id, hop_index, service_id
		from public.route_hops where route_id = $1
		order by hop_index
	`, route.ID)
	if err != nil {
		return application.QueryPathSnapshot{}, fmt.Errorf("load query path hops: %w", err)
	}
	defer hopRows.Close()
	for hopRows.Next() {
		var hop domain.RouteHop
		if err := hopRows.Scan(&hop.RouteID, &hop.HopIndex, &hop.ServiceID); err != nil {
			return application.QueryPathSnapshot{}, fmt.Errorf("load query path hop scan: %w", err)
		}
		config.Route.Hops = append(config.Route.Hops, hop)
	}
	if err := hopRows.Err(); err != nil {
		return application.QueryPathSnapshot{}, fmt.Errorf("load query path hop rows: %w", err)
	}
	return config, nil
}

func (s *Store) ResolveEnabledQueryPath(ctx context.Context, sourceCanonicalID domain.BrainID, path domain.QueryPathValue) (application.QueryPathSnapshot, error) {
	var id domain.RecordID
	err := s.db.QueryRow(ctx, `
		select qp.id
		from public.query_paths qp
		join public.brains b on b.id = qp.brain_id
		where b.canonical_id = $1 and qp.path = $2 and qp.state = 'enabled'
	`, sourceCanonicalID, path).Scan(&id)
	if err != nil {
		return application.QueryPathSnapshot{}, fmt.Errorf("resolve enabled query path: %w", err)
	}
	return s.LoadQueryPathConfig(ctx, id)
}

func (s *Store) ListQueryPaths(ctx context.Context, brainID domain.RecordID, limit int) ([]domain.QueryPath, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		select id, brain_id, path, visibility, state, operations,
		       config_version, created_at, updated_at
		from public.query_paths
		where brain_id = $1
		order by updated_at desc, id desc
		limit $2
	`, brainID, limit)
	if err != nil {
		return nil, fmt.Errorf("list query paths: %w", err)
	}
	defer rows.Close()
	paths := make([]domain.QueryPath, 0)
	for rows.Next() {
		path, err := scanQueryPath(rows)
		if err != nil {
			return nil, fmt.Errorf("list query paths scan: %w", err)
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list query paths rows: %w", err)
	}
	return paths, nil
}

func (s *Store) DeleteQueryPath(ctx context.Context, brainID, queryPathID domain.RecordID, expectedVersion int64) error {
	tag, err := s.db.Exec(ctx, `
		delete from public.query_paths
		where id = $1 and brain_id = $2 and config_version = $3
	`, queryPathID, brainID, expectedVersion)
	if err != nil {
		return fmt.Errorf("delete query path: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrVersionConflict
	}
	return nil
}
