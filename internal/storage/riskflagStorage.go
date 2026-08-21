package storage

import (
	"fx-app-api/internal/domain/riskflag/entity"
)

type RiskFlagStorage interface {
	Init() error
	CreateRiskFlag(flag *entity.RiskFlag) error
	GetRiskFlag(id int) (*entity.RiskFlag, error)
	ListRiskFlags(filter RiskFlagFilter) ([]*entity.RiskFlag, error)
	DeleteRiskFlag(id int) error
}

type RiskFlagFilter struct {
	TransactionID int
	TraderID      int
	CustomerID    int
}
