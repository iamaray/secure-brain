package store

import (
	"context"
	"fmt"
	"time"

	"secure-brain/internal/application"
	"secure-brain/internal/domain"
)

func (s *Store) ListUsers(ctx context.Context) ([]domain.User, error) {
	rows, err := s.db.Query(ctx, `
		select id, handle, display_name, created_at
		from public.app_users
		order by handle, id
	`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	users := make([]domain.User, 0)
	for rows.Next() {
		var user domain.User
		if err := rows.Scan(&user.ID, &user.Handle, &user.DisplayName, &user.CreatedAt); err != nil {
			return nil, fmt.Errorf("list users scan: %w", err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list users rows: %w", err)
	}
	return users, nil
}

func (s *Store) GetUser(ctx context.Context, id domain.RecordID) (domain.User, error) {
	var user domain.User
	err := s.db.QueryRow(ctx, `
		select id, handle, display_name, created_at
		from public.app_users
		where id = $1
	`, id).Scan(&user.ID, &user.Handle, &user.DisplayName, &user.CreatedAt)
	if err != nil {
		return domain.User{}, fmt.Errorf("get user: %w", err)
	}
	return user, nil
}

func (s *Store) CreateSession(ctx context.Context, tokenHash []byte, userID domain.RecordID, expiresAt time.Time) (application.SessionSnapshot, error) {
	var session application.SessionSnapshot
	err := s.db.QueryRow(ctx, `
		insert into public.mock_sessions (token_hash, user_id, expires_at)
		values ($1, $2, $3)
		returning id, token_hash, user_id, created_at, last_seen_at, expires_at
	`, tokenHash, userID, expiresAt.UTC()).Scan(
		&session.ID, &session.TokenHash, &session.UserID, &session.CreatedAt,
		&session.LastSeenAt, &session.ExpiresAt,
	)
	if err != nil {
		return application.SessionSnapshot{}, fmt.Errorf("create session: %w", err)
	}
	return session, nil
}

func (s *Store) GetSessionByTokenHash(ctx context.Context, tokenHash []byte) (application.SessionSnapshot, error) {
	var session application.SessionSnapshot
	err := s.db.QueryRow(ctx, `
		select ms.id, ms.token_hash, ms.user_id, ms.created_at, ms.last_seen_at, ms.expires_at,
		       u.id, u.handle, u.display_name, u.created_at
		from public.mock_sessions ms
		join public.app_users u on u.id = ms.user_id
		where ms.token_hash = $1
		  and ms.expires_at > now()
	`, tokenHash).Scan(
		&session.ID, &session.TokenHash, &session.UserID, &session.CreatedAt,
		&session.LastSeenAt, &session.ExpiresAt, &session.User.ID,
		&session.User.Handle, &session.User.DisplayName, &session.User.CreatedAt,
	)
	if err != nil {
		return application.SessionSnapshot{}, fmt.Errorf("get session: %w", err)
	}
	return session, nil
}

// TouchSession updates last_seen_at only when the previous value is at least a
// minute old. The returned boolean reports whether an update occurred.
func (s *Store) TouchSession(ctx context.Context, id domain.RecordID, now time.Time) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		update public.mock_sessions
		set last_seen_at = $2
		where id = $1
		  and last_seen_at <= $2 - interval '1 minute'
		  and expires_at > $2
	`, id, now.UTC())
	if err != nil {
		return false, fmt.Errorf("touch session: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) (bool, error) {
	tag, err := s.db.Exec(ctx, `delete from public.mock_sessions where token_hash = $1`, tokenHash)
	if err != nil {
		return false, fmt.Errorf("delete session: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
