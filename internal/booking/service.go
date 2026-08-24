package booking

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
)

type Clock func() time.Time

type Service struct {
	store        *repository.Store
	now          Clock
	holdDuration time.Duration
}

func New(store *repository.Store, now Clock, holdDuration time.Duration) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, now: now, holdDuration: holdDuration}
}

type CreateRequest struct {
	StudentID      int64  `json:"student_id"`
	GuardianID     int64  `json:"guardian_id"`
	SlotID         int64  `json:"slot_id"`
	IdempotencyKey string `json:"-"`
}

func (s *Service) Create(ctx context.Context, actor domain.Actor, request CreateRequest) (domain.Booking, error) {
	if err := actor.Require(domain.RoleGuardian, domain.RoleOperator); err != nil {
		return domain.Booking{}, err
	}
	if request.StudentID <= 0 || request.GuardianID <= 0 || request.SlotID <= 0 || request.IdempotencyKey == "" {
		return domain.Booking{}, fmt.Errorf("%w: student, guardian, slot and idempotency key are required", domain.ErrValidation)
	}
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		created, err := s.createOnce(ctx, actor, request)
		if err == nil {
			return created, nil
		}
		lastErr = err
		if !retryableContention(err) {
			return domain.Booking{}, err
		}
		delay := time.Duration(attempt+1) * 5 * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return domain.Booking{}, errors.Join(ctx.Err(), lastErr)
		case <-timer.C:
		}
	}
	return domain.Booking{}, fmt.Errorf("booking contention retry exhausted: %w", lastErr)
}

