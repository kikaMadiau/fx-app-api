// Package profile gère le profil personnel d'un cambiste : identité, photo,
// préférences de notification, double authentification (TOTP) et historique de connexion.
// Ces données vivent dans la table trader_profiles, à côté de la table traders.
package profile

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"fx-app-api/internal/storage"

	"golang.org/x/crypto/bcrypt"
)

// Profile est la partie publique du profil (jamais le secret TOTP ni les codes de secours).
type Profile struct {
	BirthDate         *string       `json:"birth_date"` // AAAA-MM-JJ
	IDType            string        `json:"id_type"`
	IDNumber          string        `json:"id_number"`
	HasPhoto          bool          `json:"has_photo"`
	PhotoUpdatedAt    *time.Time    `json:"photo_updated_at"`
	Notifications     Notifications `json:"notifications"`
	TwoFactorEnabled  bool          `json:"two_factor_enabled"`
	RecoveryCodesLeft int           `json:"recovery_codes_left"`
	LastLoginAt       *time.Time    `json:"last_login_at"`
	PreviousLoginAt   *time.Time    `json:"previous_login_at"`
	LastLoginAgent    string        `json:"last_login_agent"`
	Preferences       Preferences   `json:"preferences"`
	Gender            string        `json:"gender"` // M, F ou vide
	Address           Address       `json:"address"`
	Location          Location      `json:"location"`
}

// Address : adresse du membre du personnel (commune, quartier, avenue, numéro, repère).
type Address struct {
	Commune   string `json:"commune"`
	Quartier  string `json:"quartier"`
	Avenue    string `json:"avenue"`
	Number    string `json:"number"`
	Reference string `json:"reference"`
}

// Location : dernière position partagée par l'appareil (si le partage est activé).
type Location struct {
	Sharing   bool       `json:"sharing"`
	Latitude  *float64   `json:"latitude"`
	Longitude *float64   `json:"longitude"`
	Accuracy  *float64   `json:"accuracy"` // en mètres
	At        *time.Time `json:"at"`
}

var Genders = []string{"M", "F"}

// Preferences : réglages d'affichage de l'interface, conservés d'un appareil à l'autre.
type Preferences struct {
	Currency        string `json:"currency"`         // devise proposée par défaut dans une opération
	Timezone        string `json:"timezone"`         // fuseau utilisé pour afficher les dates
	DateFormat      string `json:"date_format"`      // DD/MM/YYYY, YYYY-MM-DD ou LONG
	Theme           string `json:"theme"`            // light, dark ou system
	DashboardAlerts bool   `json:"dashboard_alerts"` // carte « Alertes » sur le tableau de bord
}

// Valeurs acceptées (la RDC a deux fuseaux : Kinshasa UTC+1 à l'ouest, Lubumbashi UTC+2 à l'est).
var (
	PreferenceCurrencies  = []string{"USD", "EUR", "GBP"}
	PreferenceTimezones   = []string{"Africa/Kinshasa", "Africa/Lubumbashi"}
	PreferenceDateFormats = []string{"DD/MM/YYYY", "YYYY-MM-DD", "LONG"}
	PreferenceThemes      = []string{"light", "dark", "system"}
)

func DefaultPreferences() Preferences {
	return Preferences{Currency: "USD", Timezone: "Africa/Kinshasa", DateFormat: "DD/MM/YYYY", Theme: "light", DashboardAlerts: true}
}

