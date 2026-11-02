package repository

import (
	"database/sql"
	"fmt"
	"fx-app-api/internal/domain/riskflag/entity"
	"fx-app-api/internal/storage"
	"strings"
	"time"
)

type RiskFlagWithDetails = storage.RiskFlagWithDetails

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

func (r *RiskFlagRepository) BeginTx() (*sql.Tx, error) { return r.store.DB().Begin() }

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
			status VARCHAR(20) NOT NULL DEFAULT 'OPEN',
			assigned_to INT REFERENCES traders(id),
			resolution_note TEXT,
			resolved_at TIMESTAMPTZ,
			resolved_by INT REFERENCES traders(id),
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMPTZ
		);`

	_, err := r.store.DB().Exec(createTableSQL)
	if err != nil {
		return err
	}

	// Migration des colonnes existantes
	alterTableSQL := `
		ALTER TABLE risk_flags
			ADD COLUMN IF NOT EXISTS status VARCHAR(20) NOT NULL DEFAULT 'OPEN',
			ADD COLUMN IF NOT EXISTS assigned_to INT REFERENCES traders(id),
			ADD COLUMN IF NOT EXISTS resolution_note TEXT,
			ADD COLUMN IF NOT EXISTS resolved_at TIMESTAMPTZ,
			ADD COLUMN IF NOT EXISTS resolved_by INT REFERENCES traders(id);`

	_, err = r.store.DB().Exec(alterTableSQL)
	if err != nil {
		return err
	}
	if _, err := r.store.DB().Exec(`ALTER TABLE risk_flags ALTER COLUMN transaction_id DROP NOT NULL`); err != nil {
		return err
	}

	// Création de la table d'historique
	createHistoryTableSQL := `
		CREATE TABLE IF NOT EXISTS risk_flag_status_history (
			id SERIAL PRIMARY KEY,
			risk_flag_id INT NOT NULL REFERENCES risk_flags(id),
			from_status VARCHAR(20),
			to_status VARCHAR(20) NOT NULL,
			changed_by INT REFERENCES traders(id),
			note TEXT,
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
		);`

	_, err = r.store.DB().Exec(createHistoryTableSQL)
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
			flag, reason, score, level, transaction_id, trader_id, customer_id, status, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10
		) RETURNING id, created_at, updated_at`

	flag.CreatedAt = time.Now()
	flag.UpdatedAt = time.Now()
	if flag.Status == "" {
		flag.Status = string(entity.RiskFlagStatusOpen)
	}

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
			flag.Status,
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
			flag.Status,
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
		       COALESCE(status, ''), COALESCE(assigned_to, 0), COALESCE(resolution_note, ''),
		       resolved_at, COALESCE(resolved_by, 0),
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
		&flag.Status,
		&flag.AssignedTo,
		&flag.ResolutionNote,
		&flag.ResolvedAt,
		&flag.ResolvedBy,
		&flag.CreatedAt,
		&flag.UpdatedAt,
		&flag.DeletedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("%w: id %d", storage.ErrRiskFlagNotFound, id)
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
		       COALESCE(status, ''), COALESCE(assigned_to, 0), COALESCE(resolution_note, ''),
		       resolved_at, COALESCE(resolved_by, 0),
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
	if filter.Status != "" {
		args = append(args, filter.Status)
		conditions = append(conditions, fmt.Sprintf("status = $%d", len(args)))
	}
	if filter.AssignedTo > 0 {
		args = append(args, filter.AssignedTo)
		conditions = append(conditions, fmt.Sprintf("assigned_to = $%d", len(args)))
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
			&flag.Status,
			&flag.AssignedTo,
			&flag.ResolutionNote,
			&flag.ResolvedAt,
			&flag.ResolvedBy,
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

// ListRiskFlagsWithDetails liste les indicateurs de risque avec les détails du client et de la transaction.
func (r *RiskFlagRepository) ListRiskFlagsWithDetails(filter storage.RiskFlagFilter) ([]*RiskFlagWithDetails, int, error) {
	query := `
		SELECT COUNT(*) OVER(), f.id, f.flag, COALESCE(f.reason, ''), COALESCE(f.score, 0), COALESCE(f.level, ''),
		       f.transaction_id, COALESCE(f.trader_id, 0), COALESCE(f.customer_id, 0),
		       COALESCE(f.status, ''), COALESCE(f.assigned_to, 0), COALESCE(f.resolution_note, ''),
		       f.resolved_at, COALESCE(f.resolved_by, 0),
		       f.created_at, f.updated_at, f.deleted_at,
		       c.id, COALESCE(c.full_name, ''), COALESCE(c.phone, ''), COALESCE(k.kyc_status, 'UNVERIFIED'),
		       t.id, t.type, t.currency, t.amount, t.rate, t.converted_amount, t.created_at
		FROM risk_flags f
		LEFT JOIN customers c ON c.id = f.customer_id
		LEFT JOIN customer_kyc_profiles k ON k.customer_id = c.id
		LEFT JOIN transactions t ON t.id = f.transaction_id
		WHERE f.deleted_at IS NULL`

	var args []any
	var conditions []string
	if filter.TransactionID > 0 {
		args = append(args, filter.TransactionID)
		conditions = append(conditions, fmt.Sprintf("f.transaction_id = $%d", len(args)))
	}
	if filter.TraderID > 0 {
		args = append(args, filter.TraderID)
		conditions = append(conditions, fmt.Sprintf("f.trader_id = $%d", len(args)))
	}
	if filter.CustomerID > 0 {
		args = append(args, filter.CustomerID)
		conditions = append(conditions, fmt.Sprintf("f.customer_id = $%d", len(args)))
	}
	if filter.Status != "" {
		args = append(args, filter.Status)
		conditions = append(conditions, fmt.Sprintf("f.status = $%d", len(args)))
	}
	if filter.Level != "" {
		args = append(args, filter.Level)
		conditions = append(conditions, fmt.Sprintf("f.level = $%d", len(args)))
	}
	if filter.Query != "" {
		args = append(args, "%"+strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(filter.Query)+"%")
		conditions = append(conditions, fmt.Sprintf("(c.full_name ILIKE $%d ESCAPE E'\\\\' OR c.phone ILIKE $%d ESCAPE E'\\\\')", len(args), len(args)))
	}
	if filter.AssignedTo > 0 {
		args = append(args, filter.AssignedTo)
		conditions = append(conditions, fmt.Sprintf("f.assigned_to = $%d", len(args)))
	}
	if len(conditions) > 0 {
		query += " AND " + strings.Join(conditions, " AND ")
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)
	query += fmt.Sprintf(" ORDER BY f.created_at DESC, f.id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))

	rows, err := r.store.DB().Query(query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list risk flags with details: %w", err)
	}
	defer rows.Close()

	var flags []*RiskFlagWithDetails
	var total int
	for rows.Next() {
		flag := &RiskFlagWithDetails{}
		var customerID, transactionID sql.NullInt64
		var fullName, phone, kycStatus sql.NullString
		var transactionType, currency sql.NullString
		var amount, rate, converted sql.NullFloat64
		var transactionCreated sql.NullTime
		if err := rows.Scan(
			&total,
			&flag.ID,
			&flag.Flag,
			&flag.Reason,
			&flag.Score,
			&flag.Level,
			&flag.TransactionId,
			&flag.TraderId,
			&flag.CustomerId,
			&flag.Status,
			&flag.AssignedTo,
			&flag.ResolutionNote,
			&flag.ResolvedAt,
			&flag.ResolvedBy,
			&flag.CreatedAt,
			&flag.UpdatedAt,
			&flag.DeletedAt,
			&customerID, &fullName, &phone, &kycStatus,
			&transactionID, &transactionType, &currency, &amount, &rate, &converted, &transactionCreated,
		); err != nil {
			return nil, 0, fmt.Errorf("failed to scan risk flag with details: %w", err)
		}
		if customerID.Valid {
			flag.Customer = &storage.RiskFlagCustomer{ID: int(customerID.Int64), FullName: fullName.String, Phone: phone.String, KYCStatus: kycStatus.String}
		}
		flag.CustomerName, flag.CustomerPhone = fullName.String, phone.String
		if transactionID.Valid {
			flag.Transaction = &storage.RiskFlagTransaction{ID: int(transactionID.Int64), Type: transactionType.String, Currency: currency.String, Amount: amount.Float64, Rate: rate.Float64, ConvertedAmount: converted.Float64}
			if transactionCreated.Valid {
				flag.Transaction.CreatedAt = transactionCreated.Time
			}
		}
		flags = append(flags, flag)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("failed to iterate risk flags with details: %w", err)
	}

	return flags, total, nil
}

// DeleteRiskFlag effectue une suppression logique d'un indicateur de risque.
func (r *RiskFlagRepository) DeleteRiskFlag(id int) error {
	return r.DeleteRiskFlagWithTx(nil, id)
}

func (r *RiskFlagRepository) DeleteRiskFlagWithTx(tx *sql.Tx, id int) error {
	query := `UPDATE risk_flags SET deleted_at = $1, updated_at = $1 WHERE id = $2 AND deleted_at IS NULL`

	var result sql.Result
	var err error
	if tx != nil {
		result, err = tx.Exec(query, time.Now(), id)
	} else {
		result, err = r.store.DB().Exec(query, time.Now(), id)
	}
	if err != nil {
		return fmt.Errorf("failed to delete risk flag: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check risk flag delete result: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%w: id %d", storage.ErrRiskFlagNotFound, id)
	}

	return nil
}

// UpdateRiskFlagStatus met à jour le statut d'un risk_flag avec historique.
func (r *RiskFlagRepository) UpdateRiskFlagStatus(tx *sql.Tx, flagID int, newStatus string, changedBy int, note string, assignedTo *int) (*entity.RiskFlag, error) {
	if tx == nil {
		createdTx, err := r.store.DB().Begin()
		if err != nil {
			return nil, fmt.Errorf("failed to begin risk flag transition: %w", err)
		}
		updated, err := r.UpdateRiskFlagStatus(createdTx, flagID, newStatus, changedBy, note, assignedTo)
		if err != nil {
			_ = createdTx.Rollback()
			return nil, err
		}
		if err := createdTx.Commit(); err != nil {
			return nil, fmt.Errorf("failed to commit risk flag transition: %w", err)
		}
		return updated, nil
	}
	// Récupérer le flag actuel
	query := `SELECT id, status, assigned_to, resolution_note, resolved_at, resolved_by FROM risk_flags WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`
	var currentStatus string
	var currentAssignedTo *int
	var currentResolutionNote string
	var currentResolvedAt *time.Time
	var currentResolvedBy *int

	err := tx.QueryRow(query, flagID).Scan(&flagID, &currentStatus, &currentAssignedTo, &currentResolutionNote, &currentResolvedAt, &currentResolvedBy)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("%w: id %d", storage.ErrRiskFlagNotFound, flagID)
		}
		return nil, fmt.Errorf("failed to get risk flag: %w", err)
	}

	// Vérifier la transition autorisée
	if !isValidStatusTransition(currentStatus, newStatus) {
		return nil, fmt.Errorf("%w: from %s to %s", storage.ErrInvalidStatusTransition, currentStatus, newStatus)
	}

	// Valider resolution_note pour RESOLVED et FALSE_POSITIVE
	note = strings.TrimSpace(note)
	if (newStatus == string(entity.RiskFlagStatusResolved) || newStatus == string(entity.RiskFlagStatusFalsePositive)) && note == "" {
		return nil, fmt.Errorf("%w: %s", storage.ErrResolutionNoteRequired, newStatus)
	}

	// Valider assigned_to pour IN_REVIEW
	if newStatus == string(entity.RiskFlagStatusInReview) {
		if assignedTo != nil && *assignedTo <= 0 {
			return nil, storage.ErrAssignedToRequired
		}
		if assignedTo == nil || *assignedTo == 0 {
			if currentAssignedTo == nil || *currentAssignedTo == 0 {
				return nil, storage.ErrAssignedToRequired
			}
			// Utiliser l'assigned_to existant
			assignedTo = currentAssignedTo
		}
	}

	// Mettre à jour le flag
	updateQuery := `
		UPDATE risk_flags SET
			status = $1,
			assigned_to = COALESCE($2, assigned_to),
			resolution_note = $3,
			resolved_at = CASE WHEN $1 IN ('RESOLVED', 'FALSE_POSITIVE') THEN NOW() ELSE resolved_at END,
			resolved_by = CASE WHEN $1 IN ('RESOLVED', 'FALSE_POSITIVE') THEN $4 ELSE resolved_by END,
			updated_at = NOW()
		WHERE id = $5`

	_, err = tx.Exec(updateQuery, newStatus, assignedTo, note, changedBy, flagID)
	if err != nil {
		return nil, fmt.Errorf("failed to update risk flag status: %w", err)
	}

	// Insérer dans l'historique
	historyQuery := `
		INSERT INTO risk_flag_status_history (risk_flag_id, from_status, to_status, changed_by, note)
		VALUES ($1, $2, $3, $4, $5)`

	_, err = tx.Exec(historyQuery, flagID, currentStatus, newStatus, changedBy, note)
	if err != nil {
		return nil, fmt.Errorf("failed to insert status history: %w", err)
	}

	// Lire la version mise à jour dans la même transaction.
	query = `SELECT id, flag, COALESCE(reason, ''), COALESCE(score, 0), COALESCE(level, ''), transaction_id,
		COALESCE(trader_id, 0), COALESCE(customer_id, 0), COALESCE(status, ''), assigned_to,
		COALESCE(resolution_note, ''), resolved_at, resolved_by, created_at, updated_at, deleted_at
		FROM risk_flags WHERE id = $1 AND deleted_at IS NULL`
	updatedFlag := &entity.RiskFlag{}
	err = tx.QueryRow(query, flagID).Scan(&updatedFlag.ID, &updatedFlag.Flag, &updatedFlag.Reason, &updatedFlag.Score,
		&updatedFlag.Level, &updatedFlag.TransactionId, &updatedFlag.TraderId, &updatedFlag.CustomerId, &updatedFlag.Status,
		&updatedFlag.AssignedTo, &updatedFlag.ResolutionNote, &updatedFlag.ResolvedAt, &updatedFlag.ResolvedBy,
		&updatedFlag.CreatedAt, &updatedFlag.UpdatedAt, &updatedFlag.DeletedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to read updated risk flag: %w", err)
	}

	return updatedFlag, nil
}

