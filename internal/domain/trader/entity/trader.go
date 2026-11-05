package entity

import "time"

type Store struct {
	ID               int        `json:"id"`
	Name             string     `json:"name"`
	BCCLicenseNumber string     `json:"bcc_license_number"`
	Status           string     `json:"status"`
	LicenseExpiresAt *time.Time `json:"license_expires_at"`
}

type Trader struct {
	Id           int        `json:"id"`
	Name         string     `json:"name"`
	FirstName    string     `json:"first_name"`
	LastName     string     `json:"last_name"`
	Email        string     `json:"email"`
	Phone        string     `json:"phone"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	DeletedAt    *time.Time `json:"deleted_at"` // Utilise un pointeur pour les timestamps nullables
	Roles        string     `json:"roles"`
	PasswordHash string     `json:"password_hash"` // Renommé de Password pour refléter le hachage
	//Store     Store  `json:"store"`
	StoreId int `json:"store_id"`
	//Location Locatin `json:"location"`
	IsActive           bool   `json:"is_active"`
	IsPlatformAdmin    bool   `json:"is_platform_admin"`
	MustChangePassword bool   `json:"must_change_password,omitempty"`
	Store              *Store `json:"store,omitempty"`
}