// ValidatePreferences renvoie une erreur lisible si une valeur n'est pas dans la liste autorisée.
func ValidatePreferences(p Preferences) error {
	checks := []struct {
		field, value string
		allowed      []string
	}{
		{"currency", p.Currency, PreferenceCurrencies},
		{"timezone", p.Timezone, PreferenceTimezones},
		{"date_format", p.DateFormat, PreferenceDateFormats},
		{"theme", p.Theme, PreferenceThemes},
	}
	for _, c := range checks {
		if !contains(c.allowed, c.value) {
			return fmt.Errorf("%s must be one of: %s", c.field, strings.Join(c.allowed, ", "))
		}
	}
	return nil
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

type Notifications struct {
	Email        bool `json:"email"`
	SMS          bool `json:"sms"`
	WeeklyReport bool `json:"weekly_report"`
}

var (
	ErrNoPhoto             = errors.New("no profile photo")
	ErrTwoFactorNotPending = errors.New("two-factor setup was not started")
	ErrTwoFactorDisabled   = errors.New("two-factor authentication is not enabled")
)

const recoveryCodeCount = 8

type Repository struct{ store *storage.PostgresStore }

func NewRepository(store *storage.PostgresStore) *Repository { return &Repository{store: store} }

func (r *Repository) Init() error {
	_, err := r.store.DB().Exec(`
		CREATE TABLE IF NOT EXISTS trader_profiles (
			trader_id INT PRIMARY KEY REFERENCES traders(id),
			birth_date DATE,
			id_type VARCHAR(30) NOT NULL DEFAULT '',
			id_number VARCHAR(60) NOT NULL DEFAULT '',
			photo BYTEA,
			photo_mime VARCHAR(30),
			photo_updated_at TIMESTAMPTZ,
			notify_email BOOLEAN NOT NULL DEFAULT TRUE,
			notify_sms BOOLEAN NOT NULL DEFAULT FALSE,
			weekly_report BOOLEAN NOT NULL DEFAULT TRUE,
			totp_secret TEXT,
			totp_pending_secret TEXT,
			totp_enabled BOOLEAN NOT NULL DEFAULT FALSE,
			totp_last_step BIGINT NOT NULL DEFAULT 0,
			recovery_codes TEXT NOT NULL DEFAULT '[]',
			last_login_at TIMESTAMPTZ,
			previous_login_at TIMESTAMPTZ,
			last_login_agent TEXT NOT NULL DEFAULT '',
			updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`)
	if err != nil {
		return err
	}
	// Colonnes ajoutées après la première version : ADD COLUMN IF NOT EXISTS garde les données en place.
	_, err = r.store.DB().Exec(`
		ALTER TABLE trader_profiles
			ADD COLUMN IF NOT EXISTS pref_currency VARCHAR(3) NOT NULL DEFAULT 'USD',
			ADD COLUMN IF NOT EXISTS pref_timezone VARCHAR(40) NOT NULL DEFAULT 'Africa/Kinshasa',
			ADD COLUMN IF NOT EXISTS pref_date_format VARCHAR(12) NOT NULL DEFAULT 'DD/MM/YYYY',
			ADD COLUMN IF NOT EXISTS pref_theme VARCHAR(10) NOT NULL DEFAULT 'light',
			ADD COLUMN IF NOT EXISTS pref_dashboard_alerts BOOLEAN NOT NULL DEFAULT TRUE,
			ADD COLUMN IF NOT EXISTS gender VARCHAR(1) NOT NULL DEFAULT '',
			ADD COLUMN IF NOT EXISTS addr_commune VARCHAR(80) NOT NULL DEFAULT '',
			ADD COLUMN IF NOT EXISTS addr_quartier VARCHAR(80) NOT NULL DEFAULT '',
			ADD COLUMN IF NOT EXISTS addr_avenue VARCHAR(120) NOT NULL DEFAULT '',
			ADD COLUMN IF NOT EXISTS addr_number VARCHAR(20) NOT NULL DEFAULT '',
			ADD COLUMN IF NOT EXISTS addr_reference VARCHAR(160) NOT NULL DEFAULT '',
			ADD COLUMN IF NOT EXISTS location_sharing BOOLEAN NOT NULL DEFAULT FALSE,
			ADD COLUMN IF NOT EXISTS location_lat DOUBLE PRECISION,
			ADD COLUMN IF NOT EXISTS location_lng DOUBLE PRECISION,
			ADD COLUMN IF NOT EXISTS location_accuracy DOUBLE PRECISION,
			ADD COLUMN IF NOT EXISTS location_at TIMESTAMPTZ;`)
	return err
}

func (r *Repository) SetPreferences(traderID int, p Preferences) error {
	if err := r.ensureRow(r.store.DB(), traderID); err != nil {
		return err
	}
	_, err := r.store.DB().Exec(`
		UPDATE trader_profiles SET pref_currency = $2, pref_timezone = $3, pref_date_format = $4,
		       pref_theme = $5, pref_dashboard_alerts = $6, updated_at = NOW()
		WHERE trader_id = $1`, traderID, p.Currency, p.Timezone, p.DateFormat, p.Theme, p.DashboardAlerts)
	return err
}

func (r *Repository) ensureRow(exec interface {
	Exec(string, ...any) (sql.Result, error)
}, traderID int) error {
	_, err := exec.Exec(`INSERT INTO trader_profiles (trader_id) VALUES ($1) ON CONFLICT (trader_id) DO NOTHING`, traderID)
	return err
}

// Get renvoie le profil, avec les valeurs par défaut s'il n'a jamais été enregistré.
func (r *Repository) Get(traderID int) (*Profile, error) {
	p := &Profile{Notifications: Notifications{Email: true, WeeklyReport: true}, Preferences: DefaultPreferences()}
	var birth sql.NullTime
	var recovery string
	pr := &p.Preferences
	err := r.store.DB().QueryRow(`
		SELECT birth_date, id_type, id_number, photo IS NOT NULL, photo_updated_at,
		       notify_email, notify_sms, weekly_report, totp_enabled, recovery_codes,
		       last_login_at, previous_login_at, last_login_agent,
		       pref_currency, pref_timezone, pref_date_format, pref_theme, pref_dashboard_alerts,
		       gender, addr_commune, addr_quartier, addr_avenue, addr_number, addr_reference,
		       location_sharing, location_lat, location_lng, location_accuracy, location_at
		FROM trader_profiles WHERE trader_id = $1`, traderID).Scan(
		&birth, &p.IDType, &p.IDNumber, &p.HasPhoto, &p.PhotoUpdatedAt,
		&p.Notifications.Email, &p.Notifications.SMS, &p.Notifications.WeeklyReport,
		&p.TwoFactorEnabled, &recovery, &p.LastLoginAt, &p.PreviousLoginAt, &p.LastLoginAgent,
		&pr.Currency, &pr.Timezone, &pr.DateFormat, &pr.Theme, &pr.DashboardAlerts,
		&p.Gender, &p.Address.Commune, &p.Address.Quartier, &p.Address.Avenue, &p.Address.Number, &p.Address.Reference,
		&p.Location.Sharing, &p.Location.Latitude, &p.Location.Longitude, &p.Location.Accuracy, &p.Location.At)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get trader profile: %w", err)
	}
	if birth.Valid {
		formatted := birth.Time.Format("2006-01-02")
		p.BirthDate = &formatted
	}
	var hashes []string
	_ = json.Unmarshal([]byte(recovery), &hashes)
	p.RecoveryCodesLeft = len(hashes)
	return p, nil
}

