package entity

import "time"

type RiskFlag struct {
	ID            int        `json:"id"`
	Flag          string     `json:"flag"`
	Reason        string     `json:"reason"`
	Score         float64    `json:"score"`
	Level         string     `json:"level"`
	TransactionId int        `json:"transaction_id"`
	TraderId      int        `json:"trader_id"`
	CustomerId    int        `json:"customer_id"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	DeletedAt     *time.Time `json:"deleted_at"` // Utilise un pointeur pour les timestamps nullables
}
