package notification

import (
	"database/sql"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"fx-app-api/internal/storage"
)

// Record : un message envoyé (ou non) à un client, tel qu'il est conservé dans le journal.
type Record struct {
	ID            int64     `json:"id"`
	CustomerID    int       `json:"customer_id"`
	TransactionID *int      `json:"transaction_id,omitempty"`
	Channel       string    `json:"channel"`
	Template      string    `json:"template"`
	Recipient     string    `json:"recipient"`
	Subject       string    `json:"subject,omitempty"`
	Body          string    `json:"body"`
	Status        string    `json:"status"`
	Detail        string    `json:"detail,omitempty"`
	Mode          string    `json:"mode"`
	CreatedAt     time.Time `json:"created_at"`
}

// TransactionContext : ce qu'il faut savoir d'une opération pour écrire au client.
type TransactionContext struct {
	TransactionID   int
	Amount          float64
	Currency        string
	TargetCurrency  string
	Type            string
	Rate            float64
	ConvertedAmount float64
	Status          string
	CreatedAt       time.Time
	CustomerID      int
	CustomerName    string
	Phone           string
	Email           string
	IDType          string
	IDNumber        string
	IDExpiryDate    string
	HasIDFront      bool
	HasIDBack       bool
	TraderFirstName string
	TraderLastName  string
	ExchangeOffice  string
	StoreName       string
	StoreLicense    string
}

// DayOperation : une opération du jour du client, pour le cumul journalier.
type DayOperation struct {
	ID              int
	Amount          float64
	Currency        string
	ConvertedAmount float64
	TargetCurrency  string
}

type Repository struct {
	store *storage.PostgresStore
}

func NewRepository(store *storage.PostgresStore) *Repository { return &Repository{store: store} }

func (r *Repository) Init() error {
	_, err := r.store.DB().Exec(`
		ALTER TABLE customers ADD COLUMN IF NOT EXISTS email VARCHAR(255);
		CREATE TABLE IF NOT EXISTS customer_notifications (
			id BIGSERIAL PRIMARY KEY,
			customer_id INT NOT NULL REFERENCES customers(id),
			transaction_id INT REFERENCES transactions(id),
			channel VARCHAR(10) NOT NULL,
			template VARCHAR(40) NOT NULL,
			recipient TEXT NOT NULL DEFAULT '',
			subject TEXT NOT NULL DEFAULT '',
			body TEXT NOT NULL DEFAULT '',
			status VARCHAR(10) NOT NULL,
			detail TEXT NOT NULL DEFAULT '',
			mode VARCHAR(10) NOT NULL DEFAULT 'test',
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS customer_notifications_tx_idx ON customer_notifications(transaction_id);
		CREATE INDEX IF NOT EXISTS customer_notifications_customer_idx ON customer_notifications(customer_id, template, created_at);`)
	if err != nil {
		return fmt.Errorf("init customer notifications: %w", err)
	}
	return nil
}

// NormalizeEmail nettoie l'adresse e-mail d'un client ; une chaîne vide efface l'adresse.
func NormalizeEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", nil
	}
	addr, err := mail.ParseAddress(value)
	if err != nil || addr.Address != value || len(value) > 255 || !strings.Contains(value[strings.LastIndex(value, "@"):], ".") {
		return "", fmt.Errorf("email must be a valid e-mail address")
	}
	return value, nil
}

// SetCustomerEmailWithTx enregistre l'adresse e-mail du client (vide = aucune).
func (r *Repository) SetCustomerEmailWithTx(tx *sql.Tx, customerID int, email string) error {
	query := `UPDATE customers SET email = NULLIF($1, ''), updated_at = NOW() WHERE id = $2 AND deleted_at IS NULL`
	var err error
	if tx != nil {
		_, err = tx.Exec(query, email, customerID)
	} else {
		_, err = r.store.DB().Exec(query, email, customerID)
	}
	if err != nil {
		return fmt.Errorf("set customer email: %w", err)
	}
	return nil
}

func (r *Repository) LoadTransaction(transactionID int) (*TransactionContext, error) {
	c := &TransactionContext{}
	err := r.store.DB().QueryRow(`
		SELECT t.id, t.amount, t.currency, COALESCE(t.target_currency, ''), COALESCE(t.type, ''),
		       COALESCE(t.rate, 0), COALESCE(t.converted_amount, 0), COALESCE(t.status, ''), t.created_at,
		       c.id, c.full_name, COALESCE(c.phone, ''), COALESCE(c.email, ''),
		       COALESCE(c.id_type, ''), COALESCE(c.id_number, ''),
		       COALESCE(TO_CHAR(k.id_expiry_date, 'YYYY-MM-DD'), ''),
		       EXISTS (SELECT 1 FROM customer_id_documents d WHERE d.customer_id = c.id AND d.side = 'front'),
		       EXISTS (SELECT 1 FROM customer_id_documents d WHERE d.customer_id = c.id AND d.side = 'back'),
		       COALESCE(tr.first_name, ''), COALESCE(tr.last_name, ''), COALESCE(tr.name, ''),
		       COALESCE(s.name, ''), COALESCE(s.bcc_license_number, '')
		FROM transactions t
		JOIN customers c ON c.id = t.customer_id
		JOIN traders tr ON tr.id = t.trader_id
		LEFT JOIN customer_kyc_profiles k ON k.customer_id = c.id
		LEFT JOIN stores s ON s.id = tr.store_id
		WHERE t.id = $1 AND t.deleted_at IS NULL`, transactionID).Scan(
		&c.TransactionID, &c.Amount, &c.Currency, &c.TargetCurrency, &c.Type,
		&c.Rate, &c.ConvertedAmount, &c.Status, &c.CreatedAt,
		&c.CustomerID, &c.CustomerName, &c.Phone, &c.Email,
		&c.IDType, &c.IDNumber, &c.IDExpiryDate, &c.HasIDFront, &c.HasIDBack,
		&c.TraderFirstName, &c.TraderLastName, &c.ExchangeOffice, &c.StoreName, &c.StoreLicense)
	if err != nil {
		return nil, fmt.Errorf("load transaction %d for notification: %w", transactionID, err)
	}
	return c, nil
}