// UpdateIdentity enregistre la date de naissance et la pièce d'identité (birthDate nil = effacée).
func (r *Repository) UpdateIdentity(traderID int, birthDate *time.Time, idType, idNumber string) error {
	if err := r.ensureRow(r.store.DB(), traderID); err != nil {
		return err
	}
	_, err := r.store.DB().Exec(`
		UPDATE trader_profiles SET birth_date = $2, id_type = $3, id_number = $4, updated_at = NOW()
		WHERE trader_id = $1`, traderID, birthDate, idType, idNumber)
	return err
}

// UpdatePersonal enregistre le sexe et l'adresse.
func (r *Repository) UpdatePersonal(traderID int, gender string, a Address) error {
	if err := r.ensureRow(r.store.DB(), traderID); err != nil {
		return err
	}
	_, err := r.store.DB().Exec(`
		UPDATE trader_profiles SET gender = $2, addr_commune = $3, addr_quartier = $4, addr_avenue = $5,
		       addr_number = $6, addr_reference = $7, updated_at = NOW()
		WHERE trader_id = $1`, traderID, gender, a.Commune, a.Quartier, a.Avenue, a.Number, a.Reference)
	return err
}

// SetLocation active le partage et mémorise la position envoyée par l'appareil.
func (r *Repository) SetLocation(traderID int, lat, lng float64, accuracy *float64, at time.Time) error {
	if err := r.ensureRow(r.store.DB(), traderID); err != nil {
		return err
	}
	_, err := r.store.DB().Exec(`
		UPDATE trader_profiles SET location_sharing = TRUE, location_lat = $2, location_lng = $3,
		       location_accuracy = $4, location_at = $5
		WHERE trader_id = $1`, traderID, lat, lng, accuracy, at)
	return err
}

