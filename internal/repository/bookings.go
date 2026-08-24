package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
)

func (s *Store) CreateBooking(ctx context.Context, booking domain.Booking) (domain.Booking, error) {
	var expires any
	if booking.HoldExpiresAt != nil {
		expires = utc(*booking.HoldExpiresAt)
	}
	result, err := s.q.ExecContext(ctx, `INSERT INTO bookings
		(student_id, guardian_id, slot_id, status, hold_expires_at, version, cancellation_reason, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 1, '', ?, ?)`, booking.StudentID, booking.GuardianID, booking.SlotID,
		booking.Status, expires, utc(booking.CreatedAt), utc(booking.UpdatedAt))
	if err != nil {
		if isUniqueConstraint(err) {
			return domain.Booking{}, fmt.Errorf("%w: active booking already exists", domain.ErrConflict)
		}
		return domain.Booking{}, fmt.Errorf("create booking: %w", err)
	}
	booking.ID, err = result.LastInsertId()
	if err != nil {
		return domain.Booking{}, err
	}
	return s.BookingByID(ctx, booking.ID)
}

func (s *Store) BookingByID(ctx context.Context, id int64) (domain.Booking, error) {
	return scanBooking(s.q.QueryRowContext(ctx, `SELECT id, student_id, guardian_id, slot_id, status,
		hold_expires_at, version, cancellation_reason, created_at, updated_at FROM bookings WHERE id = ?`, id))
}

func scanBooking(row *sql.Row) (domain.Booking, error) {
	var booking domain.Booking
	var status, created, updated string
	var expires sql.NullString
	err := row.Scan(&booking.ID, &booking.StudentID, &booking.GuardianID, &booking.SlotID, &status,
		&expires, &booking.Version, &booking.CancellationReason, &created, &updated)
	if err != nil {
		return domain.Booking{}, mapNoRows(err, "booking")
	}
	booking.Status = domain.BookingStatus(status)
	var parseErr error
	booking.HoldExpiresAt, parseErr = nullableTime(expires)
	if parseErr != nil {
		return domain.Booking{}, parseErr
	}
	booking.CreatedAt, parseErr = parseTime(created)
	if parseErr != nil {
		return domain.Booking{}, parseErr
	}
	booking.UpdatedAt, parseErr = parseTime(updated)
	return booking, parseErr
}

func (s *Store) ListBookings(ctx context.Context, userID int64, role domain.Role, status string, limit, offset int) ([]domain.Booking, error) {
	column := "student_id"
	if role == domain.RoleGuardian {
		column = "guardian_id"
	}
	query := `SELECT id, student_id, guardian_id, slot_id, status, hold_expires_at,
		version, cancellation_reason, created_at, updated_at FROM bookings WHERE ` + column + ` = ?`
	args := []any{userID}
	if status != "" {
		query += " AND status = ?"
		args = append(args, status)
	}
	query += " ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := s.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list bookings: %w", err)
	}
	defer rows.Close()
	var bookings []domain.Booking
	for rows.Next() {
		var booking domain.Booking
		var state, created, updated string
		var expires sql.NullString
		if err := rows.Scan(&booking.ID, &booking.StudentID, &booking.GuardianID, &booking.SlotID,
			&state, &expires, &booking.Version, &booking.CancellationReason, &created, &updated); err != nil {
			return nil, fmt.Errorf("scan booking: %w", err)
		}
		booking.Status = domain.BookingStatus(state)
		booking.HoldExpiresAt, err = nullableTime(expires)
		if err != nil {
			return nil, err
		}
		booking.CreatedAt, err = parseTime(created)
		if err != nil {
			return nil, err
		}
		booking.UpdatedAt, err = parseTime(updated)
		if err != nil {
			return nil, err
		}
		bookings = append(bookings, booking)
	}
	return bookings, rows.Err()
}

