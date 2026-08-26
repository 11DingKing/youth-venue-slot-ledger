package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
)

type Closure struct {
	ID        int64                `json:"id"`
	VenueID   int64                `json:"venue_id"`
	StartsAt  time.Time            `json:"starts_at"`
	EndsAt    time.Time            `json:"ends_at"`
	Reason    string               `json:"reason"`
	Status    domain.ClosureStatus `json:"status"`
	Version   int64                `json:"version"`
	CreatedBy int64                `json:"created_by"`
	CreatedAt time.Time            `json:"created_at"`
	UpdatedAt time.Time            `json:"updated_at"`
}

func (s *Store) CreateClosure(ctx context.Context, closure Closure) (Closure, error) {
	if closure.VenueID <= 0 || closure.Reason == "" || !closure.EndsAt.After(closure.StartsAt) {
		return Closure{}, fmt.Errorf("%w: invalid closure", domain.ErrValidation)
	}
	result, err := s.q.ExecContext(ctx, `INSERT INTO closures
		(venue_id, starts_at, ends_at, reason, status, version, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?)`, closure.VenueID, utc(closure.StartsAt), utc(closure.EndsAt),
		closure.Reason, closure.Status, closure.CreatedBy, utc(closure.CreatedAt), utc(closure.UpdatedAt))
	if err != nil {
		return Closure{}, fmt.Errorf("create closure: %w", err)
	}
	closure.ID, err = result.LastInsertId()
	if err != nil {
		return Closure{}, err
	}
	return s.ClosureByID(ctx, closure.ID)
}

func (s *Store) ClosureByID(ctx context.Context, id int64) (Closure, error) {
	row := s.q.QueryRowContext(ctx, `SELECT id, venue_id, starts_at, ends_at, reason, status,
		version, created_by, created_at, updated_at FROM closures WHERE id = ?`, id)
	return scanClosure(row)
}

func scanClosure(row *sql.Row) (Closure, error) {
	var closure Closure
	var starts, ends, status, created, updated string
	err := row.Scan(&closure.ID, &closure.VenueID, &starts, &ends, &closure.Reason,
		&status, &closure.Version, &closure.CreatedBy, &created, &updated)
	if err != nil {
		return Closure{}, mapNoRows(err, "closure")
	}
	var parseErr error
	closure.StartsAt, parseErr = parseTime(starts)
	if parseErr != nil {
		return Closure{}, parseErr
	}
	closure.EndsAt, parseErr = parseTime(ends)
	if parseErr != nil {
		return Closure{}, parseErr
	}
	closure.CreatedAt, parseErr = parseTime(created)
	if parseErr != nil {
		return Closure{}, parseErr
	}
	closure.UpdatedAt, parseErr = parseTime(updated)
	closure.Status = domain.ClosureStatus(status)
	return closure, parseErr
}

func (s *Store) TransitionClosure(ctx context.Context, id, version int64, from, to domain.ClosureStatus, at time.Time) (Closure, error) {
	if err := domain.ValidateClosureTransition(from, to); err != nil {
		return Closure{}, err
	}
	result, err := s.q.ExecContext(ctx, `UPDATE closures SET status = ?, version = version + 1,
		updated_at = ? WHERE id = ? AND version = ? AND status = ?`, to, utc(at), id, version, from)
	if err != nil {
		return Closure{}, fmt.Errorf("transition closure: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return Closure{}, fmt.Errorf("%w: closure", domain.ErrVersionConflict)
	}
	return s.ClosureByID(ctx, id)
}

func (s *Store) CloseSlotsForWindow(ctx context.Context, venueID int64, startsAt, endsAt time.Time) (int64, error) {
	result, err := s.q.ExecContext(ctx, `UPDATE slots SET status = 'closed', version = version + 1
		WHERE venue_id = ? AND starts_at < ? AND ends_at > ? AND status = 'open'`,
		venueID, utc(endsAt), utc(startsAt))
	if err != nil {
		return 0, fmt.Errorf("close slots: %w", err)
	}
	return result.RowsAffected()
}

func (s *Store) ReopenSlotsForWindow(ctx context.Context, venueID int64, startsAt, endsAt time.Time) (int64, error) {
	result, err := s.q.ExecContext(ctx, `UPDATE slots SET status = 'open', version = version + 1
		WHERE venue_id = ? AND starts_at < ? AND ends_at > ? AND status = 'closed'
		AND NOT EXISTS (
			SELECT 1 FROM closures c WHERE c.venue_id = slots.venue_id AND c.status = 'applied'
			AND c.starts_at < slots.ends_at AND c.ends_at > slots.starts_at
		)`, venueID, utc(endsAt), utc(startsAt))
	if err != nil {
		return 0, fmt.Errorf("reopen slots: %w", err)
	}
	return result.RowsAffected()
}

func (s *Store) BookingIDsAffectedByClosure(ctx context.Context, venueID int64, startsAt, endsAt time.Time, limit int) ([]int64, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT b.id FROM bookings b JOIN slots s ON s.id = b.slot_id
		WHERE s.venue_id = ? AND s.starts_at < ? AND s.ends_at > ?
		AND b.status IN ('held','confirmed') ORDER BY b.id LIMIT ?`, venueID, utc(endsAt), utc(startsAt), limit)
	if err != nil {
		return nil, fmt.Errorf("list closure bookings: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
