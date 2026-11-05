// Package registration gère « Créer un compte » : un cambiste s'inscrit avec le numéro
// d'agrément BCC de son bureau. Le compte n'est créé que si l'agrément correspond à un store
// actif dont l'agrément n'a pas expiré ; il rejoint alors ce store avec le rôle TRADER, mais reste
// en attente (PENDING, inactif) tant qu'un superviseur du bureau ne l'a pas accepté : les numéros
// d'agrément sont publics.
package registration

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"fx-app-api/internal/storage"
)

var (
	ErrUnknownLicense   = errors.New("unknown BCC license")
	ErrLicenseSuspended = errors.New("BCC license suspended or withdrawn")
	ErrEmailTaken       = errors.New("email already used")
)

// ErrLicenseExpired porte la date d'expiration de l'agrément.
type ErrLicenseExpired struct{ ExpiredAt time.Time }

func (e ErrLicenseExpired) Error() string { return "BCC license expired" }

// Store : bureau de change correspondant à un agrément.
type Store struct {
	ID   int
	Name string
}

// Account : données saisies par le cambiste (mot de passe déjà haché).
type Account struct {
	FirstName, LastName, Email, Phone, PasswordHash string
}

type Repository struct {
	store *storage.PostgresStore
}

func NewRepository(store *storage.PostgresStore) *Repository { return &Repository{store: store} }

// CheckLicense trouve le store qui porte cet agrément et vérifie qu'il est valable.
// L'agrément provisoire de migration (MIGRATION-PENDING) n'ouvre pas d'inscription.
func (r *Repository) CheckLicense(license string, now time.Time) (*Store, error) {
	var s Store
	var status string
	var expires sql.NullTime
	err := r.store.DB().QueryRow(`
		SELECT id, name, status, license_expires_at FROM stores
		WHERE UPPER(bcc_license_number) = UPPER($1) AND deleted_at IS NULL AND bcc_license_number <> 'MIGRATION-PENDING'`,
		license).Scan(&s.ID, &s.Name, &status, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnknownLicense
	}
	if err != nil {
		return nil, fmt.Errorf("check BCC license: %w", err)
	}
	if !strings.EqualFold(status, "ACTIVE") {
		return nil, ErrLicenseSuspended
	}
	if expires.Valid && !expires.Time.After(now) {
		return nil, ErrLicenseExpired{ExpiredAt: expires.Time}
	}
	return &s, nil
}

// Create enregistre le compte dans le store (rôle TRADER, en attente) ; refuse une adresse déjà prise.
func (r *Repository) Create(store *Store, acc Account) (int, error) {
	tx, err := r.store.DB().Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM traders WHERE LOWER(email) = LOWER($1) AND deleted_at IS NULL)`, acc.Email).Scan(&exists); err != nil {
		return 0, fmt.Errorf("check email: %w", err)
	}
	if exists {
		return 0, ErrEmailTaken
	}
	var id int
	err = tx.QueryRow(`
		INSERT INTO traders (first_name, last_name, email, phone, status, role, roles, password_hash,
		                     store_id, is_active, must_change_password, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'PENDING', 'TRADER', ARRAY['TRADER']::TEXT[], $5, $6, FALSE, FALSE,
		        CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id`,
		acc.FirstName, acc.LastName, acc.Email, acc.Phone, acc.PasswordHash, store.ID).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create trader: %w", err)
	}
	return id, tx.Commit()
}
