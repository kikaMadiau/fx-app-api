package repository

import (
	"database/sql"
	"errors"
	"fmt"
	customerentity "fx-app-api/internal/domain/customer/entity"
	customerrepository "fx-app-api/internal/domain/customer/repository"
	"fx-app-api/internal/domain/transaction/entity"
	"fx-app-api/internal/storage"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTransactionListLimit = 20
	maxTransactionListLimit     = 100

	// Colonnes lues par toutes les requêtes de lecture, dans l'ordre attendu par scanTransaction.
	transactionColumns = `t.id, t.amount, t.currency, COALESCE(t.target_currency, ''),
		       COALESCE(t.type, ''), COALESCE(t.rate, 0),
		       COALESCE(t.converted_amount, 0), COALESCE(t.risk_level, ''), COALESCE(t.risk_score, 0),
		       COALESCE(t.status, ''), t.created_at, t.updated_at, t.deleted_at, t.trader_id, t.customer_id`
)

// sqlExecutor est implémenté à la fois par *sql.DB et *sql.Tx : il permet d'écrire une seule fois
// les requêtes qui doivent pouvoir tourner avec ou sans transaction.
type sqlExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// pickExecutor renvoie la transaction si elle est fournie, la connexion sinon.
func pickExecutor(db *sql.DB, tx *sql.Tx) sqlExecutor {
	if tx != nil {
		return tx
	}

	return db
}

// TransactionRepository est l'implémentation concrète de storage.TransactionStorage.
type TransactionRepository struct {
	store *storage.PostgresStore
}

// NewTransactionRepository crée une nouvelle instance qui implémente storage.TransactionStorage.
func NewTransactionRepository(store *storage.PostgresStore) storage.TransactionStorage {
	return &TransactionRepository{store: store}
}

// Store retourne la store PostgreSQL sous-jacente.
func (r *TransactionRepository) Store() *storage.PostgresStore {
	return r.store
}

// BeginTx ouvre une transaction SQL.
func (r *TransactionRepository) BeginTx() (*sql.Tx, error) {
	return r.store.DB().Begin()
}

// Init crée la table 'transactions' si elle n'existe pas.
func (r *TransactionRepository) Init() error {
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

	if _, err := r.store.DB().Exec(alterTableSQL); err != nil {
		return err
	}

	// Index des lectures fréquentes : historique d'un client (analyse AML sur 30 jours) et liste récente.
	if _, err := r.store.DB().Exec(`
		CREATE INDEX IF NOT EXISTS transactions_customer_created_idx
		ON transactions (customer_id, created_at DESC) WHERE deleted_at IS NULL`); err != nil {
		return err
	}

	_, err := r.store.DB().Exec(`
		CREATE INDEX IF NOT EXISTS transactions_created_idx
		ON transactions (created_at DESC, id DESC) WHERE deleted_at IS NULL`)
	return err
}

// CreateTransaction insère une nouvelle transaction dans la base de données.
func (r *TransactionRepository) CreateTransaction(tx *entity.Transaction) error {
	return r.CreateTransactionWithTx(nil, tx)
}

// CreateTransactionWithTx insère une nouvelle transaction, dans la transaction SQL fournie si elle n'est pas nil.
// Elle renvoie storage.ErrTraderNotFound ou storage.ErrCustomerIDNotFound si le trader ou le client n'existe pas.
func (r *TransactionRepository) CreateTransactionWithTx(dbTx *sql.Tx, tx *entity.Transaction) error {
	if err := insertTransaction(pickExecutor(r.store.DB(), dbTx), tx); err != nil {
		return mapTransactionWriteError("create transaction", err)
	}

	return nil
}

