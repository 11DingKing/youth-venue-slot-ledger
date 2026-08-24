package domain

import (
	"errors"
	"testing"
)

func TestRoleValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		role  Role
		valid bool
	}{
		{name: "student", role: RoleStudent, valid: true},
		{name: "guardian", role: RoleGuardian, valid: true},
		{name: "operator", role: RoleOperator, valid: true},
		{name: "coach", role: RoleCoach, valid: true},
		{name: "empty", role: "", valid: false},
		{name: "unknown", role: "administrator", valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := test.role.Valid(); got != test.valid {
				t.Fatalf("Role.Valid() = %v, want %v", got, test.valid)
			}
		})
	}
}

func TestActorRequire(t *testing.T) {
	t.Parallel()
	actor := Actor{UserID: 42, Role: RoleGuardian, RequestID: "request-1"}
	if err := actor.Require(RoleStudent, RoleGuardian); err != nil {
		t.Fatalf("guardian should be allowed: %v", err)
	}
	if err := actor.Require(RoleOperator, RoleCoach); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestBookingTransitions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		from BookingStatus
		to   BookingStatus
		ok   bool
	}{
		{name: "held confirmed", from: BookingHeld, to: BookingConfirmed, ok: true},
		{name: "held cancelled", from: BookingHeld, to: BookingCancelled, ok: true},
		{name: "held no show", from: BookingHeld, to: BookingNoShow, ok: true},
		{name: "confirmed check in", from: BookingConfirmed, to: BookingCheckedIn, ok: true},
		{name: "confirmed cancelled", from: BookingConfirmed, to: BookingCancelled, ok: true},
		{name: "confirmed no show", from: BookingConfirmed, to: BookingNoShow, ok: true},
		{name: "waitlisted held", from: BookingWaitlisted, to: BookingHeld, ok: true},
		{name: "waitlisted cancelled", from: BookingWaitlisted, to: BookingCancelled, ok: true},
		{name: "cancelled confirmed", from: BookingCancelled, to: BookingConfirmed, ok: false},
		{name: "checked in cancelled", from: BookingCheckedIn, to: BookingCancelled, ok: false},
		{name: "no show held", from: BookingNoShow, to: BookingHeld, ok: false},
		{name: "held checked in", from: BookingHeld, to: BookingCheckedIn, ok: false},
		{name: "same state", from: BookingConfirmed, to: BookingConfirmed, ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := CanTransitionBooking(test.from, test.to); got != test.ok {
				t.Fatalf("CanTransitionBooking(%q, %q) = %v, want %v", test.from, test.to, got, test.ok)
			}
			err := ValidateBookingTransition(test.from, test.to)
			if test.ok && err != nil {
				t.Fatalf("valid transition rejected: %v", err)
			}
			if !test.ok && !errors.Is(err, ErrInvalidState) {
				t.Fatalf("invalid transition error = %v, want ErrInvalidState", err)
			}
		})
	}
}

func TestClosureTransitions(t *testing.T) {
	t.Parallel()
	if err := ValidateClosureTransition(ClosurePlanned, ClosureApplied); err != nil {
		t.Fatalf("planned to applied: %v", err)
	}
	if err := ValidateClosureTransition(ClosureApplied, ClosureReopened); err != nil {
		t.Fatalf("applied to reopened: %v", err)
	}
	invalid := [][2]ClosureStatus{
		{ClosurePlanned, ClosureReopened},
		{ClosureApplied, ClosurePlanned},
		{ClosureReopened, ClosureApplied},
		{ClosureApplied, ClosureApplied},
	}
	for _, transition := range invalid {
		if err := ValidateClosureTransition(transition[0], transition[1]); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("transition %s -> %s error = %v", transition[0], transition[1], err)
		}
	}
}
