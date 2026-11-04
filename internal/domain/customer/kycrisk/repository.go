package kycrisk

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"fx-app-api/internal/storage"
)

// Signals : informations calculées en base, utilisées par les règles de risque KYC.
type Signals struct {
	HasIDFront bool
	HasIDBack  bool
	// Autres clients actifs qui ont le même type et le même numéro de pièce.
	DuplicateIDCount int
	// Opération précédente du client (hors opération en cours), nil s'il n'en a pas.
	PreviousTransactionAt *time.Time
}

// Override est une dérogation accordée par la conformité : elle lève une seule fois les
// blocages KYC d'un client, dans un délai limité.
type Override struct {
	ID            int       `json:"id"`
	CustomerID    int       `json:"customer_id"`
	Reason        string    `json:"reason"`
	GrantedBy     int       `json:"granted_by"`
	GrantedByName string    `json:"granted_by_name"`
	GrantedAt     time.Time `json:"granted_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// DayTotalUSD : opérations non annulées du client depuis le début de la journée (fuseau de
// l'application), en équivalent USD, sans l'opération excludeTxID.
func (r *Repository) DayTotalUSD(tx *sql.Tx, customerID, excludeTxID int, now time.Time) (float64, error) {
	local := now.In(Location())
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	rows, err := r.on(tx).Query(`
		SELECT amount, currency, COALESCE(converted_amount, 0), COALESCE(target_currency, '')
		FROM transactions
		WHERE customer_id = $1 AND id <> $2 AND deleted_at IS NULL
		  AND status IS DISTINCT FROM 'CANCELLED' AND created_at >= $3`, customerID, excludeTxID, dayStart)
	if err != nil {
		return 0, fmt.Errorf("day total: %w", err)
	}
	defer rows.Close()
	var total float64
	for rows.Next() {
		var amount, converted float64
		var currency, target string
		if err := rows.Scan(&amount, &currency, &converted, &target); err != nil {
			return 0, err
		}
		total += USDEquivalent(amount, currency, converted, target)
	}
	return total, rows.Err()
}

type queryer interface {
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
	Exec(query string, args ...any) (sql.Result, error)
}

type Repository struct{ store *storage.PostgresStore }

func NewRepository(store *storage.PostgresStore) *Repository { return &Repository{store: store} }

func (r *Repository) on(tx *sql.Tx) queryer {
	if tx != nil {
		return tx
	}
	return r.store.DB()
}

func (r *Repository) Init() error {
	_, err := r.store.DB().Exec(`
		CREATE TABLE IF NOT EXISTS kyc_overrides (
			id SERIAL PRIMARY KEY,
			customer_id INT NOT NULL REFERENCES customers(id),
			reason TEXT NOT NULL,
			granted_by INT REFERENCES traders(id),
			granted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			expires_at TIMESTAMPTZ NOT NULL,
			used_at TIMESTAMPTZ,
			transaction_id INT REFERENCES transactions(id)
		);
		CREATE INDEX IF NOT EXISTS kyc_overrides_customer_idx ON kyc_overrides (customer_id, expires_at);`)
	if err != nil {
		return fmt.Errorf("create kyc_overrides: %w", err)
	}
	return nil
}

// CustomerSignals lit les signaux KYC d'un client ; excludeTransactionID est l'opération en
// cours d'analyse (0 si aucune).
func (r *Repository) CustomerSignals(tx *sql.Tx, customerID, excludeTransactionID int) (*Signals, error) {
	s := &Signals{}
	var previous sql.NullTime
	err := r.on(tx).QueryRow(`
		SELECT
			EXISTS (SELECT 1 FROM customer_id_documents d WHERE d.customer_id = c.id AND d.side = 'front'),
			EXISTS (SELECT 1 FROM customer_id_documents d WHERE d.customer_id = c.id AND d.side = 'back'),
			(SELECT COUNT(*) FROM customers o
			  WHERE o.id <> c.id AND o.deleted_at IS NULL
			    AND COALESCE(c.id_type, '') <> '' AND o.id_type = c.id_type
			    AND UPPER(REGEXP_REPLACE(o.id_number, '[\s-]', '', 'g')) = UPPER(REGEXP_REPLACE(c.id_number, '[\s-]', '', 'g'))),
			(SELECT MAX(t.created_at) FROM transactions t
			  WHERE t.customer_id = c.id AND t.id <> $2 AND t.deleted_at IS NULL)
		FROM customers c
		WHERE c.id = $1`, customerID, excludeTransactionID).Scan(&s.HasIDFront, &s.HasIDBack, &s.DuplicateIDCount, &previous)
	if err != nil {
		return nil, fmt.Errorf("read KYC signals for customer %d: %w", customerID, err)
	}
	if previous.Valid {
		s.PreviousTransactionAt = &previous.Time
	}
	return s, nil
}

// OpenFlagNames renvoie les alertes encore ouvertes du client (par code de règle) : une même
// alerte client n'est pas recréée à chaque opération tant qu'elle n'est pas traitée.
func (r *Repository) OpenFlagNames(tx *sql.Tx, customerID int) (map[string]bool, error) {
	rows, err := r.on(tx).Query(`
		SELECT DISTINCT flag FROM risk_flags
		WHERE customer_id = $1 AND deleted_at IS NULL AND status IN ('OPEN', 'IN_REVIEW')`, customerID)
	if err != nil {
		return nil, fmt.Errorf("read open risk flags for customer %d: %w", customerID, err)
	}
	defer rows.Close()
	names := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names[name] = true
	}
	return names, rows.Err()
}

const overrideColumns = `
	o.id, o.customer_id, o.reason, COALESCE(o.granted_by, 0),
	COALESCE(NULLIF(TRIM(COALESCE(t.first_name, '') || ' ' || COALESCE(t.last_name, '')), ''), t.email, ''),
	o.granted_at, o.expires_at`

// ActiveOverride renvoie la dérogation encore utilisable du client, ou nil.
func (r *Repository) ActiveOverride(tx *sql.Tx, customerID int) (*Override, error) {
	o := &Override{}
	err := r.on(tx).QueryRow(`
		SELECT`+overrideColumns+`
		FROM kyc_overrides o
		LEFT JOIN traders t ON t.id = o.granted_by
		WHERE o.customer_id = $1 AND o.used_at IS NULL AND o.expires_at > NOW()
		ORDER BY o.granted_at DESC
		LIMIT 1`, customerID).Scan(&o.ID, &o.CustomerID, &o.Reason, &o.GrantedBy, &o.GrantedByName, &o.GrantedAt, &o.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read KYC override for customer %d: %w", customerID, err)
	}
	return o, nil
}

// GrantOverride enregistre une dérogation ; une dérogation encore ouverte pour ce client est remplacée.
func (r *Repository) GrantOverride(tx *sql.Tx, customerID, grantedBy int, reason string) (*Override, error) {
	q := r.on(tx)
	if _, err := q.Exec(`
		UPDATE kyc_overrides SET expires_at = NOW()
		WHERE customer_id = $1 AND used_at IS NULL AND expires_at > NOW()`, customerID); err != nil {
		return nil, fmt.Errorf("close previous KYC override: %w", err)
	}
	var id int
	if err := q.QueryRow(`
		INSERT INTO kyc_overrides (customer_id, reason, granted_by, expires_at)
		VALUES ($1, $2, NULLIF($3, 0), NOW() + $4 * INTERVAL '1 second')
		RETURNING id`, customerID, strings.TrimSpace(reason), grantedBy, int(OverrideValidity.Seconds())).Scan(&id); err != nil {
		return nil, fmt.Errorf("create KYC override: %w", err)
	}
	return r.ActiveOverride(tx, customerID)
}

// ConsumeOverride marque la dérogation comme utilisée par l'opération.
func (r *Repository) ConsumeOverride(tx *sql.Tx, overrideID, transactionID int) error {
	result, err := r.on(tx).Exec(`
		UPDATE kyc_overrides SET used_at = NOW(), transaction_id = $2
		WHERE id = $1 AND used_at IS NULL`, overrideID, transactionID)
	if err != nil {
		return fmt.Errorf("use KYC override: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return errors.New("KYC override already used")
	}
	return nil
}
