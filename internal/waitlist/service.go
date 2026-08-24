package waitlist

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
)

type Service struct {
	store *repository.Store
	now   func() time.Time
	hold  time.Duration
}

func New(store *repository.Store, now func() time.Time, hold time.Duration) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, now: now, hold: hold}
}

func (s *Service) PromoteNext(ctx context.Context, actor domain.Actor, slotID int64) (domain.Booking, bool, error) {
	now := s.now().UTC()
	var promoted domain.Booking
	booking, err := s.store.NextWaitlistedBooking(ctx, slotID)
	if errors.Is(err, domain.ErrNotFound) {
		return promoted, false, nil
	}
	if err != nil {
		return promoted, false, err
	}
	slot, err := s.store.ReserveWaitlistSeat(ctx, slotID)
	if err != nil {
		return promoted, false, err
	}
	err = s.store.WithTx(ctx, func(tx *repository.Store) error {
		promoted, err = tx.PromoteWaitlistedBooking(ctx, booking.ID, booking.Version, now.Add(s.hold), now)
		if err != nil {
			return err
		}
		if err := tx.MarkWaitlistPromoted(ctx, booking.ID, now); err != nil {
			return err
		}
		_, err = tx.AppendLedger(ctx, domain.LedgerEntry{SlotID: slot.ID, BookingID: &booking.ID,
			Kind: domain.LedgerReserve, Delta: 1, BalanceAfter: slot.Reserved, Reason: "waitlist promotion",
			CorrelationID: fmt.Sprintf("%s:waitlist:%d", actor.RequestID, booking.ID), CreatedAt: now})
		if err != nil {
			return err
		}
		_, err = tx.AppendAudit(ctx, domain.AuditEvent{ActorID: actor.UserID, ActorRole: actor.Role,
			Action: "waitlist.promote", ObjectType: "booking", ObjectID: repository.AuditObjectID(booking.ID),
			Result: "success", RequestID: actor.RequestID, CreatedAt: now})
		return err
	})
	return promoted, promoted.ID != 0, err
}
