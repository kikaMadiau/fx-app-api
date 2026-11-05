package entity

import (
	"fmt"
	"strings"
	"time"
)

type Transaction struct {
	ID              int        `json:"id"`
	StoreID         int        `json:"store_id,omitempty"`
	Amount          float64    `json:"amount"`
	Currency        string     `json:"currency"`
	TargetCurrency  string     `json:"target_currency,omitempty"`
	Type            string     `json:"type"` // BUY ou SELL
	Rate            float64    `json:"rate"`
	ConvertedAmount float64    `json:"converted_amount,omitempty"`
	RiskLevel       string     `json:"risk_level,omitempty"` // LOW, MEDIUM, HIGH, CRITICAL
	RiskScore       float64    `json:"risk_score"`
	Status          string     `json:"status"` // PENDING, COMPLETED ou CANCELLED
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"` // Utilise un pointeur pour les timestamps nullables
	TraderID        int        `json:"trader_id"`
	CustomerID      int        `json:"customer_id"`
}

// TransactionListItem est la vue « liste » d'une transaction : ses données de base
// enrichies du client concerné. Les champs de Transaction sont aplatis dans le JSON.
type TransactionListItem struct {
	Transaction
	CustomerName     string `json:"customer_name"`
	CustomerPhone    string `json:"customer_phone,omitempty"`
	CustomerIDType   string `json:"customer_id_type"`
	CustomerIDNumber string `json:"customer_id_number"`
}

// Type de transaction de change du point de vue de l'agence.
const (
	TypeBuy  = "BUY"  // l'agence achète la devise au client (« Achat »)
	TypeSell = "SELL" // l'agence vend la devise au client (« Vente »)
)

// Statuts d'une transaction.
const (
	StatusPending   = "PENDING"
	StatusCompleted = "COMPLETED"
	StatusCancelled = "CANCELLED"
)

// ValidTypes et ValidStatuses listent les valeurs acceptées.
var (
	ValidTypes    = []string{TypeBuy, TypeSell}
	ValidStatuses = []string{StatusPending, StatusCompleted, StatusCancelled}
)

// NormalizeType met le type en majuscules et vérifie qu'il appartient à l'enum.
// Les anciens libellés français « Achat » et « Vente » sont acceptés. Le type est obligatoire.
func NormalizeType(value string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	switch normalized {
	case "ACHAT":
		normalized = TypeBuy
	case "VENTE":
		normalized = TypeSell
	}

	if normalized == "" {
		return "", fmt.Errorf("type is required (%s)", strings.Join(ValidTypes, ", "))
	}
	for _, valid := range ValidTypes {
		if valid == normalized {
			return normalized, nil
		}
	}

	return "", fmt.Errorf("type must be one of: %s", strings.Join(ValidTypes, ", "))
}

// NormalizeStatus met le statut en majuscules et vérifie qu'il appartient à l'enum.
// Un statut vide vaut COMPLETED : une opération de change au guichet est réalisée sur le champ.
func NormalizeStatus(value string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	if normalized == "" {
		return StatusCompleted, nil
	}

	for _, valid := range ValidStatuses {
		if valid == normalized {
			return normalized, nil
		}
	}

	return "", fmt.Errorf("status must be one of: %s", strings.Join(ValidStatuses, ", "))
}

// NormalizeCurrency met le code devise en majuscules et vérifie qu'il s'agit de 3 lettres (ISO 4217).
func NormalizeCurrency(field, value string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	if normalized == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	if len(normalized) != 3 {
		return "", fmt.Errorf("%s must be a 3-letter currency code", field)
	}
	for _, r := range normalized {
		if r < 'A' || r > 'Z' {
			return "", fmt.Errorf("%s must be a 3-letter currency code", field)
		}
	}

	return normalized, nil
}
