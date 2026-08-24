package checkin

import (
	"context"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
)

type Service struct {
	store *repository.Store
	now   func() time.Time
}

func New(store *repository.Store, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, now: now}
}

func (s *Service) Redeem(ctx context.Context, actor domain.Actor, bookingID, version int64) (domain.Booking, error) {
	if err := actor.Require(domain.RoleCoach, domain.RoleOperator); err != nil {
		return domain.Booking{}, err
	}
	now := s.now().UTC()
	booking, err := s.store.BookingByID(ctx, bookingID)
	if err != nil {
		return domain.Booking{}, err
	}
	if booking.Status != domain.BookingConfirmed || booking.Version != version {
		return domain.Booking{}, domain.ErrInvalidState
	}
	slot, err := s.store.SlotByID(ctx, booking.SlotID)
	if err != nil {
		return domain.Booking{}, err
	}
	if err := domain.ValidateCheckInWindow(slot, now); err != nil {
		return domain.Booking{}, err
	}
	if err := s.store.RecordCheckInStandalone(ctx, booking.ID, actor.UserID, actor.RequestID, now); err != nil {
		return domain.Booking{}, err
	}
	var checkedIn domain.Booking
	err = s.store.WithTx(ctx, func(tx *repository.Store) error {
		checkedIn, err = tx.TransitionBooking(ctx, booking.ID, booking.Version, domain.BookingConfirmed, domain.BookingCheckedIn, "", now)
		if err != nil {
			return err
		}
		_, err = tx.AppendAudit(ctx, domain.AuditEvent{ActorID: actor.UserID, ActorRole: actor.Role,
			Action: "booking.check_in", ObjectType: "booking", ObjectID: repository.AuditObjectID(booking.ID),
			Result: "success", RequestID: actor.RequestID, CreatedAt: now})
		return err
	})
	return checkedIn, err
}
