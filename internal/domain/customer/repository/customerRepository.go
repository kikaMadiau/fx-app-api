package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"fx-app-api/internal/domain/customer/entity"
	"fx-app-api/internal/storage"
	"strings"
	"time"

	"github.com/lib/pq"
)

const (
	defaultCustomerListLimit = 50
	maxCustomerListLimit     = 200
)

// sqlExecutor est implémenté à la fois par *sql.DB et *sql.Tx : il permet d'écrire
// une seule fois les requêtes qui doivent pouvoir tourner avec ou sans transaction.
type sqlExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

// pickExecutor renvoie la transaction si elle est fournie, la connexion sinon.
func pickExecutor(db *sql.DB, tx *sql.Tx) sqlExecutor {
	if tx != nil {
		return tx
	}

	return db
}

// CustomerRepository est l'implémentation concrète de storage.CustomerStorage.
type CustomerRepository struct {
	store *storage.PostgresStore
}

// NewCustomerRepository crée une nouvelle instance qui implémente storage.CustomerStorage.
func NewCustomerRepository(store *storage.PostgresStore) storage.CustomerStorage {
	return &CustomerRepository{store: store}
}

// Store retourne la store PostgreSQL sous-jacente.
func (r *CustomerRepository) Store() *storage.PostgresStore {
	return r.store
}

// BeginTx ouvre une transaction SQL.
func (r *CustomerRepository) BeginTx() (*sql.Tx, error) {
	return r.store.DB().Begin()
}

// Init crée la table 'customers' si elle n'existe pas.
func (r *CustomerRepository) Init() error {
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS customers (
			id SERIAL PRIMARY KEY,
			full_name VARCHAR(255) NOT NULL,
			id_number VARCHAR(100) NOT NULL,
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

	if _, err := r.store.DB().Exec(alterTableSQL); err != nil {
		return err
	}

	// Les index d'unicité ignorent les clients supprimés (soft delete) : un numéro de pièce
	// ou un téléphone libéré par une suppression peut être réutilisé. L'ancienne contrainte
	// UNIQUE sur id_number (créée avant ce changement) est donc remplacée par un index partiel.
	if _, err := r.store.DB().Exec(`ALTER TABLE customers DROP CONSTRAINT IF EXISTS customers_id_number_key`); err != nil {
		return err
	}

	uniqueIDNumberSQL := `
		CREATE UNIQUE INDEX IF NOT EXISTS customers_id_number_unique_idx
		ON customers (id_number)
		WHERE deleted_at IS NULL;`

	if _, err := r.store.DB().Exec(uniqueIDNumberSQL); err != nil {
		return err
	}

	uniquePhoneSQL := `
		CREATE UNIQUE INDEX IF NOT EXISTS customers_phone_unique_idx
		ON customers (phone)
		WHERE phone IS NOT NULL AND phone <> '' AND deleted_at IS NULL;`

	_, err := r.store.DB().Exec(uniquePhoneSQL)
	return err
}

// CreateCustomer insère un nouveau client dans la base de données.
func (r *CustomerRepository) CreateCustomer(customer *entity.Customer) error {
	return r.CreateCustomerWithTx(nil, customer)
}

// CreateCustomerWithTx insère un nouveau client, dans la transaction fournie si elle n'est pas nil.
func (r *CustomerRepository) CreateCustomerWithTx(tx *sql.Tx, customer *entity.Customer) error {
	if err := normalizeCustomerPhone(customer); err != nil {
		return err
	}

	query := `
		INSERT INTO customers (
			full_name, id_number, id_type, phone, address, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7
		) RETURNING id, created_at, updated_at`

	customer.CreatedAt = time.Now()
	customer.UpdatedAt = time.Now()

	err := pickExecutor(r.store.DB(), tx).QueryRow(query,
		customer.FullName,
		customer.IDNumber,
		customer.IDType,
		customer.Phone,
		customer.Address,
		customer.CreatedAt,
		customer.UpdatedAt,
	).Scan(&customer.ID, &customer.CreatedAt, &customer.UpdatedAt)

	if err != nil {
		return mapCustomerWriteError("create customer", err)
	}

	return nil
}

// GetCustomer récupère un client par son ID.
func (r *CustomerRepository) GetCustomer(id int) (*entity.Customer, error) {
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
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: id %d", storage.ErrCustomerIDNotFound, id)
		}
		return nil, fmt.Errorf("failed to get customer: %w", err)
	}

	return customer, nil
}

