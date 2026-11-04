package storage

import (
	"database/sql"
	"fx-app-api/internal/domain/riskflag/entity"
	"time"
)

type RiskFlagStorage interface {
	Init() error
	CreateRiskFlag(flag *entity.RiskFlag) error
	CreateRiskFlagWithTx(tx *sql.Tx, flag *entity.RiskFlag) error
	GetRiskFlag(id int) (*entity.RiskFlag, error)
	ListRiskFlags(filter RiskFlagFilter) ([]*entity.RiskFlag, error)
	ListRiskFlagsWithDetails(filter RiskFlagFilter) ([]*RiskFlagWithDetails, int, error)
	DeleteRiskFlag(id int) error
	UpdateRiskFlagStatus(tx *sql.Tx, flagID int, newStatus string, changedBy int, note string, assignedTo *int) (*entity.RiskFlag, error)
	ListRiskFlagStatusHistory(flagID int) ([]*entity.RiskFlagStatusHistory, error)
}

type RiskFlagFilter struct {
	StoreID       int
	TransactionID int
	TraderID      int
	CustomerID    int
	Status        string
	Level         string
	Query         string
	AssignedTo    int
	Limit         int
	Offset        int
}

type ScopedRiskFlagStorage interface {
	GetRiskFlagInStore(id, storeID int) (*entity.RiskFlag, error)
	DeleteRiskFlagInStore(id, storeID int) error
	DeleteRiskFlagWithTxInStore(tx *sql.Tx, id, storeID int) error
	UpdateRiskFlagStatusInStore(tx *sql.Tx, flagID, storeID int, newStatus string, changedBy int, note string, assignedTo *int) (*entity.RiskFlag, error)
	ListRiskFlagStatusHistoryInStore(flagID, storeID int) ([]*entity.RiskFlagStatusHistory, error)
}

type RiskFlagWithDetails struct {
	ID                   int                  `json:"id"`
	Flag                 string               `json:"flag"`
	Reason               string               `json:"reason"`
	Score                float64              `json:"score"`
	Level                string               `json:"level"`
	TransactionId        *int                 `json:"transaction_id"`
	TraderId             int                  `json:"trader_id"`
	CustomerId           int                  `json:"customer_id"`
	Status               string               `json:"status"`
	AssignedTo           *int                 `json:"assigned_to,omitempty"`
	ResolutionNote       string               `json:"resolution_note,omitempty"`
	ResolvedAt           *time.Time           `json:"resolved_at,omitempty"`
	ResolvedBy           *int                 `json:"resolved_by,omitempty"`
	CreatedAt            time.Time            `json:"created_at"`
	UpdatedAt            time.Time            `json:"updated_at"`
	DeletedAt            *time.Time           `json:"deleted_at"`
	CustomerName         string               `json:"customer_name"`
	CustomerPhone        string               `json:"customer_phone,omitempty"`
	IDType               string               `json:"id_type,omitempty"`
	IDNumber             string               `json:"id_number,omitempty"`
	TransactionType      string               `json:"transaction_type,omitempty"`
	TransactionAmount    float64              `json:"transaction_amount,omitempty"`
	TransactionRate      float64              `json:"transaction_rate,omitempty"`
	TransactionCreatedAt time.Time            `json:"transaction_created_at,omitempty"`
	Customer             *RiskFlagCustomer    `json:"customer"`
	Transaction          *RiskFlagTransaction `json:"transaction"`
}

type RiskFlagCustomer struct {
	ID        int    `json:"id"`
	FullName  string `json:"full_name"`
	Phone     string `json:"phone"`
	KYCStatus string `json:"kyc_status"`
}
type RiskFlagTransaction struct {
	ID              int       `json:"id"`
	Type            string    `json:"type"`
	Currency        string    `json:"currency"`
	Amount          float64   `json:"amount"`
	Rate            float64   `json:"rate"`
	ConvertedAmount float64   `json:"converted_amount"`
	CreatedAt       time.Time `json:"created_at"`
}
