package entity

import "time"

type Transaction struct {
	ID              int        `json:"id"`
	Amount          float64    `json:"amount"`
	Currency        string     `json:"currency"`
	TargetCurrency  string     `json:"target_currency,omitempty"`
	Type            string     `json:"type"` // e.g., "deposit", "withdrawal"
	Rate            float64    `json:"rate"`
	ConvertedAmount float64    `json:"converted_amount,omitempty"`
	RiskLevel       string     `json:"risk_level,omitempty"` // LOW, MEDIUM, HIGH, CRITICAL
	RiskScore       float64    `json:"risk_score,omitempty"`
	Status          string     `json:"status"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	DeletedAt       *time.Time `json:"deleted_at"` // Utilise un pointeur pour les timestamps nullables
	TraderID        int        `json:"trader_id"`
	CustomerID      int        `json:"customer_id"`
}