// GetCustomerByPhone récupère un client par son numéro de téléphone normalisé.
func (r *CustomerRepository) GetCustomerByPhone(phone string) (*entity.Customer, error) {
	normalizedPhone, err := storage.NormalizePhone(phone)
	if err != nil {
		return nil, err
	}

	customer, err := r.getCustomerByPhone(normalizedPhone)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", storage.ErrCustomerNotFound, normalizedPhone)
		}
		return nil, fmt.Errorf("failed to get customer by phone: %w", err)
	}

	return customer, nil
}

// UpdateCustomer met à jour les informations d'un client existant dans la base de données.
func (r *CustomerRepository) UpdateCustomer(customer *entity.Customer) error {
	return r.UpdateCustomerWithTx(nil, customer)
}

// UpdateCustomerWithTx met à jour les informations d'un client existant dans la base de données avec une transaction SQL.
// Elle renvoie storage.ErrCustomerIDNotFound si le client n'existe pas (ou a été supprimé).
func (r *CustomerRepository) UpdateCustomerWithTx(tx *sql.Tx, customer *entity.Customer) error {
	if err := normalizeCustomerPhone(customer); err != nil {
		return err
	}

	query := `
		UPDATE customers SET
			full_name = $1, id_number = $2, id_type = $3, phone = $4, address = $5,
			risk_level = $6, risk_score = $7, updated_at = $8
		WHERE id = $9 AND deleted_at IS NULL`

	customer.UpdatedAt = time.Now()

	result, err := pickExecutor(r.store.DB(), tx).Exec(query,
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
	if err != nil {
		return mapCustomerWriteError("update customer", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check customer update result: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%w: id %d", storage.ErrCustomerIDNotFound, customer.ID)
	}

	return nil
}

// ListCustomers renvoie une page de clients actifs, enrichis du statut KYC, du nombre de
// transactions et des alertes de risque encore ouvertes, ainsi que le nombre total de résultats.
// La requête lit aussi les tables transactions et risk_flags : elles sont toutes créées au démarrage.
func (r *CustomerRepository) ListCustomers(filter storage.CustomerFilter) ([]*entity.CustomerSummary, int, error) {
	conditions := []string{"c.deleted_at IS NULL"}
	args := []any{}

	addArg := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}

	if query := strings.TrimSpace(filter.Query); query != "" {
		placeholder := addArg("%" + escapeLikePattern(query) + "%")
		clause := fmt.Sprintf(
			"c.full_name ILIKE %[1]s OR c.id_number ILIKE %[1]s OR c.phone ILIKE %[1]s",
			placeholder,
		)

		// Un numéro saisi avec des espaces ou des tirets ("+243 810 000 001") est aussi
		// comparé à sa forme normalisée, qui est celle stockée en base.
		if normalizedPhone, err := storage.NormalizePhone(query); err == nil && normalizedPhone != query {
			clause += " OR c.phone ILIKE " + addArg("%"+escapeLikePattern(normalizedPhone)+"%")
		}

		conditions = append(conditions, "("+clause+")")
	}
	if filter.KYCStatus != "" {
		conditions = append(conditions, "k.kyc_status = "+addArg(filter.KYCStatus))
	}
	if filter.RiskLevel != "" {
		conditions = append(conditions, "c.risk_level = "+addArg(filter.RiskLevel))
	}

	where := strings.Join(conditions, " AND ")
	const fromClause = `
		FROM customers c
		LEFT JOIN customer_kyc_profiles k ON k.customer_id = c.id`

	var total int
	countQuery := "SELECT COUNT(*) " + fromClause + " WHERE " + where
	if err := r.store.DB().QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count customers: %w", err)
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = defaultCustomerListLimit
	}
	if limit > maxCustomerListLimit {
		limit = maxCustomerListLimit
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	listArgs := append(append([]any{}, args...), limit, offset)
	limitPlaceholder := fmt.Sprintf("$%d", len(args)+1)
	offsetPlaceholder := fmt.Sprintf("$%d", len(args)+2)

	listQuery := `
		SELECT c.id, c.full_name, c.id_number, COALESCE(c.id_type, ''), COALESCE(c.phone, ''),
		       COALESCE(c.address, ''), c.created_at, c.updated_at, c.deleted_at,
		       COALESCE(c.risk_level, ''), COALESCE(c.risk_score, 0),
		       COALESCE(k.kyc_status, ''),
		       (SELECT COUNT(*) FROM transactions t
		         WHERE t.customer_id = c.id AND t.deleted_at IS NULL),
		       (SELECT COUNT(*) FROM risk_flags f
		         WHERE f.customer_id = c.id AND f.deleted_at IS NULL
		           AND f.status IN ('OPEN', 'IN_REVIEW')),
		       COALESCE((SELECT COALESCE(NULLIF(f.reason, ''), f.flag) FROM risk_flags f
		         WHERE f.customer_id = c.id AND f.deleted_at IS NULL
		           AND f.status IN ('OPEN', 'IN_REVIEW')
		         ORDER BY f.created_at DESC, f.id DESC LIMIT 1), '')` +
		fromClause + `
		WHERE ` + where + `
		ORDER BY c.created_at DESC, c.id DESC
		LIMIT ` + limitPlaceholder + ` OFFSET ` + offsetPlaceholder

	rows, err := r.store.DB().Query(listQuery, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list customers: %w", err)
	}
	defer rows.Close()

	customers := make([]*entity.CustomerSummary, 0, limit)
	for rows.Next() {
		summary := &entity.CustomerSummary{}
		if err := rows.Scan(
			&summary.ID,
			&summary.FullName,
			&summary.IDNumber,
			&summary.IDType,
			&summary.Phone,
			&summary.Address,
			&summary.CreatedAt,
			&summary.UpdatedAt,
			&summary.DeletedAt,
			&summary.RiskLevel,
			&summary.RiskScore,
			&summary.KYCStatus,
			&summary.TransactionCount,
			&summary.OpenRiskFlags,
			&summary.LatestFlagReason,
		); err != nil {
			return nil, 0, fmt.Errorf("failed to scan customer: %w", err)
		}
		customers = append(customers, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("failed to read customers: %w", err)
	}

	return customers, total, nil
}

func (r *CustomerRepository) getCustomerByPhone(normalizedPhone string) (*entity.Customer, error) {
	query := `
		SELECT id, full_name, id_number, COALESCE(id_type, ''), COALESCE(phone, ''),
		       COALESCE(address, ''), created_at, updated_at, deleted_at,
		       COALESCE(risk_level, ''), COALESCE(risk_score, 0)
		FROM customers
		WHERE phone = $1 AND deleted_at IS NULL`

	customer := &entity.Customer{}
	err := r.store.DB().QueryRow(query, strings.TrimSpace(normalizedPhone)).Scan(
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

	return customer, err
}

// normalizeCustomerPhone met le téléphone du client sous sa forme normalisée.
// Un téléphone vide reste accepté (la colonne est optionnelle), un téléphone invalide est refusé.
func normalizeCustomerPhone(customer *entity.Customer) error {
	if strings.TrimSpace(customer.Phone) == "" {
		customer.Phone = ""
		return nil
	}

	normalizedPhone, err := storage.NormalizePhone(customer.Phone)
	if err != nil {
		return fmt.Errorf("invalid customer phone: %w", err)
	}
	customer.Phone = normalizedPhone

	return nil
}

// mapCustomerWriteError traduit une violation d'unicité en erreur métier explicite
// (téléphone ou numéro de pièce déjà utilisé). Les autres erreurs sont simplement enveloppées.
func mapCustomerWriteError(action string, err error) error {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		switch {
		case strings.Contains(pqErr.Constraint, "phone"):
			return fmt.Errorf("failed to %s: %w", action, storage.ErrCustomerPhoneExists)
		case strings.Contains(pqErr.Constraint, "id_number"):
			return fmt.Errorf("failed to %s: %w", action, storage.ErrCustomerIDNumberExists)
		}
	}

	return fmt.Errorf("failed to %s: %w", action, err)
}

// escapeLikePattern neutralise les caractères spéciaux de LIKE/ILIKE saisis par l'utilisateur.
func escapeLikePattern(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}
