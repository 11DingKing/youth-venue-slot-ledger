package closure

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
)

type Service struct {
	store       *repository.Store
	now         func() time.Time
	maxAttempts int
}

func New(store *repository.Store, now func() time.Time, maxAttempts int) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, now: now, maxAttempts: maxAttempts}
}

type CreateRequest struct {
	VenueID  int64     `json:"venue_id"`
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
	Reason   string    `json:"reason"`
}

func (s *Service) CreateAndApply(ctx context.Context, actor domain.Actor, request CreateRequest) (repository.Closure, error) {
	if err := actor.Require(domain.RoleOperator); err != nil {
		return repository.Closure{}, err
	}
	now := s.now().UTC()
	var applied repository.Closure
	var compensationPayload string
	err := s.store.WithTx(ctx, func(tx *repository.Store) error {
		created, err := tx.CreateClosure(ctx, repository.Closure{VenueID: request.VenueID,
			StartsAt: request.StartsAt.UTC(), EndsAt: request.EndsAt.UTC(), Reason: request.Reason,
			Status: domain.ClosurePlanned, CreatedBy: actor.UserID, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			return err
		}
		if _, err := tx.CloseSlotsForWindow(ctx, request.VenueID, request.StartsAt, request.EndsAt); err != nil {
			return err
		}
		applied, err = tx.TransitionClosure(ctx, created.ID, created.Version, domain.ClosurePlanned, domain.ClosureApplied, now)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]int64{"closure_id": applied.ID})
		if err != nil {
			return err
		}
		compensationPayload = string(payload)
		_, err = tx.AppendAudit(ctx, domain.AuditEvent{ActorID: actor.UserID, ActorRole: actor.Role,
			Action: "closure.apply", ObjectType: "closure", ObjectID: repository.AuditObjectID(applied.ID),
			Result: "success", RequestID: actor.RequestID, Metadata: map[string]string{"reason": request.Reason}, CreatedAt: now})
		return err
	})
	if err != nil {
		return applied, err
	}
	_, err = s.store.EnqueueDurableJob(ctx, "closure_compensation", compensationPayload,
		fmt.Sprintf("closure:%d:compensate", applied.ID), now, now, s.maxAttempts)
	return applied, err
}

func (s *Service) Reopen(ctx context.Context, actor domain.Actor, id, version int64) (repository.Closure, error) {
	if err := actor.Require(domain.RoleOperator); err != nil {
		return repository.Closure{}, err
	}
	now := s.now().UTC()
	var reopened repository.Closure
	err := s.store.WithTx(ctx, func(tx *repository.Store) error {
		closure, err := tx.ClosureByID(ctx, id)
		if err != nil {
			return err
		}
		if closure.Version != version || closure.Status != domain.ClosureApplied {
			return domain.ErrVersionConflict
		}
		reopened, err = tx.TransitionClosure(ctx, id, version, domain.ClosureApplied, domain.ClosureReopened, now)
		if err != nil {
			return err
		}
		if _, err := tx.ReopenSlotsForWindow(ctx, closure.VenueID, closure.StartsAt, closure.EndsAt); err != nil {
			return err
		}
		_, err = tx.AppendAudit(ctx, domain.AuditEvent{ActorID: actor.UserID, ActorRole: actor.Role,
			Action: "closure.reopen", ObjectType: "closure", ObjectID: repository.AuditObjectID(id),
			Result: "success", RequestID: actor.RequestID, CreatedAt: now})
		return err
	})
	return reopened, err
}
