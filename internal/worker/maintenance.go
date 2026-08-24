package worker

import (
	"context"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/auth"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
)

type SessionMaintenance struct{ auth *auth.Service }

func NewSessionMaintenance(service *auth.Service) *SessionMaintenance {
	return &SessionMaintenance{auth: service}
}

func (m *SessionMaintenance) RevokeExpired(ctx context.Context, _ domain.WorkerJob) error {
	_, err := m.auth.RevokeExpired(ctx)
	return err
}

func EnqueueSessionMaintenance(ctx context.Context, store interface {
	EnqueueJob(context.Context, string, string, string, time.Time, time.Time, int) (int64, error)
}, now time.Time, maxAttempts int) error {
	_, err := store.EnqueueJob(ctx, "session_maintenance", `{}`, "session-maintenance:"+now.UTC().Format("2006-01-02-15"), now, now, maxAttempts)
	return err
}
