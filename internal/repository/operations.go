package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
)

type IdempotencyRecord struct {
	RequestHash  string
	ResourceType string
	ResourceID   int64
	ExpiresAt    time.Time
}

func (s *Store) IdempotencyRecord(ctx context.Context, actorID int64, method, path, key string, now time.Time) (IdempotencyRecord, bool, error) {
	var record IdempotencyRecord
	var expires string
	err := s.q.QueryRowContext(ctx, `SELECT request_hash, resource_type, resource_id, expires_at
		FROM idempotency_keys WHERE actor_id = ? AND method = ? AND path = ? AND key = ? AND expires_at > ?`,
		actorID, method, path, key, utc(now)).Scan(&record.RequestHash, &record.ResourceType, &record.ResourceID, &expires)
	if err == sql.ErrNoRows {
		return IdempotencyRecord{}, false, nil
	}
	if err != nil {
		return IdempotencyRecord{}, false, fmt.Errorf("find idempotency key: %w", err)
	}
	record.ExpiresAt, err = parseTime(expires)
	return record, true, err
}

func (s *Store) SaveIdempotencyRecord(ctx context.Context, actorID int64, method, path, key, requestHash, resourceType string, resourceID int64, expiresAt, now time.Time) error {
	_, err := s.q.ExecContext(ctx, `INSERT INTO idempotency_keys
		(actor_id, method, path, key, request_hash, resource_type, resource_id, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, actorID, method, path, key, requestHash,
		resourceType, resourceID, utc(expiresAt), utc(now))
	if err != nil {
		if isUniqueConstraint(err) {
			return fmt.Errorf("%w: idempotency key", domain.ErrConflict)
		}
		return fmt.Errorf("save idempotency key: %w", err)
	}
	return nil
}

func (s *Store) AppendLedger(ctx context.Context, entry domain.LedgerEntry) (domain.LedgerEntry, error) {
	var bookingID any
	if entry.BookingID != nil {
		bookingID = *entry.BookingID
	}
	result, err := s.q.ExecContext(ctx, `INSERT INTO ledger_entries
		(slot_id, booking_id, kind, delta, balance_after, reason, correlation_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, entry.SlotID, bookingID, entry.Kind, entry.Delta,
		entry.BalanceAfter, entry.Reason, entry.CorrelationID, utc(entry.CreatedAt))
	if err != nil {
		return domain.LedgerEntry{}, fmt.Errorf("append ledger: %w", err)
	}
	entry.ID, err = result.LastInsertId()
	return entry, err
}

func (s *Store) LedgerBalance(ctx context.Context, slotID int64) (int, error) {
	var balance int
	err := s.q.QueryRowContext(ctx, `SELECT COALESCE(SUM(delta),0) FROM ledger_entries WHERE slot_id = ?`, slotID).Scan(&balance)
	if err != nil {
		return 0, fmt.Errorf("ledger balance: %w", err)
	}
	return balance, nil
}

func (s *Store) AppendAudit(ctx context.Context, event domain.AuditEvent) (domain.AuditEvent, error) {
	metadata, err := json.Marshal(event.Metadata)
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("encode audit metadata: %w", err)
	}
	result, err := s.q.ExecContext(ctx, `INSERT INTO audit_events
		(actor_id, actor_role, action, object_type, object_id, result, request_id, metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.ActorID, event.ActorRole, event.Action,
		event.ObjectType, event.ObjectID, event.Result, event.RequestID, string(metadata), utc(event.CreatedAt))
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("append audit event: %w", err)
	}
	event.ID, err = result.LastInsertId()
	return event, err
}

func (s *Store) ListAuditEvents(ctx context.Context, objectType, objectID string, limit, offset int) ([]domain.AuditEvent, error) {
	query := `SELECT id, actor_id, actor_role, action, object_type, object_id, result,
		request_id, metadata_json, created_at FROM audit_events WHERE 1=1`
	var args []any
	if objectType != "" {
		query += " AND object_type = ?"
		args = append(args, objectType)
	}
	if objectID != "" {
		query += " AND object_id = ?"
		args = append(args, objectID)
	}
	query += " ORDER BY id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := s.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()
	var events []domain.AuditEvent
	for rows.Next() {
		var event domain.AuditEvent
		var role, metadata, created string
		if err := rows.Scan(&event.ID, &event.ActorID, &role, &event.Action, &event.ObjectType,
			&event.ObjectID, &event.Result, &event.RequestID, &metadata, &created); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		event.ActorRole = domain.Role(role)
		if err := json.Unmarshal([]byte(metadata), &event.Metadata); err != nil {
			return nil, fmt.Errorf("decode audit metadata: %w", err)
		}
		event.CreatedAt, err = parseTime(created)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func AuditObjectID(id int64) string { return strconv.FormatInt(id, 10) }
