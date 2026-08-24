package audit

import (
	"context"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
)

type Service struct{ store *repository.Store }

func New(store *repository.Store) *Service { return &Service{store: store} }

func (s *Service) List(ctx context.Context, actor domain.Actor, objectType, objectID string, limit, offset int) ([]domain.AuditEvent, error) {
	if err := actor.Require(domain.RoleOperator); err != nil {
		return nil, err
	}
	return s.store.ListAuditEvents(ctx, objectType, objectID, limit, offset)
}
