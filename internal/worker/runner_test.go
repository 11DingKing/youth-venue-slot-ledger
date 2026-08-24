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

func TestRunnerCompletesRegisteredJob(t *testing.T) {
	store, closeDB := workerTestStore(t)
	defer closeDB()
	now := time.Date(2026, time.August, 24, 11, 0, 0, 0, time.UTC)
	runner := New(store, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Millisecond, time.Minute, func() time.Time { return now })
	var calls atomic.Int32
	if err := runner.Register("success", func(ctx context.Context, job domain.WorkerJob) error {
		calls.Add(1)
		if job.Payload != `{"value":1}` || job.Attempts != 1 {
			t.Fatalf("handler job = %+v", job)
		}
		return ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueJob(context.Background(), "success", `{"value":1}`, "success-1", now, now, 3); err != nil {
		t.Fatal(err)
	}
	worked, err := runner.HandleOnce(context.Background())
	if err != nil || !worked || calls.Load() != 1 {
		t.Fatalf("HandleOnce() = %v, %v; calls = %d", worked, err, calls.Load())
	}
	worked, err = runner.HandleOnce(context.Background())
	if err != nil || worked {
		t.Fatalf("empty HandleOnce() = %v, %v", worked, err)
	}
}

func TestRunnerRetriesFailureAndRejectsDuplicateHandler(t *testing.T) {
	store, closeDB := workerTestStore(t)
	defer closeDB()
	now := time.Now().UTC()
	current := now
	runner := New(store, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Millisecond, time.Second, func() time.Time { return current })
	handler := func(context.Context, domain.WorkerJob) error { return errors.New("temporary failure") }
	if err := runner.Register("failure", handler); err != nil {
		t.Fatal(err)
	}
	if err := runner.Register("failure", handler); err == nil {
		t.Fatal("duplicate handler registration succeeded")
	}
	if err := runner.Register("", handler); err == nil {
		t.Fatal("empty handler kind succeeded")
	}
	if _, err := store.EnqueueJob(context.Background(), "failure", `{}`, "failure-1", now, now, 2); err != nil {
		t.Fatal(err)
	}
	worked, err := runner.HandleOnce(context.Background())
	if err != nil || !worked {
		t.Fatalf("failed job iteration = %v, %v", worked, err)
	}
	current = now.Add(2 * time.Second)
	worked, err = runner.HandleOnce(context.Background())
	if err != nil || !worked {
		t.Fatalf("retry iteration = %v, %v", worked, err)
	}
	current = now.Add(time.Hour)
	worked, err = runner.HandleOnce(context.Background())
	if err != nil || worked {
		t.Fatalf("dead job iteration = %v, %v", worked, err)
	}
}

func TestRetryBackoffIsBounded(t *testing.T) {
	t.Parallel()
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: -1, want: time.Second},
		{attempt: 0, want: time.Second},
		{attempt: 1, want: time.Second},
		{attempt: 2, want: 2 * time.Second},
		{attempt: 3, want: 4 * time.Second},
		{attempt: 6, want: 32 * time.Second},
		{attempt: 10, want: 32 * time.Second},
	}
	for _, test := range tests {
		if got := retryBackoff(test.attempt); got != test.want {
			t.Errorf("retryBackoff(%d) = %v, want %v", test.attempt, got, test.want)
		}
	}
}

func workerTestStore(t *testing.T) (*repository.Store, func()) {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "worker.db"))
	if err != nil {
		t.Fatal(err)
	}
	return repository.New(db), func() { _ = db.Close() }
}
