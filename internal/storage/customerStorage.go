package storage

import (
	"database/sql"
	"fx-app-api/internal/domain/customer/entity"
)

// CustomerFilter décrit les critères de recherche et de pagination de la liste des clients.
type CustomerFilter struct {
	StoreID   int
	Query     string // recherche partielle sur le nom, le téléphone et le numéro de pièce
	KYCStatus string // statut KYC exact (UNVERIFIED, VERIFIED, ...)
	RiskLevel string // niveau de risque exact (LOW, MEDIUM, HIGH, CRITICAL)
	Limit     int
	Offset    int
}

type ScopedCustomerStorage interface {
	GetCustomerInStore(id, storeID int) (*entity.Customer, error)
	GetCustomerWithTxInStore(tx *sql.Tx, id, storeID int) (*entity.Customer, error)
	GetCustomerByPhoneInStore(phone string, storeID int) (*entity.Customer, error)
	FindOrCreateCustomerWithTx(tx *sql.Tx, customer *entity.Customer) (bool, error)
}

type CustomerStorage interface {
	Init() error
	// BeginTx ouvre une transaction SQL pour regrouper plusieurs écritures.
	BeginTx() (*sql.Tx, error)
	CreateCustomer(customer *entity.Customer) error
	CreateCustomerWithTx(tx *sql.Tx, customer *entity.Customer) error
	UpdateCustomer(customer *entity.Customer) error
	UpdateCustomerWithTx(tx *sql.Tx, customer *entity.Customer) error
	GetCustomer(id int) (*entity.Customer, error)
	// GetCustomerWithTx lit dans la transaction SQL fournie (nil : connexion normale) et voit donc
	// un client créé plus tôt dans cette même transaction, pas encore commité.
	GetCustomerWithTx(tx *sql.Tx, id int) (*entity.Customer, error)
	GetCustomerByPhone(phone string) (*entity.Customer, error)
	// ListCustomers renvoie une page de clients actifs et le nombre total de résultats.
	ListCustomers(filter CustomerFilter) ([]*entity.CustomerSummary, int, error)
}
