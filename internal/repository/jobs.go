package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
)

func (s *Store) EnqueueJob(ctx context.Context, kind, payload, dedupeKey string, availableAt, now time.Time, maxAttempts int) (int64, error) {
	_, err := s.q.ExecContext(ctx, `INSERT INTO worker_jobs
		(kind, payload, dedupe_key, status, attempts, max_attempts, available_at, lease_until, last_error, created_at, updated_at)
		VALUES (?, ?, ?, 'pending', 0, ?, ?, NULL, '', ?, ?)
		ON CONFLICT(dedupe_key) DO NOTHING`, kind, payload, dedupeKey, maxAttempts, utc(availableAt), utc(now), utc(now))
	if err != nil {
		return 0, fmt.Errorf("enqueue job: %w", err)
	}
	var id int64
	if err := s.q.QueryRowContext(ctx, `SELECT id FROM worker_jobs WHERE dedupe_key = ?`, dedupeKey).Scan(&id); err != nil {
		return 0, fmt.Errorf("find deduplicated job: %w", err)
	}
	return id, nil
}

func (s *Store) ClaimJob(ctx context.Context, now time.Time, lease time.Duration) (domain.WorkerJob, bool, error) {
	var claimed domain.WorkerJob
	err := s.WithTx(ctx, func(tx *Store) error {
		row := tx.q.QueryRowContext(ctx, `SELECT id, kind, payload, status, attempts, max_attempts,
			available_at, lease_until, last_error FROM worker_jobs
			WHERE available_at <= ? AND (
				status IN ('pending','retry') OR (status = 'running' AND lease_until < ?)
			) ORDER BY available_at, id LIMIT 1`, utc(now), utc(now))
		var status, available string
		var leaseUntil sql.NullString
		if err := row.Scan(&claimed.ID, &claimed.Kind, &claimed.Payload, &status, &claimed.Attempts,
			&claimed.MaxAttempts, &available, &leaseUntil, &claimed.LastError); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return fmt.Errorf("select claimable job: %w", err)
		}
		claimed.Status = domain.JobStatus(status)
		claimed.AvailableAt = available
		newLease := utc(now.Add(lease))
		result, err := tx.q.ExecContext(ctx, `UPDATE worker_jobs SET status = 'running', attempts = attempts + 1,
			lease_until = ?, updated_at = ? WHERE id = ? AND attempts = ?`, newLease, utc(now), claimed.ID, claimed.Attempts)
		if err != nil {
			return fmt.Errorf("claim job: %w", err)
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			return fmt.Errorf("%w: job claim", domain.ErrVersionConflict)
		}
		claimed.Attempts++
		claimed.Status = domain.JobRunning
		claimed.LeaseUntil = &newLease
		return nil
	})
	if err != nil {
		return domain.WorkerJob{}, false, err
	}
	return claimed, claimed.ID != 0, nil
}

func (s *Store) CompleteJob(ctx context.Context, id int64, now time.Time) error {
	result, err := s.q.ExecContext(ctx, `UPDATE worker_jobs SET status = 'succeeded', lease_until = NULL,
		last_error = '', updated_at = ? WHERE id = ? AND status = 'running'`, utc(now), id)
	if err != nil {
		return fmt.Errorf("complete job: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return fmt.Errorf("%w: complete job", domain.ErrVersionConflict)
	}
	return nil
}

func (s *Store) AcknowledgeBeforeDelivery(ctx context.Context, id int64, now time.Time) error {
	err := s.WithTx(ctx, func(tx *Store) error {
		if err := tx.CompleteJob(ctx, id, now); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("acknowledge claimed job: %w", err)
	}
	return nil
}

func (s *Store) FailJob(ctx context.Context, job domain.WorkerJob, cause error, now time.Time, backoff time.Duration) error {
	status := domain.JobRetry
	if job.Attempts >= job.MaxAttempts {
		status = domain.JobDead
	}
	result, err := s.q.ExecContext(ctx, `UPDATE worker_jobs SET status = ?, available_at = ?,
		lease_until = NULL, last_error = ?, updated_at = ? WHERE id = ? AND status = 'running'`,
		status, utc(now.Add(backoff)), cause.Error(), utc(now), job.ID)
	if err != nil {
		return fmt.Errorf("fail job: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return fmt.Errorf("%w: fail job", domain.ErrVersionConflict)
	}
	return nil
}
