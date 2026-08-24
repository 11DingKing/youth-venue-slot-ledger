package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
)

func (s *Store) CreateSession(ctx context.Context, session domain.Session) (domain.Session, error) {
	result, err := s.q.ExecContext(ctx, `INSERT INTO sessions
		(user_id, token_hash, expires_at, revoked_at, created_at) VALUES (?, ?, ?, NULL, ?)`,
		session.UserID, session.TokenHash, utc(session.ExpiresAt), utc(session.CreatedAt))
	if err != nil {
		return domain.Session{}, fmt.Errorf("create session: %w", err)
	}
	session.ID, err = result.LastInsertId()
	if err != nil {
		return domain.Session{}, fmt.Errorf("session id: %w", err)
	}
	return session, nil
}

func (s *Store) SessionUserByTokenHash(ctx context.Context, tokenHash string, now time.Time) (domain.Session, domain.User, error) {
	row := s.q.QueryRowContext(ctx, `SELECT
		s.id, s.user_id, s.token_hash, s.expires_at, s.revoked_at, s.created_at,
		u.id, u.email, u.password_hash, u.name, u.role, u.birth_date, u.ability_level, u.active, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.revoked_at IS NULL AND s.expires_at > ? AND u.active = 1`, tokenHash, utc(now))
	var session domain.Session
	var user domain.User
	var sessionExpires, sessionCreated, userCreated string
	var revoked, birth sql.NullString
	var role string
	var active int
	err := row.Scan(&session.ID, &session.UserID, &session.TokenHash, &sessionExpires, &revoked, &sessionCreated,
		&user.ID, &user.Email, &user.PasswordHash, &user.Name, &role, &birth, &user.AbilityLevel, &active, &userCreated)
	if err != nil {
		return domain.Session{}, domain.User{}, mapNoRows(err, "active session")
	}
	var parseErr error
	session.ExpiresAt, parseErr = parseTime(sessionExpires)
	if parseErr != nil {
		return domain.Session{}, domain.User{}, parseErr
	}
	session.CreatedAt, parseErr = parseTime(sessionCreated)
	if parseErr != nil {
		return domain.Session{}, domain.User{}, parseErr
	}
	session.RevokedAt, parseErr = nullableTime(revoked)
	if parseErr != nil {
		return domain.Session{}, domain.User{}, parseErr
	}
	user.Role = domain.Role(role)
	user.Active = active == 1
	user.CreatedAt, parseErr = parseTime(userCreated)
	if parseErr != nil {
		return domain.Session{}, domain.User{}, parseErr
	}
	user.BirthDate, parseErr = nullableTime(birth)
	return session, user, parseErr
}

func (s *Store) RevokeSession(ctx context.Context, tokenHash string, at time.Time) (bool, error) {
	result, err := s.q.ExecContext(ctx, `UPDATE sessions SET revoked_at = ?
		WHERE token_hash = ? AND revoked_at IS NULL`, utc(at), tokenHash)
	if err != nil {
		return false, fmt.Errorf("revoke session: %w", err)
	}
	changed, err := result.RowsAffected()
	return changed > 0, err
}

func (s *Store) RevokeSessionForLogout(ctx context.Context, tokenHash string, at time.Time) (domain.Session, domain.User, error) {
	session, user, err := s.SessionUserByTokenHash(ctx, tokenHash, at)
	if err != nil {
		return domain.Session{}, domain.User{}, fmt.Errorf("%w: session is not active", domain.ErrUnauthorized)
	}
	revoked, err := s.RevokeSession(ctx, tokenHash, at)
	if err != nil {
		return domain.Session{}, domain.User{}, err
	}
	if !revoked {
		return domain.Session{}, domain.User{}, fmt.Errorf("%w: session is not active", domain.ErrUnauthorized)
	}
	return session, user, nil
}

func (s *Store) RevokeExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	result, err := s.q.ExecContext(ctx, `UPDATE sessions SET revoked_at = ?
		WHERE revoked_at IS NULL AND expires_at <= ?`, utc(now), utc(now))
	if err != nil {
		return 0, fmt.Errorf("revoke expired sessions: %w", err)
	}
	return result.RowsAffected()
}
