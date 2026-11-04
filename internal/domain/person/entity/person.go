package entity

import "time"

// Person holds only the shared identity used to recognize the same individual across stores.
// Store-specific KYC, risk and activity remain attached to customer records.
type Person struct {
	ID          int        `json:"id"`
	FullName    string     `json:"full_name"`
	Phone       string     `json:"phone"`
	IDType      string     `json:"id_type"`
	IDNumber    string     `json:"id_number"`
	DateOfBirth *time.Time `json:"date_of_birth,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}
