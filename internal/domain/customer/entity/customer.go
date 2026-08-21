package entity

import "time"

type Customer struct {
	ID        int        `json:"id"`
	FullName  string     `json:"full_name"`
	IDNumber  string     `json:"id_number"`
	IDType    string     `json:"id_type"`
	Phone     string     `json:"phone"`
	Address   string     `json:"address"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"` // Utilise un pointeur pour les timestamps nullables
	RiskLevel string     `json:"risk_level,omitempty"`
	RiskScore float64    `json:"risk_score,omitempty"`
	// Add other fields as necessary
}
