package worker

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
)

func TestWorkerCancellationDoesNotAbandonRunningHandler(t *testing.T) {
	store, closeDB := workerTestStore(t)
	defer closeDB()
	now := time.Date(2026, time.August, 24, 14, 0, 0, 0, time.UTC)
	runner := New(store, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Millisecond, time.Minute, func() time.Time { return now })
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	if err := runner.Register("owned", func(context.Context, domain.WorkerJob) error {
		close(started)
		<-release
		close(finished)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueJob(context.Background(), "owned", `{}`, "owned-1", now, now, 3); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan struct {
		worked bool
		err    error
	}, 1)
	go func() {
		worked, err := runner.HandleOnce(ctx)
		result <- struct {
			worked bool
			err    error
		}{worked: worked, err: err}
	}()
	<-started
	cancel()

	select {
	case got := <-result:
		t.Fatalf("HandleOnce returned before handler released: worked=%v err=%v", got.worked, got.err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	<-finished
	select {
	case got := <-result:
		if !got.worked {
			t.Fatalf("HandleOnce worked = false after claimed handler")
		}
	case <-time.After(time.Second):
		t.Fatal("HandleOnce did not return after handler released")
	}
}
