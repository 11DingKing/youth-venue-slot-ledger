package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/database"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
)

func TestFailedDeliveryRemainsRetryableUntilHandlerSucceeds(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "worker-delivery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := repository.New(db)
	now := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	current := now
	jobID, err := store.EnqueueJob(ctx, "closure_compensation", `{"closure_id":41}`,
		"closure:41:compensate", now, now, 3)
	if err != nil {
		t.Fatal(err)
	}
	runner := New(store, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Millisecond,
		time.Minute, func() time.Time { return current })
	var calls atomic.Int32
	if err := runner.Register("closure_compensation", func(context.Context, domain.WorkerJob) error {
		if calls.Add(1) == 1 {
			return errors.New("booking cancellation unavailable")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	worked, err := runner.HandleOnce(ctx)
	if err != nil || !worked {
		t.Fatalf("first HandleOnce() = worked %v, error %v", worked, err)
	}
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM worker_jobs WHERE id = ?`, jobID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(domain.JobRetry) {
		t.Fatalf("job status after failed delivery = %s, want retry", status)
	}

	current = now.Add(2 * time.Second)
	worked, err = runner.HandleOnce(ctx)
	if err != nil || !worked || calls.Load() != 2 {
		t.Fatalf("retry HandleOnce() = worked %v, error %v, calls %d", worked, err, calls.Load())
	}
	if err := db.QueryRowContext(ctx, `SELECT status FROM worker_jobs WHERE id = ?`, jobID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(domain.JobSucceeded) {
		t.Fatalf("job status after successful retry = %s, want succeeded", status)
	}
}
