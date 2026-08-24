package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
)

func (s *Store) CreateVenue(ctx context.Context, venue domain.Venue) (domain.Venue, error) {
	result, err := s.q.ExecContext(ctx, `INSERT INTO venues(name, district, timezone, active)
		VALUES (?, ?, ?, ?)`, venue.Name, venue.District, venue.Timezone, boolInt(venue.Active))
	if err != nil {
		if isUniqueConstraint(err) {
			return domain.Venue{}, fmt.Errorf("%w: venue name exists", domain.ErrConflict)
		}
		return domain.Venue{}, fmt.Errorf("create venue: %w", err)
	}
	venue.ID, err = result.LastInsertId()
	return venue, err
}

func (s *Store) ListVenues(ctx context.Context, limit, offset int) ([]domain.Venue, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT id, name, district, timezone, active FROM venues
		WHERE active = 1 ORDER BY district, name LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list venues: %w", err)
	}
	defer rows.Close()
	venues := make([]domain.Venue, 0)
	for rows.Next() {
		var venue domain.Venue
		var active int
		if err := rows.Scan(&venue.ID, &venue.Name, &venue.District, &venue.Timezone, &active); err != nil {
			return nil, fmt.Errorf("scan venue: %w", err)
		}
		venue.Active = active == 1
		venues = append(venues, venue)
	}
	return venues, rows.Err()
}

func (s *Store) CreateSlot(ctx context.Context, slot domain.Slot) (domain.Slot, error) {
	if err := slot.Validate(); err != nil {
		return domain.Slot{}, err
	}
	result, err := s.q.ExecContext(ctx, `INSERT INTO slots
		(venue_id, sport, starts_at, ends_at, capacity, reserved, minimum_age, maximum_age,
		minimum_ability, maximum_ability, status, version) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
		slot.VenueID, slot.Sport, utc(slot.StartsAt), utc(slot.EndsAt), slot.Capacity, slot.Reserved,
		slot.MinimumAge, slot.MaximumAge, slot.MinimumAbility, slot.MaximumAbility, slot.Status)
	if err != nil {
		if isUniqueConstraint(err) {
			return domain.Slot{}, fmt.Errorf("%w: slot conflicts with existing schedule", domain.ErrConflict)
		}
		return domain.Slot{}, fmt.Errorf("create slot: %w", err)
	}
	slot.ID, err = result.LastInsertId()
	if err != nil {
		return domain.Slot{}, err
	}
	return s.SlotByID(ctx, slot.ID)
}

func (s *Store) SlotByID(ctx context.Context, id int64) (domain.Slot, error) {
	return scanSlot(s.q.QueryRowContext(ctx, `SELECT id, venue_id, sport, starts_at, ends_at,
		capacity, reserved, minimum_age, maximum_age, minimum_ability, maximum_ability, status, version
		FROM slots WHERE id = ?`, id))
}

func scanSlot(row *sql.Row) (domain.Slot, error) {
	var slot domain.Slot
	var starts, ends, status string
	err := row.Scan(&slot.ID, &slot.VenueID, &slot.Sport, &starts, &ends, &slot.Capacity,
		&slot.Reserved, &slot.MinimumAge, &slot.MaximumAge, &slot.MinimumAbility, &slot.MaximumAbility,
		&status, &slot.Version)
	if err != nil {
		return domain.Slot{}, mapNoRows(err, "slot")
	}
	var parseErr error
	slot.StartsAt, parseErr = parseTime(starts)
	if parseErr != nil {
		return domain.Slot{}, parseErr
	}
	slot.EndsAt, parseErr = parseTime(ends)
	slot.Status = domain.SlotStatus(status)
	return slot, parseErr
}

func (s *Store) ListSlots(ctx context.Context, venueID int64, from, until time.Time, limit, offset int) ([]domain.Slot, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT id, venue_id, sport, starts_at, ends_at,
		capacity, reserved, minimum_age, maximum_age, minimum_ability, maximum_ability, status, version
		FROM slots WHERE venue_id = ? AND starts_at >= ? AND starts_at < ?
		ORDER BY starts_at, sport LIMIT ? OFFSET ?`, venueID, utc(from), utc(until), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list slots: %w", err)
	}
	defer rows.Close()
	var slots []domain.Slot
	for rows.Next() {
		var slot domain.Slot
		var starts, ends, status string
		if err := rows.Scan(&slot.ID, &slot.VenueID, &slot.Sport, &starts, &ends, &slot.Capacity,
			&slot.Reserved, &slot.MinimumAge, &slot.MaximumAge, &slot.MinimumAbility, &slot.MaximumAbility,
			&status, &slot.Version); err != nil {
			return nil, fmt.Errorf("scan slot: %w", err)
		}
		slot.StartsAt, err = parseTime(starts)
		if err != nil {
			return nil, err
		}
		slot.EndsAt, err = parseTime(ends)
		if err != nil {
			return nil, err
		}
		slot.Status = domain.SlotStatus(status)
		slots = append(slots, slot)
	}
	return slots, rows.Err()
}

