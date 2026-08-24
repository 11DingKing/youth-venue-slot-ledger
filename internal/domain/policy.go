package domain

import (
	"fmt"
	"time"
)

func AgeOn(birthDate, at time.Time) int {
	years := at.Year() - birthDate.Year()
	anniversary := time.Date(at.Year(), birthDate.Month(), birthDate.Day(), 0, 0, 0, 0, at.Location())
	if at.Before(anniversary) {
		years--
	}
	return years
}

func ValidateEligibility(student User, slot Slot) error {
	if student.Role != RoleStudent || student.BirthDate == nil || !student.Active {
		return fmt.Errorf("%w: inactive or incomplete student profile", ErrEligibility)
	}
	age := AgeOn(student.BirthDate.In(slot.StartsAt.Location()), slot.StartsAt)
	if age < slot.MinimumAge || age > slot.MaximumAge {
		return fmt.Errorf("%w: age %d outside %d-%d", ErrEligibility, age, slot.MinimumAge, slot.MaximumAge)
	}
	if student.AbilityLevel < slot.MinimumAbility || student.AbilityLevel > slot.MaximumAbility {
		return fmt.Errorf("%w: ability %d outside %d-%d", ErrEligibility, student.AbilityLevel, slot.MinimumAbility, slot.MaximumAbility)
	}
	return nil
}

func ValidateCheckInWindow(slot Slot, now time.Time) error {
	windowStart := slot.StartsAt.Add(-30 * time.Minute)
	windowEnd := slot.EndsAt
	if now.Before(windowStart) || now.After(windowEnd) {
		return fmt.Errorf("%w: check-in allowed between %s and %s", ErrValidation, windowStart, windowEnd)
	}
	return nil
}
