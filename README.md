# Youth Venue Slot Ledger

Youth Venue Slot Ledger is a production-oriented Go backend for a city's summer youth sports program. It coordinates students, guardians, venue operators and coaches across venue schedules, age and ability groups, coach coverage, guardian authorization, booking holds, waitlists, check-ins, temporary closures and cross-venue rescheduling.

The service uses SQLite as a real relational database. Versioned embedded migrations create the schema at startup. Capacity changes, booking transitions, ledger entries, audit events, waitlist promotions and durable job creation are committed in explicit transactions. Conditional version updates and database constraints protect capacity and state under concurrent requests.

## Run

Go 1.26 or newer is required.

```sh
cp .env.example .env
go run ./cmd/server
```

Configuration is read from environment variables. The default server listens on `:8080`, stores data in `venue-slot.db`, and creates `operator@example.test` with password `change-me-now` when no operator exists. Production deployments must override the bootstrap credentials.

```sh
HTTP_ADDR=:8080 \
DB_PATH=/tmp/venue-slot.db \
BOOTSTRAP_OPERATOR_PASSWORD='a-long-unique-password' \
go run ./cmd/server
```

## Authentication

`POST /v1/auth/login` accepts an email and password and returns an opaque token. The server stores only a SHA-256 digest of the token. Protected requests send `Authorization: Bearer <token>`. Logout revokes the server-side session immediately, and expired sessions are rejected and cleaned by a durable worker.

The business roles are `student`, `guardian`, `operator`, and `coach`. Operators provision users, guardian authorizations, venues, slots and coach coverage. Guardians create and confirm bookings for authorized students. Students and guardians can inspect their booking lifecycle. Coaches and operators redeem check-ins. Operator-only mutations require auditable reasons where applicable.

## Core API

- `GET /healthz` and `GET /readyz`
- `POST /v1/auth/login`, `POST /v1/auth/logout`, `GET /v1/auth/me`
- `POST /v1/admin/users`, `POST /v1/admin/guardian-authorizations`
- `GET|POST /v1/venues`, `GET /v1/venues/{id}/slots`, `POST /v1/slots`
- `POST /v1/bookings`, `GET /v1/bookings`
- `POST /v1/bookings/{id}/confirm`, `/cancel`, `/reschedule`, `/check-in`
- `POST /v1/slots/{id}/capacity-adjustments`
- `POST /v1/closures`, `POST /v1/closures/{id}/reopen`
- `GET /v1/audit-events`

All errors use a stable JSON envelope with an error code, user-facing message, and request ID. Clients may supply `X-Request-ID`; otherwise the server generates one. Booking creation requires an `Idempotency-Key` header scoped to the actor, HTTP method and route.

## Persistence and recovery

The schema contains users, sessions, guardian authorizations, venues, coaches, slots, coach assignments, bookings, waitlists, check-ins, closures, idempotency keys, ledger entries, audit events and worker jobs. Foreign keys, partial unique indexes and status constraints enforce core invariants.

Temporary closure compensation is represented by a persistent worker job. Workers claim jobs with a lease and conditional update, retry with bounded exponential backoff, reclaim expired leases after restart, and retain terminal failures. Closure compensation uses normal booking cancellation transactions so released seats, booking status, audit events and ledger balances cannot diverge.

## Verification

```sh
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build ./...
```

Build and run the image with its default entrypoint:

```sh
docker build --platform linux/amd64 -t youth-venue-slot-ledger:amd64 .
docker run --rm -p 8080:8080 youth-venue-slot-ledger:amd64
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
```

