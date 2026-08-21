package repository

import (
	"database/sql"
	"fmt"
	"fx-app-api/internal/domain/customer/entity"
	"fx-app-api/internal/storage"
	"time"
)

// customerRepository est l'implémentation concrète de storage.CustomerStorage.
type customerRepository struct {
	store *storage.PostgresStore
}

// NewCustomerRepository crée une nouvelle instance qui implémente storage.CustomerStorage.
func NewCustomerRepository(store *storage.PostgresStore) storage.CustomerStorage {
	return &customerRepository{store: store}
}

// Init crée la table 'customers' si elle n'existe pas.
func (r *customerRepository) Init() error {
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS customers (
			id SERIAL PRIMARY KEY,
			full_name VARCHAR(255) NOT NULL,
			id_number VARCHAR(100) UNIQUE NOT NULL,
			id_type VARCHAR(50),
			phone VARCHAR(50),
			address TEXT,
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMPTZ
		);`

	if _, err := r.store.DB().Exec(createTableSQL); err != nil {
		return err
	}

	alterTableSQL := `
		ALTER TABLE customers
			ADD COLUMN IF NOT EXISTS risk_level VARCHAR(50),
			ADD COLUMN IF NOT EXISTS risk_score NUMERIC(5, 2) DEFAULT 0;`

	_, err := r.store.DB().Exec(alterTableSQL)
	return err
}

// CreateCustomer insère un nouveau client dans la base de données.
func (r *customerRepository) CreateCustomer(customer *entity.Customer) error {
	query := `
		INSERT INTO customers (
			full_name, id_number, id_type, phone, address, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7
		) RETURNING id, created_at, updated_at`

	customer.CreatedAt = time.Now()
	customer.UpdatedAt = time.Now()

	err := r.store.DB().QueryRow(query,
		customer.FullName,
		customer.IDNumber,
		customer.IDType,
		customer.Phone,
		customer.Address,
		customer.CreatedAt,
		customer.UpdatedAt,
	).Scan(&customer.ID, &customer.CreatedAt, &customer.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create customer: %w", err)
	}

	return nil
}

// GetCustomer récupère un client par son ID.
func (r *customerRepository) GetCustomer(id int) (*entity.Customer, error) {
	query := `
		SELECT id, full_name, id_number, COALESCE(id_type, ''), COALESCE(phone, ''),
		       COALESCE(address, ''), created_at, updated_at, deleted_at,
		       COALESCE(risk_level, ''), COALESCE(risk_score, 0)
		FROM customers
		WHERE id = $1 AND deleted_at IS NULL`
	customer := &entity.Customer{}

	err := r.store.DB().QueryRow(query, id).Scan(
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
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("customer with ID %d not found", id)
		}
		return nil, fmt.Errorf("failed to get customer: %w", err)
	}

	return customer, nil
}

// UpdateCustomer met à jour les informations d'un client existant dans la base de données.
func (r *customerRepository) UpdateCustomer(customer *entity.Customer) error {
	query := `
		UPDATE customers SET
			full_name = $1, id_number = $2, id_type = $3, phone = $4, address = $5,
			risk_level = $6, risk_score = $7, updated_at = $8
		WHERE id = $9`

	customer.UpdatedAt = time.Now()

	_, err := r.store.DB().Exec(query,
		customer.FullName,
		customer.IDNumber,
		customer.IDType,
		customer.Phone,
		customer.Address,
		customer.RiskLevel,
		customer.RiskScore,
		customer.UpdatedAt,
		customer.ID,
	)

	return err
}
