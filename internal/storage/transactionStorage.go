package storage

import (
	"database/sql"
	customerentity "fx-app-api/internal/domain/customer/entity"
	"fx-app-api/internal/domain/transaction/entity"
	"time"
)

type TransactionStorage interface {
	Init() error
	// BeginTx ouvre une transaction SQL pour regrouper la création et l'analyse AML.
	BeginTx() (*sql.Tx, error)
	CreateTransaction(tx *entity.Transaction) error
	// CreateTransactionWithTx insère la transaction dans la transaction SQL fournie : si elle est
	// annulée (rollback), la transaction de change disparaît avec elle.
	CreateTransactionWithTx(dbTx *sql.Tx, tx *entity.Transaction) error
	// CreateTransactionWithNewCustomer enregistre la transaction pour ce client. Si un client actif
	// existe déjà avec ce téléphone, il est réutilisé sans être modifié (customer est alors remplacé
	// par le client existant). Elle retourne la transaction SQL non commitée.
	CreateTransactionWithNewCustomer(tx *entity.Transaction, customer *customerentity.Customer) (*sql.Tx, error)
	CreateTransactionForExistingCustomerPhone(tx *entity.Transaction, phone string) (*sql.Tx, *customerentity.Customer, error)
	UpdateTransaction(tx *entity.Transaction) error
	UpdateTransactionWithTx(tx *sql.Tx, txToUpdate *entity.Transaction) error
	GetTransaction(id int) (*entity.Transaction, error)
	ListTransactions(filter TransactionFilter) ([]*entity.Transaction, error)
	// ListTransactionsWithTx lit dans la transaction SQL fournie : elle voit donc les écritures
	// non encore commitées de cette transaction (la transaction en cours d'analyse, par exemple).
	ListTransactionsWithTx(dbTx *sql.Tx, filter TransactionFilter) ([]*entity.Transaction, error)
	// SearchTransactions renvoie une page de transactions avec le client associé, et le nombre total.
	SearchTransactions(filter TransactionFilter) ([]*entity.TransactionListItem, int, error)
}

type TransactionFilter struct {
	StoreID    int
	CustomerID int
	TraderID   int
	Since      time.Time

	// Critères utilisés par SearchTransactions uniquement.
	Query     string // recherche sur le nom ou le téléphone du client, ou sur l'identifiant exact
	Status    string
	Type      string
	RiskLevel string
	Limit     int
	Offset    int
}

type ScopedTransactionStorage interface {
	GetTransactionInStore(id, storeID int) (*entity.Transaction, error)
}