func (s *Store) TransitionBooking(ctx context.Context, id, version int64, from, to domain.BookingStatus, reason string, at time.Time) (domain.Booking, error) {
	if err := domain.ValidateBookingTransition(from, to); err != nil {
		return domain.Booking{}, err
	}
	result, err := s.q.ExecContext(ctx, `UPDATE bookings SET status = ?, version = version + 1,
		cancellation_reason = ?, updated_at = ? WHERE id = ? AND version = ? AND status = ?`,
		to, reason, utc(at), id, version, from)
	if err != nil {
		return domain.Booking{}, fmt.Errorf("transition booking: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return domain.Booking{}, fmt.Errorf("%w: booking %d", domain.ErrVersionConflict, id)
	}
	return s.BookingByID(ctx, id)
}

func (s *Store) MoveBooking(ctx context.Context, id, version, fromSlotID, toSlotID int64, at time.Time) (domain.Booking, error) {
	result, err := s.q.ExecContext(ctx, `UPDATE bookings SET slot_id = ?, version = version + 1, updated_at = ?
		WHERE id = ? AND version = ? AND slot_id = ? AND status IN ('held','confirmed')`,
		toSlotID, utc(at), id, version, fromSlotID)
	if err != nil {
		if isUniqueConstraint(err) {
			return domain.Booking{}, fmt.Errorf("%w: destination duplicate", domain.ErrConflict)
		}
		return domain.Booking{}, fmt.Errorf("move booking: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return domain.Booking{}, fmt.Errorf("%w: booking move", domain.ErrVersionConflict)
	}
	return s.BookingByID(ctx, id)
}

func (s *Store) AddWaitlistEntry(ctx context.Context, bookingID, slotID int64, at time.Time) error {
	var next int
	if err := s.q.QueryRowContext(ctx, `SELECT COALESCE(MAX(position),0) + 1 FROM waitlist_entries
		WHERE slot_id = ? AND promoted_at IS NULL`, slotID).Scan(&next); err != nil {
		return fmt.Errorf("next waitlist position: %w", err)
	}
	_, err := s.q.ExecContext(ctx, `INSERT INTO waitlist_entries
		(booking_id, slot_id, position, created_at) VALUES (?, ?, ?, ?)`, bookingID, slotID, next, utc(at))
	if err != nil {
		return fmt.Errorf("add waitlist entry: %w", err)
	}
	return nil
}

func (s *Store) NextWaitlistedBooking(ctx context.Context, slotID int64) (domain.Booking, error) {
	return scanBooking(s.q.QueryRowContext(ctx, `SELECT b.id, b.student_id, b.guardian_id, b.slot_id,
		b.status, b.hold_expires_at, b.version, b.cancellation_reason, b.created_at, b.updated_at
		FROM waitlist_entries w JOIN bookings b ON b.id = w.booking_id
		WHERE w.slot_id = ? AND w.promoted_at IS NULL AND b.status = 'waitlisted'
		ORDER BY w.position, w.id LIMIT 1`, slotID))
}

func (s *Store) MarkWaitlistPromoted(ctx context.Context, bookingID int64, at time.Time) error {
	result, err := s.q.ExecContext(ctx, `UPDATE waitlist_entries SET promoted_at = ?
		WHERE booking_id = ? AND promoted_at IS NULL`, utc(at), bookingID)
	if err != nil {
		return fmt.Errorf("mark waitlist promoted: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return fmt.Errorf("%w: waitlist entry", domain.ErrVersionConflict)
	}
	return nil
}

func (s *Store) PromoteWaitlistedBooking(ctx context.Context, bookingID, version int64, holdExpiresAt, at time.Time) (domain.Booking, error) {
	result, err := s.q.ExecContext(ctx, `UPDATE bookings SET status = 'held', hold_expires_at = ?,
		version = version + 1, updated_at = ? WHERE id = ? AND version = ? AND status = 'waitlisted'`,
		utc(holdExpiresAt), utc(at), bookingID, version)
	if err != nil {
		return domain.Booking{}, fmt.Errorf("promote waitlisted booking: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return domain.Booking{}, fmt.Errorf("%w: waitlist promotion", domain.ErrVersionConflict)
	}
	return s.BookingByID(ctx, bookingID)
}

func (s *Store) RecordCheckIn(ctx context.Context, bookingID, actorID int64, requestID string, at time.Time) error {
	_, err := s.q.ExecContext(ctx, `INSERT INTO checkins
		(booking_id, redeemed_by, redeemed_at, request_id) VALUES (?, ?, ?, ?)`, bookingID, actorID, utc(at), requestID)
	if err != nil {
		if isUniqueConstraint(err) {
			return fmt.Errorf("%w: booking already checked in", domain.ErrConflict)
		}
		return fmt.Errorf("record check-in: %w", err)
	}
	return nil
}

func (s *Store) RecordCheckInStandalone(ctx context.Context, bookingID, actorID int64, requestID string, at time.Time) error {
	booking, err := s.BookingByID(ctx, bookingID)
	if err != nil {
		return err
	}
	if booking.Status != domain.BookingConfirmed {
		return domain.ErrInvalidState
	}
	return s.RecordCheckIn(ctx, bookingID, actorID, requestID, at)
}
