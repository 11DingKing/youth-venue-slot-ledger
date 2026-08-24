package worker_test

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
	"github.com/11DingKing/youth-venue-slot-ledger/internal/worker"
)

func TestCancelledWorkerPollLeavesDurableJobForNextPoll(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "worker-cancel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := repository.New(db)
	now := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	if _, err := store.EnqueueJob(context.Background(), "cancel-aware", `{"booking_id":42}`,
		"cancel-aware-42", now, now, 3); err != nil {
		t.Fatalf("enqueue durable job: %v", err)
	}
	runner := worker.New(store, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Millisecond,
		time.Minute, func() time.Time { return now })
	var calls atomic.Int32
	if err := runner.Register("cancel-aware", func(ctx context.Context, job domain.WorkerJob) error {
		calls.Add(1)
		if job.Payload != `{"booking_id":42}` {
			t.Fatalf("handler payload = %s", job.Payload)
		}
		return ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	worked, err := runner.HandleOnce(cancelled)
	if worked || !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("cancelled HandleOnce() = worked %v, error %v, handler calls %d", worked, err, calls.Load())
	}
	worked, err = runner.HandleOnce(context.Background())
	if err != nil || !worked || calls.Load() != 1 {
		t.Fatalf("next HandleOnce() = worked %v, error %v, handler calls %d", worked, err, calls.Load())
	}
	worked, err = runner.HandleOnce(context.Background())
	if err != nil || worked || calls.Load() != 1 {
		t.Fatalf("completed queue HandleOnce() = worked %v, error %v, handler calls %d", worked, err, calls.Load())
	}
}