// CreateTransactionWithNewCustomer enregistre un client puis sa transaction dans une seule transaction SQL.
// Si un client actif existe déjà avec ce téléphone, il est réutilisé sans être modifié, et customer
// est remplacé par ce client existant.
// Elle retourne la transaction SQL non commitée pour permettre au handler de décider du commit/rollback après l'analyse AML.
func (r *TransactionRepository) CreateTransactionWithNewCustomer(tx *entity.Transaction, customer *customerentity.Customer) (*sql.Tx, error) {
	normalizedPhone, err := storage.NormalizePhone(customer.Phone)
	if err != nil {
		return nil, err
	}
	customer.Phone = normalizedPhone
	if strings.TrimSpace(customer.IDNumber) == "" {
		customer.IDNumber = normalizedPhone
	}

	dbTx, err := r.store.DB().Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	existing, err := selectCustomerByPhone(dbTx, normalizedPhone, customer.StoreID, true)
	switch {
	case err == nil:
		*customer = *existing
	case errors.Is(err, sql.ErrNoRows):
		// La création passe par le repository client : même normalisation et mêmes erreurs d'unicité
		// (téléphone ou numéro de pièce déjà utilisé) que partout ailleurs.
		if err := customerrepository.NewCustomerRepository(r.store).CreateCustomerWithTx(dbTx, customer); err != nil {
			_ = dbTx.Rollback()
			return nil, err
		}
	default:
		_ = dbTx.Rollback()
		return nil, fmt.Errorf("failed to check existing customer: %w", err)
	}

	tx.CustomerID = customer.ID
	if err := insertTransaction(dbTx, tx); err != nil {
		_ = dbTx.Rollback()
		return nil, mapTransactionWriteError("create transaction", err)
	}

	// Ne pas commit ici, laisser le handler décider
	return dbTx, nil
}

// CreateTransactionForExistingCustomerPhone associe une transaction à un client existant sans le modifier.
// Elle retourne la transaction SQL non commitée pour permettre au handler de décider du commit/rollback après l'analyse AML.
func (r *TransactionRepository) CreateTransactionForExistingCustomerPhone(tx *entity.Transaction, phone string) (*sql.Tx, *customerentity.Customer, error) {
	normalizedPhone, err := storage.NormalizePhone(phone)
	if err != nil {
		return nil, nil, err
	}

	dbTx, err := r.store.DB().Begin()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	customer, err := selectCustomerByPhone(dbTx, normalizedPhone, tx.StoreID, true)
	if err != nil {
		_ = dbTx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, storage.ErrCustomerNotFound
		}
		return nil, nil, fmt.Errorf("failed to get customer by phone: %w", err)
	}

	tx.CustomerID = customer.ID
	if err := insertTransaction(dbTx, tx); err != nil {
		_ = dbTx.Rollback()
		return nil, nil, mapTransactionWriteError("create transaction", err)
	}

	// Ne pas commit ici, laisser le handler décider
	return dbTx, customer, nil
}

// UpdateTransaction met à jour une transaction existante dans la base de données.
func (r *TransactionRepository) UpdateTransaction(tx *entity.Transaction) error {
	return r.UpdateTransactionWithTx(nil, tx)
}

// UpdateTransactionWithTx met à jour une transaction existante avec une transaction SQL.
// Elle renvoie storage.ErrTransactionNotFound si la transaction n'existe pas (ou a été supprimée).
func (r *TransactionRepository) UpdateTransactionWithTx(tx *sql.Tx, txToUpdate *entity.Transaction) error {
	query := `
		UPDATE transactions SET
			amount = $1, currency = $2, target_currency = $3, type = $4,
			rate = $5, converted_amount = $6, risk_level = $7, risk_score = $8,
			status = $9, trader_id = $10, customer_id = $11, updated_at = $12
		WHERE id = $13 AND deleted_at IS NULL`

	txToUpdate.UpdatedAt = time.Now()

	result, err := pickExecutor(r.store.DB(), tx).Exec(query,
		txToUpdate.Amount,
		txToUpdate.Currency,
		txToUpdate.TargetCurrency,
		txToUpdate.Type,
		txToUpdate.Rate,
		txToUpdate.ConvertedAmount,
		txToUpdate.RiskLevel,
		txToUpdate.RiskScore,
		txToUpdate.Status,
		txToUpdate.TraderID,
		txToUpdate.CustomerID,
		txToUpdate.UpdatedAt,
		txToUpdate.ID,
	)
	if err != nil {
		return mapTransactionWriteError("update transaction", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check transaction update result: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%w: id %d", storage.ErrTransactionNotFound, txToUpdate.ID)
	}

	return nil
}

// GetTransaction récupère une transaction par son ID.
func (r *TransactionRepository) GetTransaction(id int) (*entity.Transaction, error) {
	query := `SELECT ` + transactionColumns + `
		FROM transactions t
		WHERE t.id = $1 AND t.deleted_at IS NULL`

	tx, err := scanTransaction(r.store.DB().QueryRow(query, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: id %d", storage.ErrTransactionNotFound, id)
		}
		return nil, fmt.Errorf("failed to get transaction: %w", err)
	}

	return tx, nil
}

// ListTransactions liste les transactions, avec filtres optionnels.
func (r *TransactionRepository) ListTransactions(filter storage.TransactionFilter) ([]*entity.Transaction, error) {
	return r.ListTransactionsWithTx(nil, filter)
}

// ListTransactionsWithTx liste les transactions dans la transaction SQL fournie si elle n'est pas nil.
func (r *TransactionRepository) ListTransactionsWithTx(dbTx *sql.Tx, filter storage.TransactionFilter) ([]*entity.Transaction, error) {
	conditions := []string{"t.deleted_at IS NULL"}
	args := []any{}

	addArg := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}

	if filter.CustomerID > 0 {
		conditions = append(conditions, "t.customer_id = "+addArg(filter.CustomerID))
	}
	if filter.TraderID > 0 {
		conditions = append(conditions, "t.trader_id = "+addArg(filter.TraderID))
	}
	if filter.StoreID != 0 {
		conditions = append(conditions, "t.store_id = "+addArg(filter.StoreID))
	}
	if !filter.Since.IsZero() {
		conditions = append(conditions, "t.created_at >= "+addArg(filter.Since))
	}

	query := `SELECT ` + transactionColumns + `
		FROM transactions t
		WHERE ` + strings.Join(conditions, " AND ") + `
		ORDER BY t.created_at DESC, t.id DESC`

	rows, err := pickExecutor(r.store.DB(), dbTx).Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list transactions: %w", err)
	}
	defer rows.Close()

	var transactions []*entity.Transaction
	for rows.Next() {
		tx, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan transaction: %w", err)
		}
		transactions = append(transactions, tx)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate transactions: %w", err)
	}

	return transactions, nil
}

