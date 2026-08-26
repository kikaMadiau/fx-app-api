package repository

import (
	"database/sql"
	"errors"
	"fmt"
	customerentity "fx-app-api/internal/domain/customer/entity"
	"fx-app-api/internal/domain/transaction/entity"
	"fx-app-api/internal/storage"
	"strings"
	"time"

	"github.com/lib/pq"
)

// transactionRepository est l'implémentation concrète de storage.TransactionStorage.
type transactionRepository struct {
	store *storage.PostgresStore
}

// NewTransactionRepository crée une nouvelle instance qui implémente storage.TransactionStorage.
func NewTransactionRepository(store *storage.PostgresStore) storage.TransactionStorage {
	return &transactionRepository{store: store}
}

// Init crée la table 'transactions' si elle n'existe pas.
func (r *transactionRepository) Init() error {
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS transactions (
			id SERIAL PRIMARY KEY,
			amount NUMERIC(19, 4) NOT NULL,
			currency VARCHAR(10) NOT NULL,
			target_currency VARCHAR(10),
			type VARCHAR(50),
			rate NUMERIC(19, 6),
			converted_amount NUMERIC(19, 4),
			risk_level VARCHAR(50),
			risk_score NUMERIC(5, 2),
			status VARCHAR(50),
			trader_id INT NOT NULL,
			customer_id INT NOT NULL,
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMPTZ,
			CONSTRAINT fk_trader FOREIGN KEY(trader_id) REFERENCES traders(id),
			CONSTRAINT fk_customer FOREIGN KEY(customer_id) REFERENCES customers(id)
		);`

	if _, err := r.store.DB().Exec(createTableSQL); err != nil {
		return err
	}

	alterTableSQL := `
		ALTER TABLE transactions
			ADD COLUMN IF NOT EXISTS target_currency VARCHAR(10),
			ADD COLUMN IF NOT EXISTS converted_amount NUMERIC(19, 4);`

	_, err := r.store.DB().Exec(alterTableSQL)
	return err
}

// CreateTransaction insère une nouvelle transaction dans la base de données.
func (r *transactionRepository) CreateTransaction(tx *entity.Transaction) error {
	if err := insertTransaction(r.store.DB(), tx); err != nil {
		return fmt.Errorf("failed to create transaction: %w", err)
	}

	return nil
}

// CreateTransactionWithNewCustomer crée un client puis sa transaction dans une seule transaction SQL.
func (r *transactionRepository) CreateTransactionWithNewCustomer(tx *entity.Transaction, customer *customerentity.Customer) error {
	normalizedPhone, err := storage.NormalizePhone(customer.Phone)
	if err != nil {
		return err
	}
	customer.Phone = normalizedPhone
	if strings.TrimSpace(customer.IDNumber) == "" {
		customer.IDNumber = normalizedPhone
	}

	dbTx, err := r.store.DB().Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer rollbackUnlessCommitted(dbTx)

	if _, err := selectCustomerByPhone(dbTx, normalizedPhone, true); err == nil {
		return storage.ErrCustomerPhoneExists
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("failed to check existing customer: %w", err)
	}

	if err := insertCustomer(dbTx, customer); err != nil {
		if isUniqueViolation(err) {
			return storage.ErrCustomerPhoneExists
		}
		return fmt.Errorf("failed to create customer: %w", err)
	}

	tx.CustomerID = customer.ID
	if err := insertTransaction(dbTx, tx); err != nil {
		return fmt.Errorf("failed to create transaction: %w", err)
	}

	if err := dbTx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// CreateTransactionForExistingCustomerPhone associe une transaction à un client existant sans le modifier.
func (r *transactionRepository) CreateTransactionForExistingCustomerPhone(tx *entity.Transaction, phone string) (*customerentity.Customer, error) {
	normalizedPhone, err := storage.NormalizePhone(phone)
	if err != nil {
		return nil, err
	}

	dbTx, err := r.store.DB().Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer rollbackUnlessCommitted(dbTx)

	customer, err := selectCustomerByPhone(dbTx, normalizedPhone, true)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrCustomerNotFound
		}
		return nil, fmt.Errorf("failed to get customer by phone: %w", err)
	}

	tx.CustomerID = customer.ID
	if err := insertTransaction(dbTx, tx); err != nil {
		return nil, fmt.Errorf("failed to create transaction: %w", err)
	}

	if err := dbTx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return customer, nil
}

// UpdateTransaction met à jour une transaction existante dans la base de données.
func (r *transactionRepository) UpdateTransaction(tx *entity.Transaction) error {
	query := `
		UPDATE transactions SET
			amount = $1, currency = $2, target_currency = $3, type = $4,
			rate = $5, converted_amount = $6, risk_level = $7, risk_score = $8,
			status = $9, trader_id = $10, customer_id = $11, updated_at = $12
		WHERE id = $13`

	tx.UpdatedAt = time.Now()

	_, err := r.store.DB().Exec(query,
		tx.Amount,
		tx.Currency,
		tx.TargetCurrency,
		tx.Type,
		tx.Rate,
		tx.ConvertedAmount,
		tx.RiskLevel,
		tx.RiskScore,
		tx.Status,
		tx.TraderID,
		tx.CustomerID,
		tx.UpdatedAt,
		tx.ID,
	)

	if err != nil {
		return fmt.Errorf("failed to update transaction: %w", err)
	}
	return nil
}

// GetTransaction récupère une transaction par son ID.
func (r *transactionRepository) GetTransaction(id int) (*entity.Transaction, error) {
	query := `
		SELECT id, amount, currency, COALESCE(target_currency, ''),
		       COALESCE(type, ''), COALESCE(rate, 0),
		       COALESCE(converted_amount, 0), COALESCE(risk_level, ''), COALESCE(risk_score, 0),
		       COALESCE(status, ''), created_at, updated_at, deleted_at, trader_id, customer_id
		FROM transactions
		WHERE id = $1 AND deleted_at IS NULL`

	tx := &entity.Transaction{}
	err := r.store.DB().QueryRow(query, id).Scan(
		&tx.ID, &tx.Amount, &tx.Currency, &tx.TargetCurrency, &tx.Type, &tx.Rate,
		&tx.ConvertedAmount, &tx.RiskLevel, &tx.RiskScore, &tx.Status, &tx.CreatedAt, &tx.UpdatedAt, &tx.DeletedAt,
		&tx.TraderID, &tx.CustomerID,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("transaction with ID %d not found", id)
		}
		return nil, fmt.Errorf("failed to get transaction: %w", err)
	}

	return tx, nil
}

// ListTransactions liste les transactions, avec filtres optionnels.
func (r *transactionRepository) ListTransactions(filter storage.TransactionFilter) ([]*entity.Transaction, error) {
	query := `
		SELECT id, amount, currency, COALESCE(target_currency, ''),
		       COALESCE(type, ''), COALESCE(rate, 0),
		       COALESCE(converted_amount, 0), COALESCE(risk_level, ''), COALESCE(risk_score, 0),
		       COALESCE(status, ''), created_at, updated_at, deleted_at, trader_id, customer_id
		FROM transactions
		WHERE deleted_at IS NULL`

	var args []any
	var conditions []string
	if filter.CustomerID > 0 {
		args = append(args, filter.CustomerID)
		conditions = append(conditions, fmt.Sprintf("customer_id = $%d", len(args)))
	}
	if filter.TraderID > 0 {
		args = append(args, filter.TraderID)
		conditions = append(conditions, fmt.Sprintf("trader_id = $%d", len(args)))
	}
	if !filter.Since.IsZero() {
		args = append(args, filter.Since)
		conditions = append(conditions, fmt.Sprintf("created_at >= $%d", len(args)))
	}
	if len(conditions) > 0 {
		query += " AND " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at DESC, id DESC"

	rows, err := r.store.DB().Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list transactions: %w", err)
	}
	defer rows.Close()

	var transactions []*entity.Transaction
	for rows.Next() {
		tx := &entity.Transaction{}
		if err := rows.Scan(
			&tx.ID, &tx.Amount, &tx.Currency, &tx.TargetCurrency, &tx.Type, &tx.Rate,
			&tx.ConvertedAmount, &tx.RiskLevel, &tx.RiskScore, &tx.Status, &tx.CreatedAt, &tx.UpdatedAt, &tx.DeletedAt,
			&tx.TraderID, &tx.CustomerID,
		); err != nil {
			return nil, fmt.Errorf("failed to scan transaction: %w", err)
		}
		transactions = append(transactions, tx)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate transactions: %w", err)
	}

	return transactions, nil
}

type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

func insertCustomer(q rowQuerier, customer *customerentity.Customer) error {
	query := `
		INSERT INTO customers (
			full_name, id_number, id_type, phone, address, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7
		) RETURNING id, created_at, updated_at`

	customer.CreatedAt = time.Now()
	customer.UpdatedAt = time.Now()

	return q.QueryRow(query,
		customer.FullName,
		customer.IDNumber,
		customer.IDType,
		customer.Phone,
		customer.Address,
		customer.CreatedAt,
		customer.UpdatedAt,
	).Scan(&customer.ID, &customer.CreatedAt, &customer.UpdatedAt)
}

func insertTransaction(q rowQuerier, tx *entity.Transaction) error {
	query := `
		INSERT INTO transactions (
			amount, currency, target_currency, type, rate, converted_amount,
			risk_level, risk_score, status, trader_id, customer_id, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		) RETURNING id, created_at, updated_at`

	tx.CreatedAt = time.Now()
	tx.UpdatedAt = time.Now()

	return q.QueryRow(query,
		tx.Amount,
		tx.Currency,
		tx.TargetCurrency,
		tx.Type,
		tx.Rate,
		tx.ConvertedAmount,
		tx.RiskLevel,
		tx.RiskScore,
		tx.Status,
		tx.TraderID,
		tx.CustomerID,
		tx.CreatedAt,
		tx.UpdatedAt,
	).Scan(&tx.ID, &tx.CreatedAt, &tx.UpdatedAt)
}

func selectCustomerByPhone(q rowQuerier, normalizedPhone string, lock bool) (*customerentity.Customer, error) {
	query := `
		SELECT id, full_name, id_number, COALESCE(id_type, ''), COALESCE(phone, ''),
		       COALESCE(address, ''), created_at, updated_at, deleted_at,
		       COALESCE(risk_level, ''), COALESCE(risk_score, 0)
		FROM customers
		WHERE phone = $1 AND deleted_at IS NULL`
	if lock {
		query += " FOR UPDATE"
	}

	customer := &customerentity.Customer{}
	err := q.QueryRow(query, normalizedPhone).Scan(
		&customer.ID,
		&customer.FullName,
		&customer.IDNumber,
		&customer.IDType,
		&customer.Phone,
		&customer.Address,
		&customer.CreatedAt,
		&customer.UpdatedAt,
		&customer.DeletedAt,
		&customer.RiskLevel,
		&customer.RiskScore,
	)
	if err != nil {
		return nil, err
	}

	return customer, nil
}

func rollbackUnlessCommitted(tx *sql.Tx) {
	_ = tx.Rollback()
}

func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}
