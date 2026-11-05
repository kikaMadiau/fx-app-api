package storage

import (
	"errors"

	"github.com/lib/pq"
)

var (
	ErrCustomerPhoneExists     = errors.New("customer with this phone already exists")
	ErrCustomerIDNumberExists  = errors.New("customer with this id_number already exists")
	ErrCustomerNotFound        = errors.New("customer with this phone was not found")
	ErrCustomerIDNotFound      = errors.New("customer not found")
	ErrRiskFlagNotFound        = errors.New("risk flag not found")
	ErrInvalidStatusTransition = errors.New("invalid status transition")
	ErrResolutionNoteRequired  = errors.New("resolution_note is required for this status")
	ErrAssignedToRequired      = errors.New("assigned_to is required for IN_REVIEW status")
	ErrTransactionNotFound     = errors.New("transaction not found")
	ErrTraderNotFound          = errors.New("trader not found")
	ErrLastActiveAdmin         = errors.New("cannot remove the last active administrator")
	ErrIdentityConflict        = errors.New("identity conflicts with an existing person")
)

// PostgresErrorCode returns the SQLSTATE code when err wraps a PostgreSQL error.
func PostgresErrorCode(err error) string {
	pgErr, ok := PostgresError(err)
	if !ok {
		return ""
	}
	return string(pgErr.Code)
}

// PostgresError returns the PostgreSQL error wrapped by err, if any.
func PostgresError(err error) (*pq.Error, bool) {
	var pgErr *pq.Error
	if errors.As(err, &pgErr) {
		return pgErr, true
	}
	return nil, false
}
