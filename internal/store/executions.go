package store

import (
	"context"
	"encoding/json"
	"fmt"

	"secure-brain/internal/domain"
)

func scanExecution(row interface{ Scan(...any) error }) (domain.RouteExecution, error) {
	var execution domain.RouteExecution
	var snapshot, result []byte
	var errorCode *string
	err := row.Scan(
		&execution.ID, &execution.Mode, &execution.QueryPathID, &execution.ActorUserID,
		&execution.InitiatingBrainID, &execution.SourceBrainID, &execution.DestinationBrainID,
		&execution.SourceCanonicalID, &execution.SourcePath, &execution.DestinationCanonicalID,
		&execution.Operation, &execution.State, &snapshot, &result, &errorCode,
		&execution.ErrorMessage, &execution.CreatedAt, &execution.StartedAt, &execution.CompletedAt,
	)
	if err != nil {
		return domain.RouteExecution{}, err
	}
	if err := json.Unmarshal(snapshot, &execution.RouteSnapshot); err != nil {
		return domain.RouteExecution{}, fmt.Errorf("decode execution route snapshot: %w", err)
	}
	if err := json.Unmarshal(result, &execution.ResultMetadata); err != nil {
		return domain.RouteExecution{}, fmt.Errorf("decode execution result metadata: %w", err)
	}
	if errorCode != nil {
		code := domain.Code(*errorCode)
		execution.ErrorCode = &code
	}
	return execution, nil
}

func (s *Store) InsertExecution(ctx context.Context, input ExecutionInput) (domain.RouteExecution, error) {
	snapshot := input.RouteSnapshot
	if len(snapshot) == 0 {
		snapshot = json.RawMessage(`{}`)
	}
	result := input.ResultMetadata
	if len(result) == 0 {
		result = json.RawMessage(`{}`)
	}
	execution, err := scanExecution(s.db.QueryRow(ctx, `
		insert into public.route_executions (
			id, mode, query_path_id, actor_user_id, initiating_brain_id,
			source_brain_id, destination_brain_id, source_canonical_id, source_path,
			destination_canonical_id, operation, state, route_snapshot, result_metadata
		) values (
			coalesce($1::uuid, gen_random_uuid()), $2, $3, $4, $5,
			$6, $7, $8, $9, $10, $11, $12, $13, $14
		)
		returning id, mode, query_path_id, actor_user_id, initiating_brain_id,
		          source_brain_id, destination_brain_id, source_canonical_id, source_path,
		          destination_canonical_id, operation, state, route_snapshot, result_metadata,
		          error_code, error_message, created_at, started_at, completed_at
	`, nilIfEmpty(input.ID), input.Mode, input.QueryPathID, input.ActorUserID,
		input.InitiatingBrainID, input.SourceBrainID, input.DestinationBrainID,
		input.SourceCanonicalID, input.SourcePath, input.DestinationCanonicalID,
		input.Operation, input.State, snapshot, result))
	if err != nil {
		return domain.RouteExecution{}, fmt.Errorf("insert execution: %w", err)
	}
	return execution, nil
}

func (s *Store) UpdateExecutionState(ctx context.Context, executionID string, update ExecutionUpdate) (domain.RouteExecution, error) {
	result := update.ResultMetadata
	if len(result) == 0 {
		result = json.RawMessage(`{}`)
	}
	execution, err := scanExecution(s.db.QueryRow(ctx, `
		update public.route_executions
		set state = $2,
		    result_metadata = $3,
		    error_code = $4,
		    error_message = $5,
		    started_at = coalesce($6, started_at),
		    completed_at = $7
		where id = $1
		returning id, mode, query_path_id, actor_user_id, initiating_brain_id,
		          source_brain_id, destination_brain_id, source_canonical_id, source_path,
		          destination_canonical_id, operation, state, route_snapshot, result_metadata,
		          error_code, error_message, created_at, started_at, completed_at
	`, executionID, update.State, result, codeValue(update.ErrorCode), update.ErrorMessage,
		update.StartedAt, update.CompletedAt))
	if err != nil {
		return domain.RouteExecution{}, fmt.Errorf("update execution state: %w", err)
	}
	return execution, nil
}

func (s *Store) GetExecution(ctx context.Context, executionID string) (domain.RouteExecution, error) {
	execution, err := scanExecution(s.db.QueryRow(ctx, `
		select id, mode, query_path_id, actor_user_id, initiating_brain_id,
		       source_brain_id, destination_brain_id, source_canonical_id, source_path,
		       destination_canonical_id, operation, state, route_snapshot, result_metadata,
		       error_code, error_message, created_at, started_at, completed_at
		from public.route_executions where id = $1
	`, executionID))
	if err != nil {
		return domain.RouteExecution{}, fmt.Errorf("get execution: %w", err)
	}
	return execution, nil
}

func scanExecutionHop(row interface{ Scan(...any) error }) (domain.ExecutionHop, error) {
	var hop domain.ExecutionHop
	var errorCode *string
	err := row.Scan(&hop.ID, &hop.ExecutionID, &hop.HopIndex, &hop.ServiceID,
		&hop.ServiceCanonicalID, &hop.Status, &hop.InputSHA256, &hop.OutputSHA256,
		&hop.DurationMS, &errorCode, &hop.CreatedAt)
	if errorCode != nil {
		code := domain.Code(*errorCode)
		hop.ErrorCode = &code
	}
	return hop, err
}

func (s *Store) InsertExecutionHop(ctx context.Context, input ExecutionHopInput) (domain.ExecutionHop, error) {
	hop, err := scanExecutionHop(s.db.QueryRow(ctx, `
		insert into public.execution_hops (
			id, execution_id, hop_index, service_id, service_canonical_id,
			status, input_sha256, output_sha256, duration_ms, error_code
		) values (coalesce($1::uuid, gen_random_uuid()), $2, $3, $4, $5, $6, $7, $8, $9, $10)
		returning id, execution_id, hop_index, service_id, service_canonical_id,
		          status, input_sha256, output_sha256, duration_ms, error_code, created_at
	`, nilIfEmpty(input.ID), input.ExecutionID, input.HopIndex, input.ServiceID,
		input.ServiceCanonicalID, input.Status, input.InputSHA256, input.OutputSHA256,
		input.DurationMS, codeValue(input.ErrorCode)))
	if err != nil {
		return domain.ExecutionHop{}, fmt.Errorf("insert execution hop: %w", err)
	}
	return hop, nil
}

func codeValue(code *domain.Code) any {
	if code == nil {
		return nil
	}
	return string(*code)
}

func (s *Store) ListExecutionHops(ctx context.Context, executionID string) ([]domain.ExecutionHop, error) {
	rows, err := s.db.Query(ctx, `
		select id, execution_id, hop_index, service_id, service_canonical_id,
		       status, input_sha256, output_sha256, duration_ms, error_code, created_at
		from public.execution_hops
		where execution_id = $1
		order by hop_index
	`, executionID)
	if err != nil {
		return nil, fmt.Errorf("list execution hops: %w", err)
	}
	defer rows.Close()
	hops := make([]domain.ExecutionHop, 0)
	for rows.Next() {
		hop, err := scanExecutionHop(rows)
		if err != nil {
			return nil, fmt.Errorf("list execution hops scan: %w", err)
		}
		hops = append(hops, hop)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list execution hops rows: %w", err)
	}
	return hops, nil
}
