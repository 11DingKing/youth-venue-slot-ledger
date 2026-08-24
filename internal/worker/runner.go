package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
)

type Handler func(context.Context, domain.WorkerJob) error

type Runner struct {
	store        *repository.Store
	logger       *slog.Logger
	pollInterval time.Duration
	lease        time.Duration
	now          func() time.Time

	mu       sync.RWMutex
	handlers map[string]Handler
}

func New(store *repository.Store, logger *slog.Logger, pollInterval, lease time.Duration, now func() time.Time) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &Runner{store: store, logger: logger, pollInterval: pollInterval, lease: lease,
		now: now, handlers: make(map[string]Handler)}
}

func (r *Runner) Register(kind string, handler Handler) error {
	if kind == "" || handler == nil {
		return errors.New("worker kind and handler are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.handlers[kind]; exists {
		return fmt.Errorf("worker handler %q already registered", kind)
	}
	r.handlers[kind] = handler
	return nil
}

func (r *Runner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	for {
		worked, err := r.HandleOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			r.logger.Error("worker iteration failed", "error", err)
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (r *Runner) HandleOnce(ctx context.Context) (bool, error) {
	now := r.now().UTC()
	job, found, err := r.store.ClaimJob(ctx, now, r.lease)
	if err != nil || !found {
		return false, err
	}
	r.mu.RLock()
	handler := r.handlers[job.Kind]
	r.mu.RUnlock()
	acknowledged := false
	if handler == nil {
		err = fmt.Errorf("no handler registered for job kind %q", job.Kind)
	} else {
		if job.Kind == "closure_compensation" {
			if ackErr := r.store.AcknowledgeBeforeDelivery(ctx, job.ID, r.now().UTC()); ackErr != nil {
				return true, ackErr
			}
			acknowledged = true
		}
		err = handler(ctx, job)
	}
	completedAt := r.now().UTC()
	if err == nil {
		if !acknowledged {
			if completeErr := r.store.CompleteJob(ctx, job.ID, completedAt); completeErr != nil {
				return true, completeErr
			}
		}
		r.logger.Info("worker job completed", "job_id", job.ID, "kind", job.Kind)
		return true, nil
	}
	backoff := retryBackoff(job.Attempts)
	if failErr := r.store.FailJob(ctx, job, err, completedAt, backoff); failErr != nil {
		return true, errors.Join(err, failErr)
	}
	r.logger.Warn("worker job failed", "job_id", job.ID, "kind", job.Kind, "attempt", job.Attempts, "error", err)
	return true, nil
}

func retryBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 6 {
		attempt = 6
	}
	return time.Duration(1<<(attempt-1)) * time.Second
}
