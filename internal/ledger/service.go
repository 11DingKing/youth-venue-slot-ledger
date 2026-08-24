package ledger

import (
	"context"
	"fmt"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
)

type Service struct{ store *repository.Store }

func New(store *repository.Store) *Service { return &Service{store: store} }

type Reconciliation struct {
	SlotID        int64 `json:"slot_id"`
	Reserved      int   `json:"reserved"`
	LedgerBalance int   `json:"ledger_balance"`
	Consistent    bool  `json:"consistent"`
}

func (s *Service) Reconcile(ctx context.Context, actor domain.Actor, slotID int64) (Reconciliation, error) {
	if err := actor.Require(domain.RoleOperator); err != nil {
		return Reconciliation{}, err
	}
	slot, err := s.store.SlotByID(ctx, slotID)
	if err != nil {
		return Reconciliation{}, err
	}
	balance, err := s.store.LedgerBalance(ctx, slotID)
	if err != nil {
		return Reconciliation{}, err
	}
	result := Reconciliation{SlotID: slotID, Reserved: slot.Reserved, LedgerBalance: balance, Consistent: slot.Reserved == balance}
	if !result.Consistent {
		return result, fmt.Errorf("%w: slot reservation balance differs from ledger", domain.ErrConflict)
	}
	return result, nil
}
