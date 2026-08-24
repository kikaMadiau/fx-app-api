package repository

import (
	"database/sql"
	"fmt"
	"fx-app-api/internal/domain/trader/entity"
	"fx-app-api/internal/storage"
	"time"
)

// traderRepository est l'implémentation concrète de storage.TraderStorage.
type TraderRepository struct {
	store *storage.PostgresStore
}

// NewTraderRepository crée une nouvelle instance qui implémente storage.TraderStorage.
func NewTraderRepository(store *storage.PostgresStore) storage.TraderStorage {
	return &TraderRepository{store: store}
}

// Init crée la table 'traders' si elle n'existe pas.
func (r *TraderRepository) Init() error {
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS traders (
			id SERIAL PRIMARY KEY,
			name VARCHAR(255),
			first_name VARCHAR(255),
			last_name VARCHAR(255),
			email VARCHAR(255) UNIQUE NOT NULL,
			phone VARCHAR(50),
			status VARCHAR(50),
			role VARCHAR(50),
			password_hash VARCHAR(255) NOT NULL,
			store_id INT,
			is_active BOOLEAN DEFAULT TRUE,
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMPTZ
		);`

	_, err := r.store.DB().Exec(createTableSQL)
	return err
}

// CreateTrader insère un nouveau cambiste dans la base de données.
func (r *TraderRepository) CreateTrader(trader *entity.Trader) error {
	query := `
		INSERT INTO traders (
			name, first_name, last_name, email, phone, status, role, password_hash, store_id, is_active, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
		) RETURNING id, created_at, updated_at`

	trader.CreatedAt = time.Now()
	trader.UpdatedAt = time.Now()

	err := r.store.DB().QueryRow(query,
		trader.Name,
		trader.FirstName,
		trader.LastName,
		trader.Email,
		trader.Phone,
		trader.Status,
		trader.Roles,
		trader.PasswordHash,
		trader.StoreId,
		trader.IsActive,
		trader.CreatedAt,
		trader.UpdatedAt,
	).Scan(&trader.Id, &trader.CreatedAt, &trader.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create trader: %w", err)
	}

	return nil
}

// GetTrader récupère un cambiste par son ID.
func (r *TraderRepository) GetTrader(id int) (*entity.Trader, error) {
	query := `
		SELECT id, COALESCE(name, ''), COALESCE(first_name, ''), COALESCE(last_name, ''),
		       email, COALESCE(phone, ''), COALESCE(status, ''), COALESCE(role, ''),
		       password_hash, COALESCE(store_id, 0), is_active, created_at, updated_at, deleted_at
		FROM traders 
		WHERE id = $1 AND deleted_at IS NULL`

	trader := &entity.Trader{}

	err := r.store.DB().QueryRow(query, id).Scan(
		&trader.Id,
		&trader.Name,
		&trader.FirstName,
		&trader.LastName,
		&trader.Email,
		&trader.Phone,
		&trader.Status,
		&trader.Roles,
		&trader.PasswordHash,
		&trader.StoreId,
		&trader.IsActive,
		&trader.CreatedAt,
		&trader.UpdatedAt,
		&trader.DeletedAt, // Le scan fonctionne directement avec *time.Time pour les colonnes nullables
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("trader with ID %d not found", id)
		}
		return nil, fmt.Errorf("failed to get trader: %w", err)
	}

	return trader, nil
}

// FindOrCreateTrader trouve un cambiste par email ou le crée s'il n'existe pas.
func (r *TraderRepository) FindOrCreateTrader(trader *entity.Trader) (*entity.Trader, error) {
	// 1. Essayer de trouver le cambiste par email.
	existingTrader, err := r.GetTraderByEmail(trader.Email)
	if err != nil {
		// Si l'erreur n'est pas "non trouvé", on la retourne.
		if err.Error() != fmt.Sprintf("trader with email %s not found", trader.Email) {
			return nil, fmt.Errorf("failed to check for existing trader: %w", err)
		}
		// Si l'erreur est "non trouvé", on continue pour le créer.
	}

	// 2. Si le cambiste existe déjà, on le retourne.
	if existingTrader != nil {
		return existingTrader, nil
	}

	// 3. Sinon, on le crée.
	err = r.CreateTrader(trader)
	if err != nil {
		return nil, fmt.Errorf("failed to create new trader: %w", err)
	}
	return trader, nil
}

// GetTraderByEmail récupère un cambiste par son email.
func (r *TraderRepository) GetTraderByEmail(email string) (*entity.Trader, error) {
	query := `
		SELECT id, COALESCE(name, ''), COALESCE(first_name, ''), COALESCE(last_name, ''),
		       email, COALESCE(phone, ''), COALESCE(status, ''), COALESCE(role, ''),
		       password_hash, COALESCE(store_id, 0), is_active, created_at, updated_at, deleted_at
		FROM traders 
		WHERE email = $1 AND deleted_at IS NULL`

	trader := &entity.Trader{}

	err := r.store.DB().QueryRow(query, email).Scan(
		&trader.Id,
		&trader.Name,
		&trader.FirstName,
		&trader.LastName,
		&trader.Email,
		&trader.Phone,
		&trader.Status,
		&trader.Roles,
		&trader.PasswordHash,
		&trader.StoreId,
		&trader.IsActive,
		&trader.CreatedAt,
		&trader.UpdatedAt,
		&trader.DeletedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("trader with email %s not found", email)
		}
		return nil, fmt.Errorf("failed to get trader by email: %w", err)
	}

	return trader, nil
}

// UpdateTrader met à jour les informations d'un cambiste.
func (r *TraderRepository) UpdateTrader(trader *entity.Trader) error {
	query := `
		UPDATE traders 
		SET name = $1, first_name = $2, last_name = $3, email = $4, phone = $5, 
		    status = $6, role = $7, store_id = $8, is_active = $9, updated_at = $10
		WHERE id = $11`

	trader.UpdatedAt = time.Now()

	_, err := r.store.DB().Exec(query,
		trader.Name,
		trader.FirstName,
		trader.LastName,
		trader.Email,
		trader.Phone,
		trader.Status,
		trader.Roles,
		trader.StoreId,
		trader.IsActive,
		trader.UpdatedAt,
		trader.Id,
	)
	return err
}

// DeleteTrader effectue une suppression logique (soft delete) d'un cambiste.
func (r *TraderRepository) DeleteTrader(id int) error {
	query := `UPDATE traders SET deleted_at = $1 WHERE id = $2`
	_, err := r.store.DB().Exec(query, time.Now(), id)
	return err
}
