package storage

import (
	customerentity "fx-app-api/internal/domain/customer/entity"
	"fx-app-api/internal/domain/transaction/entity"
	"time"
)

type TransactionStorage interface {
	Init() error
	CreateTransaction(tx *entity.Transaction) error
	CreateTransactionWithNewCustomer(tx *entity.Transaction, customer *customerentity.Customer) error
	CreateTransactionForExistingCustomerPhone(tx *entity.Transaction, phone string) (*customerentity.Customer, error)
	UpdateTransaction(tx *entity.Transaction) error
	GetTransaction(id int) (*entity.Transaction, error)
	ListTransactions(filter TransactionFilter) ([]*entity.Transaction, error)
}

type TransactionFilter struct {
	CustomerID int
	TraderID   int
	Since      time.Time
}
