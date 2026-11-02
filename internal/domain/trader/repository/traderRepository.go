package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"fx-app-api/internal/domain/trader/entity"
	"fx-app-api/internal/domain/trader/permissions"
	"fx-app-api/internal/storage"
	"strings"
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
			is_active BOOLEAN NOT NULL DEFAULT TRUE,
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMPTZ
		);`

	_, err := r.store.DB().Exec(createTableSQL)
	if err != nil {
		return err
	}
	_, err = r.store.DB().Exec(`
		ALTER TABLE traders ADD COLUMN IF NOT EXISTS role VARCHAR(50) NOT NULL DEFAULT 'TRADER';
		UPDATE traders SET role = CASE UPPER(COALESCE(role, ''))
			WHEN 'MANAGER' THEN 'SUPERVISOR'
			WHEN 'TRADER' THEN 'TRADER'
			WHEN 'COMPLIANCE_OFFICER' THEN 'COMPLIANCE_OFFICER'
			WHEN 'SUPERVISOR' THEN 'SUPERVISOR'
			WHEN 'ADMIN' THEN 'ADMIN'
			ELSE 'TRADER' END;
		ALTER TABLE traders ALTER COLUMN role SET DEFAULT 'TRADER';
		ALTER TABLE traders ALTER COLUMN role SET NOT NULL;
		UPDATE traders SET is_active = TRUE WHERE is_active IS NULL;
		ALTER TABLE traders ALTER COLUMN is_active SET DEFAULT TRUE;
		ALTER TABLE traders ALTER COLUMN is_active SET NOT NULL;
		DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'traders_role_allowed_check') THEN
				ALTER TABLE traders ADD CONSTRAINT traders_role_allowed_check CHECK (role IN ('TRADER','COMPLIANCE_OFFICER','SUPERVISOR','ADMIN'));
			END IF;
		END $$;`)
	return err
}

// CreateTrader insère un nouveau cambiste dans la base de données.
func (r *TraderRepository) CreateTrader(trader *entity.Trader) error {
	return r.CreateTraderWithTx(nil, trader)
}

func (r *TraderRepository) BeginTx() (*sql.Tx, error) { return r.store.DB().Begin() }

func (r *TraderRepository) CreateTraderWithTx(tx *sql.Tx, trader *entity.Trader) error {
	if strings.TrimSpace(trader.Roles) == "" {
		trader.Roles = string(permissions.Trader)
	}
	role, ok := permissions.NormalizeRole(trader.Roles)
	if !ok {
		return fmt.Errorf("invalid trader role: %s", trader.Roles)
	}
	trader.Roles = string(role)
	query := `
		INSERT INTO traders (
			name, first_name, last_name, email, phone, status, role, password_hash, store_id, is_active, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
		) RETURNING id, created_at, updated_at`

	trader.CreatedAt = time.Now()
	trader.UpdatedAt = time.Now()

	var row interface{ Scan(...any) error }
	if tx != nil {
		row = tx.QueryRow(query,
			trader.Name, trader.FirstName, trader.LastName, trader.Email, trader.Phone, trader.Status,
			trader.Roles, trader.PasswordHash, trader.StoreId, trader.IsActive, trader.CreatedAt, trader.UpdatedAt)
	} else {
		row = r.store.DB().QueryRow(query,
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
		)
	}
	err := row.Scan(&trader.Id, &trader.CreatedAt, &trader.UpdatedAt)

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
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: id %d", storage.ErrTraderNotFound, id)
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
		if !errors.Is(err, storage.ErrTraderNotFound) {
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
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: email %s", storage.ErrTraderNotFound, email)
		}
		return nil, fmt.Errorf("failed to get trader by email: %w", err)
	}

	return trader, nil
}

// UpdateTrader met à jour les informations d'un cambiste.
func (r *TraderRepository) UpdateTrader(trader *entity.Trader) error {
	tx, err := r.BeginTx()
	if err != nil {
		return fmt.Errorf("begin trader update: %w", err)
	}
	defer tx.Rollback()
	if err := r.UpdateTraderWithTx(tx, trader); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *TraderRepository) UpdateTraderWithTx(tx *sql.Tx, trader *entity.Trader) error {
	role, ok := permissions.NormalizeRole(trader.Roles)
	if !ok {
		return fmt.Errorf("invalid trader role: %s", trader.Roles)
	}
	trader.Roles = string(role)
	query := `
		UPDATE traders 
		SET name = $1, first_name = $2, last_name = $3, email = $4, phone = $5, 
		    status = $6, role = $7, store_id = $8, is_active = $9, password_hash = $10, updated_at = $11
		WHERE id = $12`

	trader.UpdatedAt = time.Now()

	if _, err := tx.Exec(`LOCK TABLE traders IN EXCLUSIVE MODE`); err != nil {
		return fmt.Errorf("lock trader access update: %w", err)
	}
	var currentRole string
	var currentActive bool
	if err := tx.QueryRow(`SELECT COALESCE(role,''), is_active FROM traders WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, trader.Id).Scan(&currentRole, &currentActive); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: id %d", storage.ErrTraderNotFound, trader.Id)
		}
		return err
	}
	if currentRole == string(permissions.Admin) && currentActive && (trader.Roles != string(permissions.Admin) || !trader.IsActive) {
		var admins int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM traders WHERE role='ADMIN' AND is_active=TRUE AND deleted_at IS NULL`).Scan(&admins); err != nil {
			return err
		}
		if blocksLastAdminRemoval(currentRole, currentActive, admins, trader.Roles, trader.IsActive) {
			return storage.ErrLastActiveAdmin
		}
	}
	_, err := tx.Exec(query,
		trader.Name,
		trader.FirstName,
		trader.LastName,
		trader.Email,
		trader.Phone,
		trader.Status,
		trader.Roles,
		trader.StoreId,
		trader.IsActive,
		trader.PasswordHash,
		trader.UpdatedAt,
		trader.Id,
	)
	if err != nil {
		return err
	}
	return nil
}

func (r *TraderRepository) CountActiveAdmins() (int, error) {
	var count int
	err := r.store.DB().QueryRow(`SELECT COUNT(*) FROM traders WHERE deleted_at IS NULL AND is_active = TRUE AND role = 'ADMIN'`).Scan(&count)
	return count, err
}

func blocksLastAdminRemoval(currentRole string, currentActive bool, activeAdmins int, nextRole string, nextActive bool) bool {
	return currentRole == string(permissions.Admin) && currentActive && activeAdmins <= 1 && (nextRole != string(permissions.Admin) || !nextActive)
}

// DeleteTrader effectue une suppression logique (soft delete) d'un cambiste.
func (r *TraderRepository) DeleteTrader(id int) error {
	tx, err := r.BeginTx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := r.DeleteTraderWithTx(tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *TraderRepository) DeleteTraderWithTx(tx *sql.Tx, id int) error {
	if _, err := tx.Exec(`LOCK TABLE traders IN EXCLUSIVE MODE`); err != nil {
		return err
	}
	var role string
	var active bool
	if err := tx.QueryRow(`SELECT COALESCE(role,''), is_active FROM traders WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&role, &active); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: id %d", storage.ErrTraderNotFound, id)
		}
		return err
	}
	if role == string(permissions.Admin) && active {
		var admins int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM traders WHERE role='ADMIN' AND is_active=TRUE AND deleted_at IS NULL`).Scan(&admins); err != nil {
			return err
		}
		if blocksLastAdminRemoval(role, active, admins, "", false) {
			return storage.ErrLastActiveAdmin
		}
	}
	if _, err := tx.Exec(`UPDATE traders SET deleted_at = $1, updated_at=$1 WHERE id = $2`, time.Now(), id); err != nil {
		return err
	}
	return nil
}
