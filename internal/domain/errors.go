package domain

import "errors"

var (
	ErrNotFound         = errors.New("not found")
	ErrConflict         = errors.New("conflict")
	ErrValidation       = errors.New("validation failed")
	ErrUnauthorized     = errors.New("unauthorized")
	ErrForbidden        = errors.New("forbidden")
	ErrCapacity         = errors.New("slot capacity unavailable")
	ErrInvalidState     = errors.New("invalid state transition")
	ErrVersionConflict  = errors.New("version conflict")
	ErrExpired          = errors.New("expired")
	ErrIdempotencyReuse = errors.New("idempotency key reused with different request")
	ErrCoachCoverage    = errors.New("coach coverage unavailable")
	ErrGuardianRequired = errors.New("active guardian authorization required")
	ErrEligibility      = errors.New("student is not eligible for slot group")
)
