package entity

import "time"

// RiskFlagStatusHistory représente l'historique des transitions de statut d'un risk_flag.
type RiskFlagStatusHistory struct {
	ID          int       `json:"id"`
	RiskFlagID  int       `json:"risk_flag_id"`
	FromStatus  *string   `json:"from_status,omitempty"`
	ToStatus    string    `json:"to_status"`
	ChangedBy   int       `json:"changed_by"`
	Note        string    `json:"note"`
	CreatedAt   time.Time `json:"created_at"`
}