func (s *Store) ReserveSeat(ctx context.Context, slotID, version int64) (domain.Slot, error) {
	result, err := s.q.ExecContext(ctx, `UPDATE slots SET reserved = reserved + 1, version = version + 1
		WHERE id = ? AND version = ? AND status = 'open' AND reserved < capacity`, slotID, version)
	if err != nil {
		return domain.Slot{}, fmt.Errorf("reserve seat: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return domain.Slot{}, err
	}
	if changed == 0 {
		current, findErr := s.SlotByID(ctx, slotID)
		if findErr != nil {
			return domain.Slot{}, findErr
		}
		if current.Version != version {
			return domain.Slot{}, fmt.Errorf("%w: slot %d", domain.ErrVersionConflict, slotID)
		}
		return domain.Slot{}, fmt.Errorf("%w: slot %d", domain.ErrCapacity, slotID)
	}
	return s.SlotByID(ctx, slotID)
}

func (s *Store) ReserveWaitlistSeat(ctx context.Context, slotID int64) (domain.Slot, error) {
	slot, err := s.SlotByID(ctx, slotID)
	if err != nil {
		return domain.Slot{}, err
	}
	if slot.Status != domain.SlotOpen || slot.Reserved >= slot.Capacity {
		return domain.Slot{}, domain.ErrCapacity
	}
	return s.ReserveSeat(ctx, slot.ID, slot.Version)
}

func (s *Store) ReleaseSeat(ctx context.Context, slotID, version int64) (domain.Slot, error) {
	result, err := s.q.ExecContext(ctx, `UPDATE slots SET reserved = reserved - 1, version = version + 1
		WHERE id = ? AND version = ? AND reserved > 0`, slotID, version)
	if err != nil {
		return domain.Slot{}, fmt.Errorf("release seat: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return domain.Slot{}, fmt.Errorf("%w: slot %d release", domain.ErrVersionConflict, slotID)
	}
	return s.SlotByID(ctx, slotID)
}

func (s *Store) AdjustCapacity(ctx context.Context, slotID, version int64, newCapacity int) (domain.Slot, error) {
	result, err := s.q.ExecContext(ctx, `UPDATE slots SET capacity = ?, version = version + 1
		WHERE id = ? AND version = ? AND reserved <= ? AND ? > 0`, newCapacity, slotID, version, newCapacity, newCapacity)
	if err != nil {
		return domain.Slot{}, fmt.Errorf("adjust capacity: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return domain.Slot{}, fmt.Errorf("%w: capacity conflicts with reservations or version", domain.ErrConflict)
	}
	return s.SlotByID(ctx, slotID)
}

func (s *Store) HasCoachCoverage(ctx context.Context, slotID int64) (bool, error) {
	var count int
	err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM coach_assignments ca
		JOIN coaches c ON c.id = ca.coach_id WHERE ca.slot_id = ? AND c.active = 1`, slotID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("coach coverage: %w", err)
	}
	return count > 0, nil
}

func (s *Store) CreateCoach(ctx context.Context, userID int64, qualification string) (int64, error) {
	result, err := s.q.ExecContext(ctx, `INSERT INTO coaches(user_id, qualification, active)
		VALUES (?, ?, 1)`, userID, qualification)
	if err != nil {
		if isUniqueConstraint(err) {
			return 0, fmt.Errorf("%w: coach profile exists", domain.ErrConflict)
		}
		return 0, fmt.Errorf("create coach: %w", err)
	}
	return result.LastInsertId()
}

func (s *Store) AssignCoach(ctx context.Context, coachID, slotID int64, at time.Time) error {
	var overlaps int
	err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM coach_assignments existing
		JOIN slots occupied ON occupied.id = existing.slot_id
		JOIN slots target ON target.id = ?
		WHERE existing.coach_id = ? AND occupied.starts_at < target.ends_at AND occupied.ends_at > target.starts_at`,
		slotID, coachID).Scan(&overlaps)
	if err != nil {
		return fmt.Errorf("check coach overlap: %w", err)
	}
	if overlaps > 0 {
		return fmt.Errorf("%w: coach schedule overlap", domain.ErrConflict)
	}
	_, err = s.q.ExecContext(ctx, `INSERT INTO coach_assignments(coach_id, slot_id, assigned_at)
		VALUES (?, ?, ?)`, coachID, slotID, utc(at))
	if err != nil {
		if isUniqueConstraint(err) {
			return fmt.Errorf("%w: slot already has coach", domain.ErrConflict)
		}
		return fmt.Errorf("assign coach: %w", err)
	}
	return nil
}