// SearchTransactions renvoie une page de transactions actives avec le client associé,
// ainsi que le nombre total de résultats correspondant aux filtres.
func (r *TransactionRepository) SearchTransactions(filter storage.TransactionFilter) ([]*entity.TransactionListItem, int, error) {
	conditions := []string{"t.deleted_at IS NULL"}
	args := []any{}

	addArg := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}

	if query := strings.TrimSpace(filter.Query); query != "" {
		placeholder := addArg("%" + escapeLikePattern(query) + "%")
		clause := fmt.Sprintf("c.full_name ILIKE %[1]s OR c.phone ILIKE %[1]s", placeholder)

		// Un numéro saisi avec des espaces ou des tirets est aussi comparé à sa forme normalisée.
		if normalizedPhone, err := storage.NormalizePhone(query); err == nil && normalizedPhone != query {
			clause += " OR c.phone ILIKE " + addArg("%"+escapeLikePattern(normalizedPhone)+"%")
		}
		// Un identifiant saisi tel quel ("42" ou "#42") retrouve la transaction exacte.
		if id, err := strconv.Atoi(strings.TrimPrefix(query, "#")); err == nil && id > 0 {
			clause += " OR t.id = " + addArg(id)
		}

		conditions = append(conditions, "("+clause+")")
	}
	if filter.CustomerID > 0 {
		conditions = append(conditions, "t.customer_id = "+addArg(filter.CustomerID))
	}
	if filter.TraderID > 0 {
		conditions = append(conditions, "t.trader_id = "+addArg(filter.TraderID))
	}
	if filter.StoreID != 0 {
		conditions = append(conditions, "t.store_id = "+addArg(filter.StoreID))
	}
	if filter.Status != "" {
		conditions = append(conditions, "t.status = "+addArg(filter.Status))
	}
	if filter.Type != "" {
		conditions = append(conditions, "t.type = "+addArg(filter.Type))
	}
	if filter.RiskLevel != "" {
		conditions = append(conditions, "t.risk_level = "+addArg(filter.RiskLevel))
	}
	if !filter.Since.IsZero() {
		conditions = append(conditions, "t.created_at >= "+addArg(filter.Since))
	}

	where := strings.Join(conditions, " AND ")
	const fromClause = `
		FROM transactions t
		JOIN customers c ON c.id = t.customer_id`

	var total int
	if err := r.store.DB().QueryRow("SELECT COUNT(*) "+fromClause+" WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count transactions: %w", err)
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = defaultTransactionListLimit
	}
	if limit > maxTransactionListLimit {
		limit = maxTransactionListLimit
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	listArgs := append(append([]any{}, args...), limit, offset)
	listQuery := `SELECT ` + transactionColumns + `, c.full_name, COALESCE(c.phone, ''), COALESCE(c.id_type, ''), COALESCE(c.id_number, '')` +
		fromClause + `
		WHERE ` + where + `
		ORDER BY t.created_at DESC, t.id DESC
		LIMIT ` + fmt.Sprintf("$%d", len(args)+1) + ` OFFSET ` + fmt.Sprintf("$%d", len(args)+2)

	rows, err := r.store.DB().Query(listQuery, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to search transactions: %w", err)
	}
	defer rows.Close()

	items := make([]*entity.TransactionListItem, 0, limit)
	for rows.Next() {
		item := &entity.TransactionListItem{}
		tx := &item.Transaction
		if err := rows.Scan(
			&tx.ID, &tx.Amount, &tx.Currency, &tx.TargetCurrency, &tx.Type, &tx.Rate,
			&tx.ConvertedAmount, &tx.RiskLevel, &tx.RiskScore, &tx.Status, &tx.CreatedAt, &tx.UpdatedAt, &tx.DeletedAt,
			&tx.TraderID, &tx.CustomerID,
			&item.CustomerName, &item.CustomerPhone, &item.CustomerIDType, &item.CustomerIDNumber,
		); err != nil {
			return nil, 0, fmt.Errorf("failed to scan transaction: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("failed to read transactions: %w", err)
	}

	return items, total, nil
}

// rowScanner est implémenté par *sql.Row et *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanTransaction(row rowScanner) (*entity.Transaction, error) {
	tx := &entity.Transaction{}
	err := row.Scan(
		&tx.ID, &tx.Amount, &tx.Currency, &tx.TargetCurrency, &tx.Type, &tx.Rate,
		&tx.ConvertedAmount, &tx.RiskLevel, &tx.RiskScore, &tx.Status, &tx.CreatedAt, &tx.UpdatedAt, &tx.DeletedAt,
		&tx.TraderID, &tx.CustomerID,
	)
	if err != nil {
		return nil, err
	}

	return tx, nil
}

type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

func insertTransaction(q rowQuerier, tx *entity.Transaction) error {
	query := `
		INSERT INTO transactions (
			amount, currency, target_currency, type, rate, converted_amount,
			risk_level, risk_score, status, trader_id, customer_id, created_at, updated_at, store_id
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
			(SELECT store_id FROM customers WHERE id = $11)
		) RETURNING id, created_at, updated_at, store_id`

	tx.CreatedAt = time.Now()
	tx.UpdatedAt = time.Now()

	return q.QueryRow(query, tx.Amount, tx.Currency, tx.TargetCurrency, tx.Type, tx.Rate, tx.ConvertedAmount, tx.RiskLevel, tx.RiskScore, tx.Status, tx.TraderID, tx.CustomerID, tx.CreatedAt, tx.UpdatedAt).Scan(&tx.ID, &tx.CreatedAt, &tx.UpdatedAt, &tx.StoreID)
}

// selectCustomerByPhone cherche le client dans un bureau (storeID 0 : tous les bureaux).
func selectCustomerByPhone(q rowQuerier, normalizedPhone string, storeID int, lock bool) (*customerentity.Customer, error) {
	query := `
		SELECT id, full_name, id_number, COALESCE(id_type, ''), COALESCE(phone, ''),
		       COALESCE(address, ''), created_at, updated_at, deleted_at,
		       COALESCE(risk_level, ''), COALESCE(risk_score, 0), store_id
		FROM customers
		WHERE phone = $1 AND ($2 = 0 OR store_id = $2) AND deleted_at IS NULL`
	if lock {
		query += " FOR UPDATE"
	}

	customer := &customerentity.Customer{}
	err := q.QueryRow(query, normalizedPhone, storeID).Scan(&customer.ID, &customer.FullName, &customer.IDNumber, &customer.IDType, &customer.Phone, &customer.Address, &customer.CreatedAt, &customer.UpdatedAt, &customer.DeletedAt, &customer.RiskLevel, &customer.RiskScore, &customer.StoreID)
	if err != nil {
		return nil, err
	}

	return customer, nil
}

// mapTransactionWriteError traduit une clé étrangère violée (trader ou client inexistant) en erreur
// métier explicite. Les autres erreurs sont simplement enveloppées.
func mapTransactionWriteError(action string, err error) error {
	if pqErr, ok := storage.PostgresError(err); ok && pqErr.Code == "23503" {
		switch pqErr.Constraint {
		case "fk_trader":
			return fmt.Errorf("failed to %s: %w", action, storage.ErrTraderNotFound)
		case "fk_customer":
			return fmt.Errorf("failed to %s: %w", action, storage.ErrCustomerIDNotFound)
		}
	}

	return fmt.Errorf("failed to %s: %w", action, err)
}

// escapeLikePattern neutralise les caractères spéciaux de LIKE/ILIKE saisis par l'utilisateur.
func escapeLikePattern(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}
