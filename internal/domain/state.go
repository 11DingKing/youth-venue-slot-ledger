package domain

import "fmt"

var bookingTransitions = map[BookingStatus]map[BookingStatus]struct{}{
	BookingHeld: {
		BookingConfirmed: {},
		BookingCancelled: {},
		BookingNoShow:    {},
	},
	BookingConfirmed: {
		BookingCancelled: {},
		BookingCheckedIn: {},
		BookingNoShow:    {},
	},
	BookingWaitlisted: {
		BookingHeld:      {},
		BookingCancelled: {},
	},
}

func CanTransitionBooking(from, to BookingStatus) bool {
	next, ok := bookingTransitions[from]
	if !ok {
		return false
	}
	_, ok = next[to]
	return ok
}

func ValidateBookingTransition(from, to BookingStatus) error {
	if !CanTransitionBooking(from, to) {
		return fmt.Errorf("%w: booking %s to %s", ErrInvalidState, from, to)
	}
	return nil
}

type ClosureStatus string

const (
	ClosurePlanned  ClosureStatus = "planned"
	ClosureApplied  ClosureStatus = "applied"
	ClosureReopened ClosureStatus = "reopened"
)

func ValidateClosureTransition(from, to ClosureStatus) error {
	valid := (from == ClosurePlanned && to == ClosureApplied) ||
		(from == ClosureApplied && to == ClosureReopened)
	if !valid {
		return fmt.Errorf("%w: closure %s to %s", ErrInvalidState, from, to)
	}
	return nil
}

type JobStatus string

const (
	JobPending   JobStatus = "pending"
	JobRunning   JobStatus = "running"
	JobRetry     JobStatus = "retry"
	JobSucceeded JobStatus = "succeeded"
	JobDead      JobStatus = "dead"
)

type WorkerJob struct {
	ID          int64     `json:"id"`
	Kind        string    `json:"kind"`
	Payload     string    `json:"payload"`
	Status      JobStatus `json:"status"`
	Attempts    int       `json:"attempts"`
	MaxAttempts int       `json:"max_attempts"`
	AvailableAt string    `json:"available_at"`
	LeaseUntil  *string   `json:"lease_until,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
}
