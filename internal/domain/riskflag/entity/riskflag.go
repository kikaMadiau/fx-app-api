package entity

import "time"

// RiskFlagStatus est le type pour les statuts d'investigation d'un risk_flag.
type RiskFlagStatus string

// Constantes de statut pour risk_flag.
const (
	RiskFlagStatusOpen        RiskFlagStatus = "OPEN"
	RiskFlagStatusInReview    RiskFlagStatus = "IN_REVIEW"
	RiskFlagStatusResolved    RiskFlagStatus = "RESOLVED"
	RiskFlagStatusFalsePositive RiskFlagStatus = "FALSE_POSITIVE"
)

// IsValidRiskFlagStatus valide si une chaîne est un statut valide.
func IsValidRiskFlagStatus(s string) bool {
	switch RiskFlagStatus(s) {
	case RiskFlagStatusOpen, RiskFlagStatusInReview, RiskFlagStatusResolved, RiskFlagStatusFalsePositive:
		return true
	default:
		return false
	}
}

type RiskFlag struct {
	ID              int        `json:"id"`
	Flag            string     `json:"flag"`
	Reason          string     `json:"reason"`
	Score           float64    `json:"score"`
	Level           string     `json:"level"`
	TransactionId   int        `json:"transaction_id"`
	TraderId        int        `json:"trader_id"`
	CustomerId      int        `json:"customer_id"`
	Status          string     `json:"status"`
	AssignedTo      *int       `json:"assigned_to,omitempty"`
	ResolutionNote  string     `json:"resolution_note,omitempty"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy      *int       `json:"resolved_by,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	DeletedAt       *time.Time `json:"deleted_at"` // Utilise un pointeur pour les timestamps nullables
}
