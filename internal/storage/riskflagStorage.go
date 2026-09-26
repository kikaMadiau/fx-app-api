package storage

import (
	"database/sql"
	"fx-app-api/internal/domain/riskflag/entity"
)

type RiskFlagStorage interface {
	Init() error
	CreateRiskFlag(flag *entity.RiskFlag) error
	CreateRiskFlagWithTx(tx *sql.Tx, flag *entity.RiskFlag) error
	GetRiskFlag(id int) (*entity.RiskFlag, error)
	ListRiskFlags(filter RiskFlagFilter) ([]*entity.RiskFlag, error)
	DeleteRiskFlag(id int) error
	UpdateRiskFlagStatus(tx *sql.Tx, flagID int, newStatus string, changedBy int, note string, assignedTo *int) (*entity.RiskFlag, error)
	ListRiskFlagStatusHistory(flagID int) ([]*entity.RiskFlagStatusHistory, error)
}

type RiskFlagFilter struct {
	TransactionID int
	TraderID      int
	CustomerID    int
	Status        string
	AssignedTo    int
}