// StopLocation coupe le partage et efface la dernière position.
func (r *Repository) StopLocation(traderID int) error {
	_, err := r.store.DB().Exec(`
		UPDATE trader_profiles SET location_sharing = FALSE, location_lat = NULL, location_lng = NULL,
		       location_accuracy = NULL, location_at = NULL
		WHERE trader_id = $1`, traderID)
	return err
}

// StaffMember : une ligne de l'annuaire du personnel vu par l'administrateur.
type StaffMember struct {
	ID        int        `json:"id"`
	FirstName string     `json:"first_name"`
	LastName  string     `json:"last_name"`
	Email     string     `json:"email"`
	Phone     string     `json:"phone"`
	Role      string     `json:"role"`
	IsActive  bool       `json:"is_active"`
	StoreID   int        `json:"store_id"`
	StoreName string     `json:"store_name"`
	Gender    string     `json:"gender"`
	HasPhoto  bool       `json:"has_photo"`
	Address   Address    `json:"address"`
	Location  Location   `json:"location"`
	LastLogin *time.Time `json:"last_login_at"`
}

// Staff liste les comptes du personnel (storeID 0 : tous les bureaux), par bureau puis par nom.
func (r *Repository) Staff(storeID int) ([]StaffMember, error) {
	rows, err := r.store.DB().Query(`
		SELECT t.id, COALESCE(t.first_name, ''), COALESCE(t.last_name, ''), t.email, COALESCE(t.phone, ''),
		       CASE WHEN t.is_platform_admin THEN 'ADMIN' ELSE COALESCE(t.roles[1], t.role, '') END, t.is_active, COALESCE(t.store_id, 0), COALESCE(s.name, ''),
		       COALESCE(p.gender, ''), COALESCE(p.photo IS NOT NULL, FALSE),
		       COALESCE(p.addr_commune, ''), COALESCE(p.addr_quartier, ''), COALESCE(p.addr_avenue, ''),
		       COALESCE(p.addr_number, ''), COALESCE(p.addr_reference, ''),
		       COALESCE(p.location_sharing, FALSE), p.location_lat, p.location_lng, p.location_accuracy, p.location_at,
		       p.last_login_at
		FROM traders t
		LEFT JOIN stores s ON s.id = t.store_id
		LEFT JOIN trader_profiles p ON p.trader_id = t.id
		WHERE t.deleted_at IS NULL AND ($1 = 0 OR t.store_id = $1)
		ORDER BY COALESCE(s.name, ''), t.last_name, t.first_name, t.id`, storeID)
	if err != nil {
		return nil, fmt.Errorf("list staff: %w", err)
	}
	defer rows.Close()
	staff := []StaffMember{}
	for rows.Next() {
		var m StaffMember
		a, l := &m.Address, &m.Location
		if err := rows.Scan(&m.ID, &m.FirstName, &m.LastName, &m.Email, &m.Phone, &m.Role, &m.IsActive,
			&m.StoreID, &m.StoreName, &m.Gender, &m.HasPhoto,
			&a.Commune, &a.Quartier, &a.Avenue, &a.Number, &a.Reference,
			&l.Sharing, &l.Latitude, &l.Longitude, &l.Accuracy, &l.At, &m.LastLogin); err != nil {
			return nil, fmt.Errorf("scan staff: %w", err)
		}
		staff = append(staff, m)
	}
	return staff, rows.Err()
}