// LoadCustomer : coordonnées du client et du cambiste, sans opération (refus d'une opération).
func (r *Repository) LoadCustomer(customerID, traderID int) (*TransactionContext, error) {
	c := &TransactionContext{CreatedAt: time.Now()}
	err := r.store.DB().QueryRow(`
		SELECT c.id, c.full_name, COALESCE(c.phone, ''), COALESCE(c.email, ''),
		       COALESCE(tr.first_name, ''), COALESCE(tr.last_name, ''), COALESCE(tr.name, ''),
		       COALESCE(s.name, ''), COALESCE(s.bcc_license_number, '')
		FROM customers c
		JOIN traders tr ON tr.id = $2
		LEFT JOIN stores s ON s.id = tr.store_id
		WHERE c.id = $1 AND c.deleted_at IS NULL`, customerID, traderID).Scan(
		&c.CustomerID, &c.CustomerName, &c.Phone, &c.Email,
		&c.TraderFirstName, &c.TraderLastName, &c.ExchangeOffice, &c.StoreName, &c.StoreLicense)
	if err != nil {
		return nil, fmt.Errorf("load customer %d for notification: %w", customerID, err)
	}
	return c, nil
}

// DayOperations : opérations non annulées du client depuis le début de la journée.
func (r *Repository) DayOperations(customerID int, since time.Time) ([]DayOperation, error) {
	rows, err := r.store.DB().Query(`
		SELECT id, amount, currency, COALESCE(converted_amount, 0), COALESCE(target_currency, '')
		FROM transactions
		WHERE customer_id = $1 AND deleted_at IS NULL AND status IS DISTINCT FROM 'CANCELLED' AND created_at >= $2
		ORDER BY created_at, id`, customerID, since)
	if err != nil {
		return nil, fmt.Errorf("list day operations: %w", err)
	}
	defer rows.Close()
	var ops []DayOperation
	for rows.Next() {
		var op DayOperation
		if err := rows.Scan(&op.ID, &op.Amount, &op.Currency, &op.ConvertedAmount, &op.TargetCurrency); err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}
	return ops, rows.Err()
}

// SentSince indique si ce modèle a déjà été envoyé au client depuis la date donnée.
func (r *Repository) SentSince(customerID int, template string, since time.Time) (bool, error) {
	var exists bool
	err := r.store.DB().QueryRow(`
		SELECT EXISTS (SELECT 1 FROM customer_notifications
		WHERE customer_id = $1 AND template = $2 AND status = 'SENT' AND created_at >= $3)`,
		customerID, template, since).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check notification history: %w", err)
	}
	return exists, nil
}

func (r *Repository) Save(rec *Record) error {
	err := r.store.DB().QueryRow(`
		INSERT INTO customer_notifications (customer_id, transaction_id, channel, template, recipient, subject, body, status, detail, mode)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, created_at`,
		rec.CustomerID, rec.TransactionID, rec.Channel, rec.Template, rec.Recipient, rec.Subject, rec.Body, rec.Status, rec.Detail, rec.Mode,
	).Scan(&rec.ID, &rec.CreatedAt)
	if err != nil {
		return fmt.Errorf("save notification: %w", err)
	}
	return nil
}

// ListForTransaction : messages liés à une opération, du plus ancien au plus récent.
func (r *Repository) ListForTransaction(transactionID int) ([]Record, error) {
	rows, err := r.store.DB().Query(`
		SELECT id, customer_id, transaction_id, channel, template, recipient, subject, body, status, detail, mode, created_at
		FROM customer_notifications WHERE transaction_id = $1 ORDER BY created_at, id`, transactionID)
	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()
	records := []Record{}
	for rows.Next() {
		var rec Record
		var txID sql.NullInt64
		if err := rows.Scan(&rec.ID, &rec.CustomerID, &txID, &rec.Channel, &rec.Template, &rec.Recipient, &rec.Subject, &rec.Body, &rec.Status, &rec.Detail, &rec.Mode, &rec.CreatedAt); err != nil {
			return nil, err
		}
		if txID.Valid {
			id := int(txID.Int64)
			rec.TransactionID = &id
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}
