package store

import (
	"context"
	"encoding/json"
	"fmt"

	"secure-brain/internal/application"
	"secure-brain/internal/domain"
)

func scanAuditEvent(row interface{ Scan(...any) error }) (domain.AuditEvent, error) {
	var event domain.AuditEvent
	var metadata []byte
	err := row.Scan(&event.ID, &event.EventType, &event.ActorUserID, &event.ResourceType,
		&event.ResourceID, &event.BrainID, &event.ServiceID, &event.ExecutionID,
		&event.Status, &metadata, &event.CreatedAt)
	if err != nil {
		return domain.AuditEvent{}, err
	}
	if err := json.Unmarshal(metadata, &event.Metadata); err != nil {
		return domain.AuditEvent{}, fmt.Errorf("decode audit metadata: %w", err)
	}
	return event, nil
}

// InsertAuditEvent inserts the event and its materialized viewers. Call it on a
// transaction-bound Store when it accompanies a business mutation.
func (s *Store) InsertAuditEvent(ctx context.Context, input application.AuditRecordCommand) (domain.AuditEvent, error) {
	metadata, err := json.Marshal(input.Metadata)
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("encode audit metadata: %w", err)
	}
	event, err := scanAuditEvent(s.db.QueryRow(ctx, `
		insert into public.audit_events (
			id, event_type, actor_user_id, resource_type, resource_id,
			brain_id, service_id, execution_id, status, metadata
		) values (
			coalesce($1::uuid, gen_random_uuid()), $2, $3, $4, $5, $6, $7, $8, $9, $10
		)
		returning id, event_type, actor_user_id, resource_type, resource_id,
		          brain_id, service_id, execution_id, status, metadata, created_at
	`, nilIfEmpty(input.ID), input.EventType, input.ActorUserID, input.ResourceType,
		input.ResourceID, input.BrainID, input.ServiceID, input.ExecutionID,
		input.Status, metadata))
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("insert audit event: %w", err)
	}
	for _, userID := range input.ViewerUserIDs {
		if _, err := s.db.Exec(ctx, `
			insert into public.audit_event_viewers (audit_event_id, user_id)
			values ($1, $2)
			on conflict (audit_event_id, user_id) do nothing
		`, event.ID, userID); err != nil {
			return domain.AuditEvent{}, fmt.Errorf("insert audit event viewer: %w", err)
		}
	}
	return event, nil
}

