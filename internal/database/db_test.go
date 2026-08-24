package database

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAppliesAllMigrationsAndIsRepeatable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "nested", "venue slot.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	assertSchema(t, db)
	if err := Ready(ctx, db); err != nil {
		t.Fatalf("Ready() error: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer reopened.Close()
	assertSchema(t, reopened)
	var migrations int
	if err := reopened.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&migrations); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if migrations != 1 {
		t.Fatalf("migration count = %d, want 1", migrations)
	}
}

func TestForeignKeysAndChecksAreEnabled(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "constraints.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.ExecContext(ctx, `INSERT INTO slots
		(venue_id, sport, starts_at, ends_at, capacity, reserved, minimum_age, maximum_age,
		minimum_ability, maximum_ability, status, version)
		VALUES (999, 'swimming', '2026-08-01T01:00:00Z', '2026-08-01T02:00:00Z', 10, 0, 8, 15, 0, 5, 'open', 1)`)
	if err == nil {
		t.Fatal("foreign key violation unexpectedly succeeded")
	}

	_, err = db.ExecContext(ctx, `INSERT INTO venues(name, district, timezone, active)
		VALUES ('North Pool', 'North', 'Asia/Shanghai', 1)`)
	if err != nil {
		t.Fatalf("insert venue: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO slots
		(venue_id, sport, starts_at, ends_at, capacity, reserved, minimum_age, maximum_age,
		minimum_ability, maximum_ability, status, version)
		VALUES (1, 'swimming', '2026-08-01T01:00:00Z', '2026-08-01T02:00:00Z', 2, 3, 8, 15, 0, 5, 'open', 1)`)
	if err == nil {
		t.Fatal("reserved greater than capacity unexpectedly succeeded")
	}
}

func TestMigrationRollsBackFailedStatements(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "rollback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = applyOne(ctx, db, "broken.sql", `
		CREATE TABLE should_rollback (id INTEGER PRIMARY KEY);
		INSERT INTO missing_table(id) VALUES (1);
	`)
	if err == nil {
		t.Fatal("broken migration unexpectedly succeeded")
	}
	var name string
	err = db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name='should_rollback'`).Scan(&name)
	if err != sql.ErrNoRows {
		t.Fatalf("rolled-back table lookup error = %v, want sql.ErrNoRows", err)
	}
}

func TestInMemoryDatabase(t *testing.T) {
	db, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:) error: %v", err)
	}
	defer db.Close()
	assertSchema(t, db)
}

func assertSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got[name] = true
	}
	for _, table := range []string{
		"users", "sessions", "guardian_authorizations", "venues", "coaches", "slots",
		"coach_assignments", "bookings", "waitlist_entries", "checkins", "closures",
		"idempotency_keys", "ledger_entries", "audit_events", "worker_jobs",
	} {
		if !got[table] {
			t.Errorf("missing table %q", table)
		}
	}
}