func (r *Repository) SetPhoto(traderID int, data []byte, mime string) error {
	if err := r.ensureRow(r.store.DB(), traderID); err != nil {
		return err
	}
	_, err := r.store.DB().Exec(`
		UPDATE trader_profiles SET photo = $2, photo_mime = $3, photo_updated_at = NOW(), updated_at = NOW()
		WHERE trader_id = $1`, traderID, data, mime)
	return err
}

func (r *Repository) Photo(traderID int) ([]byte, string, error) {
	var data []byte
	var mime sql.NullString
	err := r.store.DB().QueryRow(`SELECT photo, photo_mime FROM trader_profiles WHERE trader_id = $1`, traderID).Scan(&data, &mime)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && data == nil) {
		return nil, "", ErrNoPhoto
	}
	if err != nil {
		return nil, "", fmt.Errorf("get trader photo: %w", err)
	}
	return data, mime.String, nil
}

func (r *Repository) DeletePhoto(traderID int) error {
	_, err := r.store.DB().Exec(`
		UPDATE trader_profiles SET photo = NULL, photo_mime = NULL, photo_updated_at = NULL, updated_at = NOW()
		WHERE trader_id = $1`, traderID)
	return err
}

func (r *Repository) SetNotifications(traderID int, n Notifications) error {
	if err := r.ensureRow(r.store.DB(), traderID); err != nil {
		return err
	}
	_, err := r.store.DB().Exec(`
		UPDATE trader_profiles SET notify_email = $2, notify_sms = $3, weekly_report = $4, updated_at = NOW()
		WHERE trader_id = $1`, traderID, n.Email, n.SMS, n.WeeklyReport)
	return err
}

// RecordLogin mémorise la connexion réussie ; la précédente devient « dernière connexion ».
func (r *Repository) RecordLogin(traderID int, userAgent string) error {
	if err := r.ensureRow(r.store.DB(), traderID); err != nil {
		return err
	}
	if len(userAgent) > 300 {
		userAgent = userAgent[:300]
	}
	_, err := r.store.DB().Exec(`
		UPDATE trader_profiles SET previous_login_at = last_login_at, last_login_at = NOW(), last_login_agent = $2
		WHERE trader_id = $1`, traderID, userAgent)
	return err
}

// TwoFactorEnabled indique si la connexion de ce cambiste demande un code.
func (r *Repository) TwoFactorEnabled(traderID int) (bool, error) {
	var enabled bool
	err := r.store.DB().QueryRow(`SELECT totp_enabled FROM trader_profiles WHERE trader_id = $1`, traderID).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return enabled, err
}

// StartTwoFactorSetup crée un nouveau secret en attente (la 2FA n'est active qu'après confirmation).
func (r *Repository) StartTwoFactorSetup(traderID int) (string, error) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		return "", err
	}
	if err := r.ensureRow(r.store.DB(), traderID); err != nil {
		return "", err
	}
	_, err = r.store.DB().Exec(`UPDATE trader_profiles SET totp_pending_secret = $2, updated_at = NOW() WHERE trader_id = $1`, traderID, secret)
	return secret, err
}

