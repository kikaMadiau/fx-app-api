package storage

import (
	"fx-app-api/internal/domain/transaction/entity"
	"time"
)

type TransactionStorage interface {
	Init() error
	CreateTransaction(tx *entity.Transaction) error
	UpdateTransaction(tx *entity.Transaction) error
	GetTransaction(id int) (*entity.Transaction, error)
	ListTransactions(filter TransactionFilter) ([]*entity.Transaction, error)
}

type TransactionFilter struct {
	CustomerID int
	TraderID   int
	Since      time.Time
}
