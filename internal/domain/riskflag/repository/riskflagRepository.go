package repository

import (
	"database/sql"
	"fmt"
	"fx-app-api/internal/domain/riskflag/entity"
	"fx-app-api/internal/storage"
	"strings"
	"time"
)

// RiskFlagRepository est l'implémentation concrète de storage.RiskFlagStorage.
type RiskFlagRepository struct {
	store *storage.PostgresStore
}

// NewRiskFlagRepository crée une nouvelle instance qui implémente storage.RiskFlagStorage.
func NewRiskFlagRepository(store *storage.PostgresStore) storage.RiskFlagStorage {
	return &RiskFlagRepository{store: store}
}

// Store retourne la store PostgreSQL sous-jacente.
func (r *RiskFlagRepository) Store() *storage.PostgresStore {
	return r.store
}

// Init crée la table 'risk_flags' si elle n'existe pas.
func (r *RiskFlagRepository) Init() error {
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS risk_flags (
			id SERIAL PRIMARY KEY,
			flag VARCHAR(255) NOT NULL,
			reason TEXT,
			score NUMERIC(5, 2),
			level VARCHAR(50),
			transaction_id INT NOT NULL,
			trader_id INT,
			customer_id INT,
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMPTZ
		);`

	_, err := r.store.DB().Exec(createTableSQL)
	return err
}

// CreateRiskFlag insère un nouvel indicateur de risque dans la base de données.
func (r *RiskFlagRepository) CreateRiskFlag(flag *entity.RiskFlag) error {
	return r.CreateRiskFlagWithTx(nil, flag)
}

// CreateRiskFlagWithTx insère un nouvel indicateur de risque dans la base de données avec une transaction SQL.
func (r *RiskFlagRepository) CreateRiskFlagWithTx(tx *sql.Tx, flag *entity.RiskFlag) error {
	query := `
		INSERT INTO risk_flags (
			flag, reason, score, level, transaction_id, trader_id, customer_id, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9
		) RETURNING id, created_at, updated_at`

	flag.CreatedAt = time.Now()
	flag.UpdatedAt = time.Now()

	var err error
	if tx != nil {
		err = tx.QueryRow(query,
			flag.Flag,
			flag.Reason,
			flag.Score,
			flag.Level,
			flag.TransactionId,
			flag.TraderId,
			flag.CustomerId,
			flag.CreatedAt,
			flag.UpdatedAt,
		).Scan(&flag.ID, &flag.CreatedAt, &flag.UpdatedAt)
	} else {
		err = r.store.DB().QueryRow(query,
			flag.Flag,
			flag.Reason,
			flag.Score,
			flag.Level,
			flag.TransactionId,
			flag.TraderId,
			flag.CustomerId,
			flag.CreatedAt,
			flag.UpdatedAt,
		).Scan(&flag.ID, &flag.CreatedAt, &flag.UpdatedAt)
	}

	if err != nil {
		return fmt.Errorf("failed to create risk flag: %w", err)
	}

	return nil
}

// GetRiskFlag récupère un indicateur de risque par son ID.
func (r *RiskFlagRepository) GetRiskFlag(id int) (*entity.RiskFlag, error) {
	query := `
		SELECT id, flag, COALESCE(reason, ''), COALESCE(score, 0), COALESCE(level, ''),
		       transaction_id, COALESCE(trader_id, 0), COALESCE(customer_id, 0),
		       created_at, updated_at, deleted_at
		FROM risk_flags
		WHERE id = $1 AND deleted_at IS NULL`

	flag := &entity.RiskFlag{}
	err := r.store.DB().QueryRow(query, id).Scan(
		&flag.ID,
		&flag.Flag,
		&flag.Reason,
		&flag.Score,
		&flag.Level,
		&flag.TransactionId,
		&flag.TraderId,
		&flag.CustomerId,
		&flag.CreatedAt,
		&flag.UpdatedAt,
		&flag.DeletedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("risk flag with ID %d not found", id)
		}
		return nil, fmt.Errorf("failed to get risk flag: %w", err)
	}

	return flag, nil
}

// ListRiskFlags liste les indicateurs de risque, avec filtres optionnels.
func (r *RiskFlagRepository) ListRiskFlags(filter storage.RiskFlagFilter) ([]*entity.RiskFlag, error) {
	query := `
		SELECT id, flag, COALESCE(reason, ''), COALESCE(score, 0), COALESCE(level, ''),
		       transaction_id, COALESCE(trader_id, 0), COALESCE(customer_id, 0),
		       created_at, updated_at, deleted_at
		FROM risk_flags
		WHERE deleted_at IS NULL`

	var args []any
	var conditions []string
	if filter.TransactionID > 0 {
		args = append(args, filter.TransactionID)
		conditions = append(conditions, fmt.Sprintf("transaction_id = $%d", len(args)))
	}
	if filter.TraderID > 0 {
		args = append(args, filter.TraderID)
		conditions = append(conditions, fmt.Sprintf("trader_id = $%d", len(args)))
	}
	if filter.CustomerID > 0 {
		args = append(args, filter.CustomerID)
		conditions = append(conditions, fmt.Sprintf("customer_id = $%d", len(args)))
	}
	if len(conditions) > 0 {
		query += " AND " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at DESC, id DESC"

	rows, err := r.store.DB().Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list risk flags: %w", err)
	}
	defer rows.Close()

	var flags []*entity.RiskFlag
	for rows.Next() {
		flag := &entity.RiskFlag{}
		if err := rows.Scan(
			&flag.ID,
			&flag.Flag,
			&flag.Reason,
			&flag.Score,
			&flag.Level,
			&flag.TransactionId,
			&flag.TraderId,
			&flag.CustomerId,
			&flag.CreatedAt,
			&flag.UpdatedAt,
			&flag.DeletedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan risk flag: %w", err)
		}
		flags = append(flags, flag)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate risk flags: %w", err)
	}

	return flags, nil
}

// DeleteRiskFlag effectue une suppression logique d'un indicateur de risque.
func (r *RiskFlagRepository) DeleteRiskFlag(id int) error {
	query := `UPDATE risk_flags SET deleted_at = $1, updated_at = $1 WHERE id = $2 AND deleted_at IS NULL`

	result, err := r.store.DB().Exec(query, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to delete risk flag: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check risk flag delete result: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("risk flag with ID %d not found", id)
	}

	return nil
}