// ConfirmTwoFactorSetup active la 2FA si le code correspond au secret en attente
// et renvoie les codes de secours (affichés une seule fois, stockés hachés).
func (r *Repository) ConfirmTwoFactorSetup(traderID int, code string, now time.Time) ([]string, bool, error) {
	tx, err := r.store.DB().Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var pending sql.NullString
	err = tx.QueryRow(`SELECT totp_pending_secret FROM trader_profiles WHERE trader_id = $1 FOR UPDATE`, traderID).Scan(&pending)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !pending.Valid) {
		return nil, false, ErrTwoFactorNotPending
	}
	if err != nil {
		return nil, false, err
	}
	step, ok := VerifyTOTP(pending.String, code, now, 0)
	if !ok {
		return nil, false, nil
	}
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(`
		UPDATE trader_profiles SET totp_secret = totp_pending_secret, totp_pending_secret = NULL,
		       totp_enabled = TRUE, totp_last_step = $2, recovery_codes = $3, updated_at = NOW()
		WHERE trader_id = $1`, traderID, step, hashes); err != nil {
		return nil, false, err
	}
	return codes, true, tx.Commit()
}

// VerifySecondFactor contrôle un code de l'application ou un code de secours (consommé s'il sert).
func (r *Repository) VerifySecondFactor(traderID int, code string, now time.Time) (ok bool, usedRecovery bool, err error) {
	tx, err := r.store.DB().Begin()
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback()
	var secret sql.NullString
	var enabled bool
	var lastStep int64
	var recovery string
	err = tx.QueryRow(`SELECT totp_secret, totp_enabled, totp_last_step, recovery_codes FROM trader_profiles WHERE trader_id = $1 FOR UPDATE`, traderID).
		Scan(&secret, &enabled, &lastStep, &recovery)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (!enabled || !secret.Valid)) {
		return false, false, ErrTwoFactorDisabled
	}
	if err != nil {
		return false, false, err
	}
	if step, valid := VerifyTOTP(secret.String, code, now, lastStep); valid {
		if _, err := tx.Exec(`UPDATE trader_profiles SET totp_last_step = $2 WHERE trader_id = $1`, traderID, step); err != nil {
			return false, false, err
		}
		return true, false, tx.Commit()
	}
	var hashes []string
	_ = json.Unmarshal([]byte(recovery), &hashes)
	normalized := normalizeRecoveryCode(code)
	for i, hash := range hashes {
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(normalized)) == nil {
			remaining, _ := json.Marshal(append(hashes[:i:i], hashes[i+1:]...))
			if _, err := tx.Exec(`UPDATE trader_profiles SET recovery_codes = $2 WHERE trader_id = $1`, traderID, string(remaining)); err != nil {
				return false, false, err
			}
			return true, true, tx.Commit()
		}
	}
	return false, false, nil
}

func (r *Repository) DisableTwoFactor(traderID int) error {
	_, err := r.store.DB().Exec(`
		UPDATE trader_profiles SET totp_enabled = FALSE, totp_secret = NULL, totp_pending_secret = NULL,
		       totp_last_step = 0, recovery_codes = '[]', updated_at = NOW()
		WHERE trader_id = $1`, traderID)
	return err
}

// Codes de secours lisibles : 10 caractères sans lettres ambiguës, affichés XXXXX-XXXXX.
const recoveryAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func newRecoveryCodes() ([]string, string, error) {
	codes := make([]string, recoveryCodeCount)
	hashes := make([]string, recoveryCodeCount)
	for i := range codes {
		raw := make([]byte, 10)
		if _, err := rand.Read(raw); err != nil {
			return nil, "", err
		}
		var b strings.Builder
		for _, v := range raw {
			b.WriteByte(recoveryAlphabet[int(v)%len(recoveryAlphabet)])
		}
		plain := b.String()
		hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
		if err != nil {
			return nil, "", err
		}
		codes[i] = plain[:5] + "-" + plain[5:]
		hashes[i] = string(hash)
	}
	encoded, _ := json.Marshal(hashes)
	return codes, string(encoded), nil
}

func normalizeRecoveryCode(code string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
}