func (s *Store) ListAuditEvents(ctx context.Context, viewerUserID domain.RecordID, filter application.AuditQuery) ([]domain.AuditEvent, error) {
	if filter.Limit <= 0 {
		filter.Limit = application.DefaultLimits().DefaultPageSize
	}
	rows, err := s.db.Query(ctx, `
		select ae.id, ae.event_type, ae.actor_user_id, ae.resource_type, ae.resource_id,
		       ae.brain_id, ae.service_id, ae.execution_id, ae.status, ae.metadata, ae.created_at
		from public.audit_event_viewers aev
		join public.audit_events ae on ae.id = aev.audit_event_id
		left join public.brains b on b.id = ae.brain_id
		left join public.services sv on sv.id = ae.service_id
		where aev.user_id = $1
		  and ($2::text = '' or ae.event_type = $2)
		  and ($3::text = '' or ae.status = $3)
		  and ($4::text = '' or b.canonical_id = $4 or sv.canonical_id = $4)
		  and ($5::timestamptz is null or ae.created_at < $5)
		order by ae.created_at desc, ae.id desc
		limit $6
	`, viewerUserID, filter.EventType, string(filter.Status), filter.NodeID, filter.Before, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()
	events := make([]domain.AuditEvent, 0)
	for rows.Next() {
		event, err := scanAuditEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("list audit events scan: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list audit events rows: %w", err)
	}
	return events, nil
}

func scanChatMessage(row interface{ Scan(...any) error }) (domain.ChatMessage, error) {
	var message domain.ChatMessage
	err := row.Scan(&message.ID, &message.BrainID, &message.UserID, &message.Role,
		&message.Content, &message.Model, &message.CreatedAt)
	return message, err
}

func (s *Store) ListChatMessages(ctx context.Context, brainID, userID domain.RecordID, limit int) ([]domain.ChatMessage, error) {
	if limit <= 0 {
		limit = application.DefaultLimits().ChatHistoryMessages
	}
	rows, err := s.db.Query(ctx, `
		select id, brain_id, user_id, role, content, model, created_at
		from (
			select id, brain_id, user_id, role, content, model, created_at
		from public.chat_messages
		where brain_id = $1 and user_id = $2
			order by created_at desc,
			         case when role = 'assistant' then 0 else 1 end,
			         id desc
			limit $3
		) recent
		order by created_at,
		         case when role = 'user' then 0 else 1 end,
		         id
	`, brainID, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list chat messages: %w", err)
	}
	defer rows.Close()
	messages := make([]domain.ChatMessage, 0)
	for rows.Next() {
		message, err := scanChatMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("list chat messages scan: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list chat messages rows: %w", err)
	}
	return messages, nil
}

// InsertChatPair inserts the user and assistant messages atomically.
func (s *Store) InsertChatPair(ctx context.Context, brainID, userID domain.RecordID, userContent, assistantContent, model string) ([]domain.ChatMessage, error) {
	var messages []domain.ChatMessage
	err := s.withinTx(ctx, func(tx *Store) error {
		userMessage, err := scanChatMessage(tx.db.QueryRow(ctx, `
			insert into public.chat_messages (brain_id, user_id, role, content)
			values ($1, $2, 'user', $3)
			returning id, brain_id, user_id, role, content, model, created_at
		`, brainID, userID, userContent))
		if err != nil {
			return fmt.Errorf("insert user chat message: %w", err)
		}
		assistantMessage, err := scanChatMessage(tx.db.QueryRow(ctx, `
			insert into public.chat_messages (brain_id, user_id, role, content, model)
			values ($1, $2, 'assistant', $3, $4)
			returning id, brain_id, user_id, role, content, model, created_at
		`, brainID, userID, assistantContent, model))
		if err != nil {
			return fmt.Errorf("insert assistant chat message: %w", err)
		}
		messages = []domain.ChatMessage{userMessage, assistantMessage}
		return nil
	})
	return messages, err
}

func (s *Store) ClearChat(ctx context.Context, brainID, userID domain.RecordID) (int64, error) {
	tag, err := s.db.Exec(ctx, `delete from public.chat_messages where brain_id = $1 and user_id = $2`, brainID, userID)
	if err != nil {
		return 0, fmt.Errorf("clear chat: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ListNetworkRoutes returns persistence facts for all enabled routes. Visibility
// and ownership projection remain application policy.
func (s *Store) ListNetworkRoutes(ctx context.Context) ([]application.NetworkRouteSnapshot, error) {
	rows, err := s.db.Query(ctx, `
		select r.id, qp.id, b.id, b.canonical_id, b.display_name, b.owner_user_id,
		       qp.path, qp.operations, qp.visibility, qp.state, r.terminal_mode,
		       db.id, db.canonical_id, db.display_name, db.owner_user_id
		from public.routes r
		join public.query_paths qp on qp.id = r.query_path_id
		join public.brains b on b.id = qp.brain_id
		left join public.brains db on db.id = r.destination_brain_id
		where qp.state = 'enabled'
		order by qp.updated_at desc, r.id
	`)
	if err != nil {
		return nil, fmt.Errorf("list network routes: %w", err)
	}
	routes := make([]application.NetworkRouteSnapshot, 0)
	for rows.Next() {
		var route application.NetworkRouteSnapshot
		var operations []string
		if err := rows.Scan(&route.RouteID, &route.QueryPathID, &route.SourceBrainID,
			&route.SourceCanonicalID, &route.SourceDisplayName, &route.SourceOwnerUserID,
			&route.Path, &operations, &route.Visibility, &route.State, &route.TerminalMode,
			&route.DestinationBrainID, &route.DestinationCanonicalID,
			&route.DestinationDisplayName, &route.DestinationOwnerUserID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("list network routes scan: %w", err)
		}
		route.Operations = make([]domain.Operation, len(operations))
		for i := range operations {
			route.Operations[i] = domain.Operation(operations[i])
		}
		routes = append(routes, route)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("list network routes rows: %w", err)
	}
	for i := range routes {
		ownerRows, err := s.db.Query(ctx, `
			select distinct b.owner_user_id
			from public.query_path_brain_grants qbg
			join public.brains b on b.id = qbg.brain_id
			where qbg.query_path_id = $1
			order by b.owner_user_id
		`, routes[i].QueryPathID)
		if err != nil {
			return nil, fmt.Errorf("list network route grant owners: %w", err)
		}
		for ownerRows.Next() {
			var userID domain.RecordID
			if err := ownerRows.Scan(&userID); err != nil {
				ownerRows.Close()
				return nil, fmt.Errorf("list network route grant owner scan: %w", err)
			}
			routes[i].AllowedBrainOwnerUserIDs = append(routes[i].AllowedBrainOwnerUserIDs, userID)
		}
		err = ownerRows.Err()
		ownerRows.Close()
		if err != nil {
			return nil, fmt.Errorf("list network route grant owner rows: %w", err)
		}

		hopRows, err := s.db.Query(ctx, `
			select rh.hop_index, sv.id, sv.canonical_id, sv.display_name, sv.owner_user_id
			from public.route_hops rh
			join public.services sv on sv.id = rh.service_id
			where rh.route_id = $1
			order by rh.hop_index
		`, routes[i].RouteID)
		if err != nil {
			return nil, fmt.Errorf("list network route hops: %w", err)
		}
		for hopRows.Next() {
			var hop application.NetworkHopSnapshot
			if err := hopRows.Scan(&hop.HopIndex, &hop.ServiceID, &hop.CanonicalID,
				&hop.DisplayName, &hop.OwnerUserID); err != nil {
				hopRows.Close()
				return nil, fmt.Errorf("list network route hop scan: %w", err)
			}
			routes[i].Hops = append(routes[i].Hops, hop)
		}
		err = hopRows.Err()
		hopRows.Close()
		if err != nil {
			return nil, fmt.Errorf("list network route hop rows: %w", err)
		}
	}
	return routes, nil
}

func (s *Store) SearchNodes(ctx context.Context, query string, limit int) ([]application.NetworkNodeSnapshot, error) {
	if limit <= 0 {
		limit = application.DefaultLimits().DefaultPageSize
	}
	rows, err := s.db.Query(ctx, `
		select canonical_id, node_type, display_name, owner_user_id, status
		from (
			select canonical_id, 'brain'::text as node_type, display_name, owner_user_id, status from public.brains
			union all
			select canonical_id, 'service'::text as node_type, display_name, owner_user_id, status from public.services
		) nodes
		where $1 = '' or canonical_id ilike '%' || $1 || '%' or display_name ilike '%' || $1 || '%'
		order by canonical_id
		limit $2
	`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("search nodes: %w", err)
	}
	defer rows.Close()
	nodes := make([]application.NetworkNodeSnapshot, 0)
	for rows.Next() {
		var node application.NetworkNodeSnapshot
		if err := rows.Scan(&node.ID, &node.Type, &node.DisplayName, &node.OwnerUserID, &node.Status); err != nil {
			return nil, fmt.Errorf("search nodes scan: %w", err)
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search nodes rows: %w", err)
	}
	return nodes, nil
}
