package storage

import "errors"

var (
	ErrCustomerPhoneExists      = errors.New("customer with this phone already exists")
	ErrCustomerNotFound         = errors.New("customer with this phone was not found")
	ErrRiskFlagNotFound         = errors.New("risk flag not found")
	ErrInvalidStatusTransition  = errors.New("invalid status transition")
	ErrResolutionNoteRequired   = errors.New("resolution_note is required for this status")
	ErrAssignedToRequired       = errors.New("assigned_to is required for IN_REVIEW status")
)
