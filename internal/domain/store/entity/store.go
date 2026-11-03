package entity

import "time"

type Store struct {
	ID               int        `json:"id"`
	Name             string     `json:"name"`
	BCCLicenseNumber string     `json:"bcc_license_number"`
	LicenseIssuedAt  *time.Time `json:"license_issued_at"`
	LicenseExpiresAt *time.Time `json:"license_expires_at"`
	Status           string     `json:"status"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	DeletedAt        *time.Time `json:"deleted_at,omitempty"`
}
