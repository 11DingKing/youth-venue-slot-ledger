package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
)

func (s *Store) CreateUser(ctx context.Context, user domain.User) (domain.User, error) {
	if !user.Role.Valid() || strings.TrimSpace(user.Email) == "" || strings.TrimSpace(user.Name) == "" {
		return domain.User{}, fmt.Errorf("%w: invalid user", domain.ErrValidation)
	}
	now := user.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var birth any
	if user.BirthDate != nil {
		birth = utc(*user.BirthDate)
	}
	result, err := s.q.ExecContext(ctx, `INSERT INTO users
		(email, password_hash, name, role, birth_date, ability_level, active, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, strings.ToLower(strings.TrimSpace(user.Email)), user.PasswordHash,
		user.Name, user.Role, birth, user.AbilityLevel, boolInt(user.Active), utc(now))
	if err != nil {
		if isUniqueConstraint(err) {
			return domain.User{}, fmt.Errorf("%w: email already exists", domain.ErrConflict)
		}
		return domain.User{}, fmt.Errorf("create user: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return domain.User{}, fmt.Errorf("user id: %w", err)
	}
	return s.UserByID(ctx, id)
}

func (s *Store) CreateAccountRecord(ctx context.Context, user domain.User) (domain.User, error) {
	if user.Role == domain.RoleCoach && !user.Active {
		return domain.User{}, fmt.Errorf("%w: coach account must be active", domain.ErrValidation)
	}
	created, err := s.CreateUser(ctx, user)
	if err != nil {
		return domain.User{}, err
	}
	return created, nil
}

func (s *Store) UserByEmail(ctx context.Context, email string) (domain.User, error) {
	return scanUser(s.q.QueryRowContext(ctx, `SELECT id, email, password_hash, name, role, birth_date,
		ability_level, active, created_at FROM users WHERE email = ?`, strings.ToLower(strings.TrimSpace(email))))
}

func (s *Store) UserByID(ctx context.Context, id int64) (domain.User, error) {
	return scanUser(s.q.QueryRowContext(ctx, `SELECT id, email, password_hash, name, role, birth_date,
		ability_level, active, created_at FROM users WHERE id = ?`, id))
}

func scanUser(row *sql.Row) (domain.User, error) {
	var user domain.User
	var role string
	var birth sql.NullString
	var active int
	var created string
	err := row.Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Name, &role, &birth,
		&user.AbilityLevel, &active, &created)
	if err != nil {
		return domain.User{}, mapNoRows(err, "user")
	}
	user.Role = domain.Role(role)
	user.Active = active == 1
	user.CreatedAt, err = parseTime(created)
	if err != nil {
		return domain.User{}, err
	}
	user.BirthDate, err = nullableTime(birth)
	return user, err
}

func (s *Store) CreateGuardianAuthorization(ctx context.Context, authorization domain.GuardianAuthorization) (domain.GuardianAuthorization, error) {
	if authorization.GuardianID <= 0 || authorization.StudentID <= 0 || !authorization.ValidUntil.After(authorization.ValidFrom) {
		return domain.GuardianAuthorization{}, fmt.Errorf("%w: invalid guardian authorization", domain.ErrValidation)
	}
	result, err := s.q.ExecContext(ctx, `INSERT INTO guardian_authorizations
		(guardian_id, student_id, valid_from, valid_until, revoked_at) VALUES (?, ?, ?, ?, NULL)`,
		authorization.GuardianID, authorization.StudentID, utc(authorization.ValidFrom), utc(authorization.ValidUntil))
	if err != nil {
		if isUniqueConstraint(err) {
			return domain.GuardianAuthorization{}, fmt.Errorf("%w: guardian authorization exists", domain.ErrConflict)
		}
		return domain.GuardianAuthorization{}, fmt.Errorf("create guardian authorization: %w", err)
	}
	authorization.ID, err = result.LastInsertId()
	return authorization, err
}

func (s *Store) HasActiveGuardianAuthorization(ctx context.Context, guardianID, studentID int64, at time.Time) (bool, error) {
	var count int
	err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM guardian_authorizations
		WHERE guardian_id = ? AND student_id = ? AND revoked_at IS NULL
		AND valid_from <= ? AND valid_until > ?`, guardianID, studentID, utc(at), utc(at)).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check guardian authorization: %w", err)
	}
	return count > 0, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
