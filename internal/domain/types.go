package domain

import (
	"fmt"
	"strings"
	"time"
)

type Role string

const (
	RoleStudent  Role = "student"
	RoleGuardian Role = "guardian"
	RoleOperator Role = "operator"
	RoleCoach    Role = "coach"
)

func (r Role) Valid() bool {
	switch r {
	case RoleStudent, RoleGuardian, RoleOperator, RoleCoach:
		return true
	default:
		return false
	}
}

type User struct {
	ID           int64      `json:"id"`
	Email        string     `json:"email"`
	PasswordHash string     `json:"-"`
	Name         string     `json:"name"`
	Role         Role       `json:"role"`
	BirthDate    *time.Time `json:"birth_date,omitempty"`
	AbilityLevel int        `json:"ability_level"`
	Active       bool       `json:"active"`
	CreatedAt    time.Time  `json:"created_at"`
}

type Session struct {
	ID        int64      `json:"id"`
	UserID    int64      `json:"user_id"`
	TokenHash string     `json:"-"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type GuardianAuthorization struct {
	ID         int64      `json:"id"`
	GuardianID int64      `json:"guardian_id"`
	StudentID  int64      `json:"student_id"`
	ValidFrom  time.Time  `json:"valid_from"`
	ValidUntil time.Time  `json:"valid_until"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type Venue struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	District string `json:"district"`
	Timezone string `json:"timezone"`
	Active   bool   `json:"active"`
}

type SlotStatus string

const (
	SlotOpen   SlotStatus = "open"
	SlotClosed SlotStatus = "closed"
)

type Slot struct {
	ID             int64      `json:"id"`
	VenueID        int64      `json:"venue_id"`
	Sport          string     `json:"sport"`
	StartsAt       time.Time  `json:"starts_at"`
	EndsAt         time.Time  `json:"ends_at"`
	Capacity       int        `json:"capacity"`
	Reserved       int        `json:"reserved"`
	MinimumAge     int        `json:"minimum_age"`
	MaximumAge     int        `json:"maximum_age"`
	MinimumAbility int        `json:"minimum_ability"`
	MaximumAbility int        `json:"maximum_ability"`
	Status         SlotStatus `json:"status"`
	Version        int64      `json:"version"`
}

func (s Slot) Validate() error {
	if s.VenueID <= 0 || strings.TrimSpace(s.Sport) == "" {
		return fmt.Errorf("%w: venue and sport are required", ErrValidation)
	}
	if !s.EndsAt.After(s.StartsAt) {
		return fmt.Errorf("%w: slot end must follow start", ErrValidation)
	}
	if s.Capacity < 1 || s.Reserved < 0 || s.Reserved > s.Capacity {
		return fmt.Errorf("%w: invalid capacity ledger", ErrValidation)
	}
	if s.MinimumAge < 0 || s.MaximumAge < s.MinimumAge {
		return fmt.Errorf("%w: invalid age range", ErrValidation)
	}
	if s.MinimumAbility < 0 || s.MaximumAbility < s.MinimumAbility {
		return fmt.Errorf("%w: invalid ability range", ErrValidation)
	}
	return nil
}

type BookingStatus string

const (
	BookingHeld       BookingStatus = "held"
	BookingConfirmed  BookingStatus = "confirmed"
	BookingWaitlisted BookingStatus = "waitlisted"
	BookingCancelled  BookingStatus = "cancelled"
	BookingCheckedIn  BookingStatus = "checked_in"
	BookingNoShow     BookingStatus = "no_show"
)

type Booking struct {
	ID                 int64         `json:"id"`
	StudentID          int64         `json:"student_id"`
	GuardianID         int64         `json:"guardian_id"`
	SlotID             int64         `json:"slot_id"`
	Status             BookingStatus `json:"status"`
	HoldExpiresAt      *time.Time    `json:"hold_expires_at,omitempty"`
	Version            int64         `json:"version"`
	CreatedAt          time.Time     `json:"created_at"`
	UpdatedAt          time.Time     `json:"updated_at"`
	CancellationReason string        `json:"cancellation_reason,omitempty"`
}

type LedgerKind string

const (
	LedgerReserve     LedgerKind = "reserve"
	LedgerRelease     LedgerKind = "release"
	LedgerAdjustment  LedgerKind = "adjustment"
	LedgerTransferIn  LedgerKind = "transfer_in"
	LedgerTransferOut LedgerKind = "transfer_out"
)

type LedgerEntry struct {
	ID            int64      `json:"id"`
	SlotID        int64      `json:"slot_id"`
	BookingID     *int64     `json:"booking_id,omitempty"`
	Kind          LedgerKind `json:"kind"`
	Delta         int        `json:"delta"`
	BalanceAfter  int        `json:"balance_after"`
	Reason        string     `json:"reason"`
	CorrelationID string     `json:"correlation_id"`
	CreatedAt     time.Time  `json:"created_at"`
}

type AuditEvent struct {
	ID         int64             `json:"id"`
	ActorID    int64             `json:"actor_id"`
	ActorRole  Role              `json:"actor_role"`
	Action     string            `json:"action"`
	ObjectType string            `json:"object_type"`
	ObjectID   string            `json:"object_id"`
	Result     string            `json:"result"`
	RequestID  string            `json:"request_id"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
}

type Actor struct {
	UserID    int64
	Role      Role
	RequestID string
}

func (a Actor) Require(roles ...Role) error {
	for _, role := range roles {
		if a.Role == role {
			return nil
		}
	}
	return fmt.Errorf("%w: role %s is not allowed", ErrForbidden, a.Role)
}
