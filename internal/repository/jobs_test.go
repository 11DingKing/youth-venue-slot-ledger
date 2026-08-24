package repository

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/database"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
)

func TestJobLeaseRetryAndCompletion(t *testing.T) {
	ctx := context.Background()
	store, db := newTestStore(t)
	defer db.Close()
	now := time.Date(2026, time.August, 24, 9, 0, 0, 0, time.UTC)
	id, err := store.EnqueueJob(ctx, "closure_compensation", `{"closure_id":1}`, "closure:1", now, now, 3)
	if err != nil || id == 0 {
		t.Fatalf("EnqueueJob() = %d, %v", id, err)
	}
	duplicateID, err := store.EnqueueJob(ctx, "closure_compensation", `{"closure_id":1}`, "closure:1", now, now, 3)
	if err != nil || duplicateID != id {
		t.Fatalf("deduplicated job = %d, %v; want %d", duplicateID, err, id)
	}
	job, found, err := store.ClaimJob(ctx, now, 30*time.Second)
	if err != nil || !found {
		t.Fatalf("ClaimJob() = %+v, %v, %v", job, found, err)
	}
	if job.Status != domain.JobRunning || job.Attempts != 1 || job.LeaseUntil == nil {
		t.Fatalf("claimed job = %+v", job)
	}
	if _, found, err := store.ClaimJob(ctx, now.Add(10*time.Second), 30*time.Second); err != nil || found {
		t.Fatalf("active lease claim = %v, %v", found, err)
	}
	if err := store.FailJob(ctx, job, errors.New("temporary"), now.Add(10*time.Second), time.Minute); err != nil {
		t.Fatalf("FailJob() error: %v", err)
	}
	if _, found, err := store.ClaimJob(ctx, now.Add(30*time.Second), 30*time.Second); err != nil || found {
		t.Fatalf("job claimed before backoff = %v, %v", found, err)
	}
	retry, found, err := store.ClaimJob(ctx, now.Add(71*time.Second), 30*time.Second)
	if err != nil || !found || retry.Attempts != 2 {
		t.Fatalf("retry claim = %+v, %v, %v", retry, found, err)
	}
	if err := store.CompleteJob(ctx, retry.ID, now.Add(72*time.Second)); err != nil {
		t.Fatalf("CompleteJob() error: %v", err)
	}
	if _, found, err := store.ClaimJob(ctx, now.Add(2*time.Hour), 30*time.Second); err != nil || found {
		t.Fatalf("completed job reclaimed = %v, %v", found, err)
	}
}

func TestExpiredLeaseIsRecoveredAfterRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "restart.db")
	db, err := database.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	store := New(db)
	now := time.Now().UTC()
	_, err = store.EnqueueJob(ctx, "recovery", `{}`, "recovery-1", now, now, 2)
	if err != nil {
		t.Fatal(err)
	}
	first, found, err := store.ClaimJob(ctx, now, time.Second)
	if err != nil || !found {
		t.Fatalf("first claim = %+v, %v, %v", first, found, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopenedDB, err := database.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopenedDB.Close()
	restartedStore := New(reopenedDB)
	if _, found, err := restartedStore.ClaimJob(ctx, now.Add(500*time.Millisecond), time.Second); err != nil || found {
		t.Fatalf("unexpired lease after restart = %v, %v", found, err)
	}
	recovered, found, err := restartedStore.ClaimJob(ctx, now.Add(2*time.Second), time.Second)
	if err != nil || !found {
		t.Fatalf("expired lease recovery = %+v, %v, %v", recovered, found, err)
	}
	if recovered.ID != first.ID || recovered.Attempts != 2 {
		t.Fatalf("recovered job = %+v, want ID %d and attempt 2", recovered, first.ID)
	}
}

func TestDeadJobAfterMaximumAttempts(t *testing.T) {
	ctx := context.Background()
	store, db := newTestStore(t)
	defer db.Close()
	now := time.Now().UTC()
	_, _ = store.EnqueueJob(ctx, "always_fail", `{}`, "dead-1", now, now, 1)
	job, found, err := store.ClaimJob(ctx, now, time.Second)
	if err != nil || !found {
		t.Fatalf("claim = %+v, %v, %v", job, found, err)
	}
	if err := store.FailJob(ctx, job, errors.New("permanent"), now, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.ClaimJob(ctx, now.Add(time.Hour), time.Second); err != nil || found {
		t.Fatalf("dead job claim = %v, %v", found, err)
	}
}
