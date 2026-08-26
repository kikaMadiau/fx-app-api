package storage

import "errors"

var (
	ErrCustomerPhoneExists = errors.New("customer with this phone already exists")
	ErrCustomerNotFound    = errors.New("customer with this phone was not found")
)
