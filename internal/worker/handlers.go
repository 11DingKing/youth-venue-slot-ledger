package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/booking"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
)

type ClosureHandlers struct {
	store    *repository.Store
	bookings *booking.Service
}

func NewClosureHandlers(store *repository.Store, bookings *booking.Service) *ClosureHandlers {
	return &ClosureHandlers{store: store, bookings: bookings}
}

func (h *ClosureHandlers) Compensation(ctx context.Context, job domain.WorkerJob) error {
	var payload struct {
		ClosureID int64 `json:"closure_id"`
	}
	if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
		return fmt.Errorf("decode closure compensation job: %w", err)
	}
	closure, err := h.store.ClosureByID(ctx, payload.ClosureID)
	if err != nil {
		return err
	}
	if closure.Status != domain.ClosureApplied {
		return nil
	}
	for {
		bookingIDs, err := h.store.BookingIDsAffectedByClosure(ctx, closure.VenueID, closure.StartsAt, closure.EndsAt, 100)
		if err != nil {
			return err
		}
		if len(bookingIDs) == 0 {
			return nil
		}
		for _, bookingID := range bookingIDs {
			booking, err := h.store.BookingByID(ctx, bookingID)
			if err != nil {
				return err
			}
			actor := domain.Actor{UserID: closure.CreatedBy, Role: domain.RoleOperator,
				RequestID: fmt.Sprintf("worker:%d:booking:%d", job.ID, bookingID)}
			if _, err := h.bookings.Cancel(ctx, actor, booking.ID, booking.Version, "temporary venue closure"); err != nil {
				return fmt.Errorf("compensate booking %d: %w", bookingID, err)
			}
		}
	}
}
