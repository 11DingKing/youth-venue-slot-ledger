package domain

import (
	"errors"
	"testing"
	"time"
)

func TestAgeOnAroundAnniversary(t *testing.T) {
	t.Parallel()
	birth := time.Date(2012, time.July, 15, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		at   time.Time
		want int
	}{
		{name: "day before", at: time.Date(2026, time.July, 14, 23, 59, 0, 0, time.UTC), want: 13},
		{name: "anniversary", at: time.Date(2026, time.July, 15, 0, 0, 0, 0, time.UTC), want: 14},
		{name: "after", at: time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC), want: 14},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := AgeOn(birth, test.at); got != test.want {
				t.Fatalf("AgeOn() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestValidateEligibility(t *testing.T) {
	t.Parallel()
	birth := time.Date(2014, time.June, 10, 0, 0, 0, 0, time.UTC)
	student := User{ID: 1, Role: RoleStudent, BirthDate: &birth, AbilityLevel: 3, Active: true}
	slot := Slot{
		VenueID: 1, Sport: "swimming", StartsAt: time.Date(2026, time.August, 1, 10, 0, 0, 0, time.UTC),
		EndsAt: time.Date(2026, time.August, 1, 11, 0, 0, 0, time.UTC), Capacity: 10,
		MinimumAge: 10, MaximumAge: 14, MinimumAbility: 2, MaximumAbility: 5, Status: SlotOpen,
	}
	if err := ValidateEligibility(student, slot); err != nil {
		t.Fatalf("eligible student rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*User, *Slot)
	}{
		{name: "inactive", mutate: func(user *User, _ *Slot) { user.Active = false }},
		{name: "not student", mutate: func(user *User, _ *Slot) { user.Role = RoleGuardian }},
		{name: "missing birth date", mutate: func(user *User, _ *Slot) { user.BirthDate = nil }},
		{name: "too young", mutate: func(_ *User, slot *Slot) { slot.MinimumAge = 13 }},
		{name: "too old", mutate: func(_ *User, slot *Slot) { slot.MaximumAge = 10 }},
		{name: "ability too low", mutate: func(user *User, _ *Slot) { user.AbilityLevel = 1 }},
		{name: "ability too high", mutate: func(user *User, _ *Slot) { user.AbilityLevel = 8 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidateUser := student
			candidateSlot := slot
			test.mutate(&candidateUser, &candidateSlot)
			if err := ValidateEligibility(candidateUser, candidateSlot); !errors.Is(err, ErrEligibility) {
				t.Fatalf("error = %v, want ErrEligibility", err)
			}
		})
	}
}

func TestValidateSlot(t *testing.T) {
	t.Parallel()
	base := Slot{
		VenueID: 1, Sport: "basketball", StartsAt: time.Now().UTC().Add(time.Hour),
		EndsAt: time.Now().UTC().Add(2 * time.Hour), Capacity: 8, Reserved: 2,
		MinimumAge: 8, MaximumAge: 15, MinimumAbility: 0, MaximumAbility: 10, Status: SlotOpen,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid slot rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Slot)
	}{
		{name: "no venue", mutate: func(slot *Slot) { slot.VenueID = 0 }},
		{name: "no sport", mutate: func(slot *Slot) { slot.Sport = "" }},
		{name: "reversed time", mutate: func(slot *Slot) { slot.EndsAt = slot.StartsAt }},
		{name: "zero capacity", mutate: func(slot *Slot) { slot.Capacity = 0 }},
		{name: "negative reserved", mutate: func(slot *Slot) { slot.Reserved = -1 }},
		{name: "oversubscribed", mutate: func(slot *Slot) { slot.Reserved = slot.Capacity + 1 }},
		{name: "negative age", mutate: func(slot *Slot) { slot.MinimumAge = -1 }},
		{name: "age range", mutate: func(slot *Slot) { slot.MaximumAge = slot.MinimumAge - 1 }},
		{name: "negative ability", mutate: func(slot *Slot) { slot.MinimumAbility = -1 }},
		{name: "ability range", mutate: func(slot *Slot) { slot.MaximumAbility = slot.MinimumAbility - 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := base
			test.mutate(&candidate)
			if err := candidate.Validate(); !errors.Is(err, ErrValidation) {
				t.Fatalf("error = %v, want ErrValidation", err)
			}
		})
	}
}

func TestValidateCheckInWindow(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, time.August, 20, 9, 0, 0, 0, time.UTC)
	slot := Slot{StartsAt: start, EndsAt: start.Add(time.Hour)}
	for _, at := range []time.Time{start.Add(-30 * time.Minute), start, start.Add(time.Hour)} {
		if err := ValidateCheckInWindow(slot, at); err != nil {
			t.Fatalf("time %s should be valid: %v", at, err)
		}
	}
	for _, at := range []time.Time{start.Add(-31 * time.Minute), start.Add(time.Hour + time.Nanosecond)} {
		if err := ValidateCheckInWindow(slot, at); !errors.Is(err, ErrValidation) {
			t.Fatalf("time %s error = %v, want ErrValidation", at, err)
		}
	}
}