// ListRiskFlagStatusHistory liste l'historique des statuts d'un risk_flag.
func (r *RiskFlagRepository) ListRiskFlagStatusHistory(flagID int) ([]*entity.RiskFlagStatusHistory, error) {
	query := `
		SELECT id, risk_flag_id, from_status, to_status, changed_by, COALESCE(note, ''), created_at
		FROM risk_flag_status_history
		WHERE risk_flag_id = $1
		ORDER BY created_at ASC, id ASC`

	rows, err := r.store.DB().Query(query, flagID)
	if err != nil {
		return nil, fmt.Errorf("failed to list status history: %w", err)
	}
	defer rows.Close()

	var history []*entity.RiskFlagStatusHistory
	for rows.Next() {
		h := &entity.RiskFlagStatusHistory{}
		if err := rows.Scan(&h.ID, &h.RiskFlagID, &h.FromStatus, &h.ToStatus, &h.ChangedBy, &h.Note, &h.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan status history: %w", err)
		}
		history = append(history, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate status history: %w", err)
	}

	return history, nil
}

// isValidStatusTransition vérifie si une transition de statut est autorisée.
func isValidStatusTransition(from, to string) bool {
	switch from {
	case string(entity.RiskFlagStatusOpen):
		return to == string(entity.RiskFlagStatusInReview) ||
			to == string(entity.RiskFlagStatusFalsePositive)
	case string(entity.RiskFlagStatusInReview):
		return to == string(entity.RiskFlagStatusResolved) ||
			to == string(entity.RiskFlagStatusFalsePositive) ||
			to == string(entity.RiskFlagStatusOpen)
	case string(entity.RiskFlagStatusResolved), string(entity.RiskFlagStatusFalsePositive):
		// Les statuts finaux ne peuvent pas changer
		return false
	default:
		return false
	}
}
