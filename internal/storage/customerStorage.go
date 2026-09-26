package storage

import (
	"database/sql"
	"fx-app-api/internal/domain/customer/entity"
)

type CustomerStorage interface {
	Init() error
	CreateCustomer(customer *entity.Customer) error
	UpdateCustomer(customer *entity.Customer) error
	UpdateCustomerWithTx(tx *sql.Tx, customer *entity.Customer) error
	GetCustomer(id int) (*entity.Customer, error)
	GetCustomerByPhone(phone string) (*entity.Customer, error)
}