func (s *Service) createOnce(ctx context.Context, actor domain.Actor, request CreateRequest) (domain.Booking, error) {
	now := s.now().UTC()
	requestHash := hashRequest(request.StudentID, request.GuardianID, request.SlotID)
	var created domain.Booking
	err := s.store.WithTx(ctx, func(tx *repository.Store) error {
		record, found, err := tx.IdempotencyRecord(ctx, actor.UserID, "POST", "/v1/bookings", request.IdempotencyKey, now)
		if err != nil {
			return err
		}
		if found {
			if record.RequestHash != requestHash || record.ResourceType != "booking" {
				return domain.ErrIdempotencyReuse
			}
			created, err = tx.BookingByID(ctx, record.ResourceID)
			return err
		}
		student, err := tx.UserByID(ctx, request.StudentID)
		if err != nil {
			return err
		}
		guardian, err := tx.UserByID(ctx, request.GuardianID)
		if err != nil {
			return err
		}
		if guardian.Role != domain.RoleGuardian || !guardian.Active {
			return domain.ErrGuardianRequired
		}
		if actor.Role == domain.RoleGuardian && actor.UserID != guardian.ID {
			return domain.ErrForbidden
		}
		authorized, err := tx.HasActiveGuardianAuthorization(ctx, guardian.ID, student.ID, now)
		if err != nil {
			return err
		}
		if !authorized {
			return domain.ErrGuardianRequired
		}
		slot, err := tx.SlotByID(ctx, request.SlotID)
		if err != nil {
			return err
		}
		if err := domain.ValidateEligibility(student, slot); err != nil {
			return err
		}
		covered, err := tx.HasCoachCoverage(ctx, slot.ID)
		if err != nil {
			return err
		}
		if !covered {
			return domain.ErrCoachCoverage
		}
		status := domain.BookingHeld
		var holdExpires *time.Time
		if slot.Status != domain.SlotOpen || slot.Reserved >= slot.Capacity {
			status = domain.BookingWaitlisted
		} else {
			updated, err := tx.ReserveSeat(ctx, slot.ID, slot.Version)
			if err != nil {
				return err
			}
			expires := now.Add(s.holdDuration)
			holdExpires = &expires
			slot = updated
		}
		created, err = tx.CreateBooking(ctx, domain.Booking{
			StudentID: student.ID, GuardianID: guardian.ID, SlotID: slot.ID, Status: status,
			HoldExpiresAt: holdExpires, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		if status == domain.BookingWaitlisted {
			if err := tx.AddWaitlistEntry(ctx, created.ID, slot.ID, now); err != nil {
				return err
			}
		} else if err := appendLedger(tx, ctx, slot, created.ID, domain.LedgerReserve, 1, "booking hold", actor.RequestID, now); err != nil {
			return err
		}
		if _, err := tx.AppendAudit(ctx, auditEvent(actor, "booking.create", "booking", created.ID, "success", map[string]string{"status": string(status)}, now)); err != nil {
			return err
		}
		return tx.SaveIdempotencyRecord(ctx, actor.UserID, "POST", "/v1/bookings", request.IdempotencyKey,
			requestHash, "booking", created.ID, now.Add(24*time.Hour), now)
	})
	return created, err
}

func retryableContention(err error) bool {
	if errors.Is(err, domain.ErrVersionConflict) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") || strings.Contains(message, "sqlite_busy")
}

func (s *Service) Confirm(ctx context.Context, actor domain.Actor, bookingID, version int64) (domain.Booking, error) {
	now := s.now().UTC()
	var confirmed domain.Booking
	err := s.store.WithTx(ctx, func(tx *repository.Store) error {
		booking, err := tx.BookingByID(ctx, bookingID)
		if err != nil {
			return err
		}
		if actor.Role != domain.RoleOperator && actor.UserID != booking.GuardianID {
			return domain.ErrForbidden
		}
		if booking.HoldExpiresAt == nil || !booking.HoldExpiresAt.After(now) {
			return domain.ErrExpired
		}
		confirmed, err = tx.TransitionBooking(ctx, booking.ID, version, domain.BookingHeld, domain.BookingConfirmed, "", now)
		if err != nil {
			return err
		}
		_, err = tx.AppendAudit(ctx, auditEvent(actor, "booking.confirm", "booking", booking.ID, "success", nil, now))
		return err
	})
	return confirmed, err
}

func (s *Service) Cancel(ctx context.Context, actor domain.Actor, bookingID, version int64, reason string) (domain.Booking, error) {
	if reason == "" {
		return domain.Booking{}, fmt.Errorf("%w: cancellation reason required", domain.ErrValidation)
	}
	now := s.now().UTC()
	var cancelled domain.Booking
	err := s.store.WithTx(ctx, func(tx *repository.Store) error {
		booking, err := tx.BookingByID(ctx, bookingID)
		if err != nil {
			return err
		}
		if actor.Role != domain.RoleOperator && actor.UserID != booking.GuardianID && actor.UserID != booking.StudentID {
			return domain.ErrForbidden
		}
		if booking.Version != version {
			return domain.ErrVersionConflict
		}
		if booking.Status != domain.BookingHeld && booking.Status != domain.BookingConfirmed && booking.Status != domain.BookingWaitlisted {
			return domain.ErrInvalidState
		}
		cancelled, err = tx.TransitionBooking(ctx, booking.ID, booking.Version, booking.Status, domain.BookingCancelled, reason, now)
		if err != nil {
			return err
		}
		if booking.Status == domain.BookingHeld || booking.Status == domain.BookingConfirmed {
			slot, err := tx.SlotByID(ctx, booking.SlotID)
			if err != nil {
				return err
			}
			slot, err = tx.ReleaseSeat(ctx, slot.ID, slot.Version)
			if err != nil {
				return err
			}
			if err := appendLedger(tx, ctx, slot, booking.ID, domain.LedgerRelease, -1, reason, actor.RequestID, now); err != nil {
				return err
			}
			if slot.Status == domain.SlotOpen && slot.Reserved < slot.Capacity {
				next, nextErr := tx.NextWaitlistedBooking(ctx, slot.ID)
				if nextErr != nil && !errors.Is(nextErr, domain.ErrNotFound) {
					return nextErr
				}
				if nextErr == nil {
					slot, err = tx.ReserveSeat(ctx, slot.ID, slot.Version)
					if err != nil {
						return err
					}
					if _, err := tx.PromoteWaitlistedBooking(ctx, next.ID, next.Version, now.Add(s.holdDuration), now); err != nil {
						return err
					}
					if err := tx.MarkWaitlistPromoted(ctx, next.ID, now); err != nil {
						return err
					}
					if err := appendLedger(tx, ctx, slot, next.ID, domain.LedgerReserve, 1, "waitlist promotion", actor.RequestID+":promote", now); err != nil {
						return err
					}
				}
			}
		}
		_, err = tx.AppendAudit(ctx, auditEvent(actor, "booking.cancel", "booking", booking.ID, "success", map[string]string{"reason": reason}, now))
		return err
	})
	return cancelled, err
}

func (s *Service) Reschedule(ctx context.Context, actor domain.Actor, bookingID, version, destinationSlotID int64) (domain.Booking, error) {
	now := s.now().UTC()
	destination, err := s.reserveRescheduleDestination(ctx, actor, bookingID, version, destinationSlotID, now)
	if err != nil {
		return domain.Booking{}, err
	}
	var moved domain.Booking
	err = s.store.WithTx(ctx, func(tx *repository.Store) error {
		booking, err := tx.BookingByID(ctx, bookingID)
		if err != nil {
			return err
		}
		if actor.Role != domain.RoleOperator && actor.UserID != booking.GuardianID {
			return domain.ErrForbidden
		}
		if booking.Version != version || (booking.Status != domain.BookingHeld && booking.Status != domain.BookingConfirmed) {
			return domain.ErrInvalidState
		}
		source, err := tx.SlotByID(ctx, booking.SlotID)
		if err != nil {
			return err
		}
		source, err = tx.ReleaseSeat(ctx, source.ID, source.Version)
		if err != nil {
			return err
		}
		moved, err = tx.MoveBooking(ctx, booking.ID, booking.Version, source.ID, destination.ID, now)
		if err != nil {
			return err
		}
		correlation := actor.RequestID + ":reschedule"
		if err := appendLedger(tx, ctx, source, booking.ID, domain.LedgerTransferOut, -1, "cross-venue reschedule", correlation, now); err != nil {
			return err
		}
		if err := appendLedger(tx, ctx, destination, booking.ID, domain.LedgerTransferIn, 1, "cross-venue reschedule", correlation, now); err != nil {
			return err
		}
		_, err = tx.AppendAudit(ctx, auditEvent(actor, "booking.reschedule", "booking", booking.ID, "success",
			map[string]string{"from_slot": strconv.FormatInt(source.ID, 10), "to_slot": strconv.FormatInt(destination.ID, 10)}, now))
		return err
	})
	return moved, err
}

func (s *Service) reserveRescheduleDestination(ctx context.Context, actor domain.Actor, bookingID, version, destinationSlotID int64, now time.Time) (domain.Slot, error) {
	var destination domain.Slot
	err := s.store.WithTx(ctx, func(tx *repository.Store) error {
		booking, err := tx.BookingByID(ctx, bookingID)
		if err != nil {
			return err
		}
		if actor.Role != domain.RoleOperator && actor.UserID != booking.GuardianID {
			return domain.ErrForbidden
		}
		if booking.Version != version || (booking.Status != domain.BookingHeld && booking.Status != domain.BookingConfirmed) {
			return domain.ErrInvalidState
		}
		student, err := tx.UserByID(ctx, booking.StudentID)
		if err != nil {
			return err
		}
		authorized, err := tx.HasActiveGuardianAuthorization(ctx, booking.GuardianID, booking.StudentID, now)
		if err != nil {
			return err
		}
		if !authorized {
			return domain.ErrGuardianRequired
		}
		destination, err = tx.SlotByID(ctx, destinationSlotID)
		if err != nil {
			return err
		}
		if err := domain.ValidateEligibility(student, destination); err != nil {
			return err
		}
		covered, err := tx.HasCoachCoverage(ctx, destination.ID)
		if err != nil {
			return err
		}
		if !covered {
			return domain.ErrCoachCoverage
		}
		destination, err = tx.ReserveSeat(ctx, destination.ID, destination.Version)
		return err
	})
	return destination, err
}

func (s *Service) List(ctx context.Context, actor domain.Actor, status string, limit, offset int) ([]domain.Booking, error) {
	if actor.Role != domain.RoleStudent && actor.Role != domain.RoleGuardian {
		return nil, domain.ErrForbidden
	}
	return s.store.ListBookings(ctx, actor.UserID, actor.Role, status, limit, offset)
}

func appendLedger(store *repository.Store, ctx context.Context, slot domain.Slot, bookingID int64, kind domain.LedgerKind, delta int, reason, correlation string, at time.Time) error {
	_, err := store.AppendLedger(ctx, domain.LedgerEntry{
		SlotID: slot.ID, BookingID: &bookingID, Kind: kind, Delta: delta, BalanceAfter: slot.Reserved,
		Reason: reason, CorrelationID: correlation, CreatedAt: at,
	})
	return err
}

func auditEvent(actor domain.Actor, action, objectType string, objectID int64, result string, metadata map[string]string, at time.Time) domain.AuditEvent {
	return domain.AuditEvent{ActorID: actor.UserID, ActorRole: actor.Role, Action: action, ObjectType: objectType,
		ObjectID: repository.AuditObjectID(objectID), Result: result, RequestID: actor.RequestID, Metadata: metadata, CreatedAt: at}
}

func hashRequest(values ...int64) string {
	hash := sha256.New()
	for _, value := range values {
		fmt.Fprintf(hash, "%d\x00", value)
	}
	return hex.EncodeToString(hash.Sum(nil))
}
