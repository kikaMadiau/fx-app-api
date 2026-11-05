package notification

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"fx-app-api/internal/storage"
)

// Réglages d'envoi choisis par l'administrateur de la plateforme (écran Paramètres) :
// Twilio pour les SMS et WhatsApp, un serveur SMTP pour les e-mails. Ils sont gardés en base
// (table notification_settings) et priment sur NOTIFY_MODE dès qu'un mode y est enregistré.

const (
	ModeTest = "test" // les messages sont écrits dans l'outbox, rien ne part
	ModeLive = "live" // envoi réel par Twilio / SMTP
	ModeOff  = "off"  // aucune notification
)

type Settings struct {
	Mode            string `json:"mode"`
	SMSEnabled      bool   `json:"sms_enabled"`
	WhatsAppEnabled bool   `json:"whatsapp_enabled"`
	EmailEnabled    bool   `json:"email_enabled"`

	TwilioAccountSID   string `json:"twilio_account_sid"`
	TwilioAuthToken    string `json:"-"`
	TwilioFrom         string `json:"twilio_from"`          // numéro SMS Twilio (+1…) ou nom d'expéditeur
	TwilioWhatsAppFrom string `json:"twilio_whatsapp_from"` // numéro WhatsApp validé par Twilio

	SMTPHost string `json:"smtp_host"`
	SMTPPort int    `json:"smtp_port"`
	SMTPUser string `json:"smtp_user"`
	SMTPPass string `json:"-"`
	SMTPFrom string `json:"smtp_from"`
	SMTPSSL  bool   `json:"smtp_ssl"` // TLS direct (port 465) ; sinon STARTTLS (587)
}

// SMSReady / WhatsAppReady / EmailReady : le canal a ce qu'il faut pour partir.
func (s Settings) SMSReady() bool {
	return s.TwilioAccountSID != "" && s.TwilioAuthToken != "" && s.TwilioFrom != ""
}
func (s Settings) WhatsAppReady() bool {
	return s.TwilioAccountSID != "" && s.TwilioAuthToken != "" && s.TwilioWhatsAppFrom != ""
}
func (s Settings) EmailReady() bool { return s.SMTPHost != "" }

var settingKeys = []string{
	"mode", "sms_enabled", "whatsapp_enabled", "email_enabled",
	"twilio_account_sid", "twilio_auth_token", "twilio_from", "twilio_whatsapp_from",
	"smtp_host", "smtp_port", "smtp_user", "smtp_pass", "smtp_from", "smtp_ssl",
}

type SettingsRepository struct{ store *storage.PostgresStore }

const encryptedSecretPrefix = "enc:v1:"

func notificationEncryptionKey() ([]byte, error) {
	encoded := strings.TrimSpace(os.Getenv("NOTIFICATION_SETTINGS_ENCRYPTION_KEY"))
	if encoded == "" {
		return nil, errors.New("NOTIFICATION_SETTINGS_ENCRYPTION_KEY must be configured to store notification secrets")
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, errors.New("NOTIFICATION_SETTINGS_ENCRYPTION_KEY must be a base64-encoded 32-byte key")
	}
	return key, nil
}

func encryptNotificationSecret(value string) (string, error) {
	key, err := notificationEncryptionKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(value), nil)
	return encryptedSecretPrefix + base64.RawStdEncoding.EncodeToString(sealed), nil
}

func decryptNotificationSecret(value string) (string, error) {
	if !strings.HasPrefix(value, encryptedSecretPrefix) {
		return value, nil
	}
	key, err := notificationEncryptionKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	sealed, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(value, encryptedSecretPrefix))
	if err != nil || len(sealed) < aead.NonceSize() {
		return "", errors.New("stored notification secret has invalid ciphertext")
	}
	nonce, ciphertext := sealed[:aead.NonceSize()], sealed[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt notification secret: %w", err)
	}
	return string(plain), nil
}

func NewSettingsRepository(store *storage.PostgresStore) *SettingsRepository {
	return &SettingsRepository{store: store}
}

func (r *SettingsRepository) Init() error {
	_, err := r.store.DB().Exec(`
		CREATE TABLE IF NOT EXISTS notification_settings (
			key VARCHAR(40) PRIMARY KEY,
			value TEXT NOT NULL DEFAULT '',
			updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		return fmt.Errorf("init notification settings: %w", err)
	}
	return r.encryptLegacySecrets()
}

// encryptLegacySecrets upgrades existing plaintext credentials once, before they are loaded.
func (r *SettingsRepository) encryptLegacySecrets() error {
	tx, err := r.store.DB().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT key, value FROM notification_settings WHERE key IN ('twilio_auth_token', 'smtp_pass') FOR UPDATE`)
	if err != nil {
		return fmt.Errorf("read legacy notification secrets: %w", err)
	}
	type entry struct{ key, value string }
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.key, &e.value); err != nil {
			rows.Close()
			return err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, e := range entries {
		if e.value == "" {
			continue
		}
		if strings.HasPrefix(e.value, encryptedSecretPrefix) {
			if _, err := decryptNotificationSecret(e.value); err != nil {
				return fmt.Errorf("validate encrypted notification secret %s: %w", e.key, err)
			}
			continue
		}
		ciphertext, err := encryptNotificationSecret(e.value)
		if err != nil {
			return fmt.Errorf("migrate legacy notification secret %s: %w", e.key, err)
		}
		if _, err := tx.Exec(`UPDATE notification_settings SET value = $2, updated_at = NOW() WHERE key = $1`, e.key, ciphertext); err != nil {
			return fmt.Errorf("encrypt legacy notification secret %s: %w", e.key, err)
		}
	}
	return tx.Commit()
}

// Load lit les réglages ; Mode reste vide tant que l'administrateur n'a rien enregistré.
func (r *SettingsRepository) Load() (Settings, error) {
	rows, err := r.store.DB().Query(`SELECT key, value FROM notification_settings`)
	if err != nil {
		return Settings{}, fmt.Errorf("load notification settings: %w", err)
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return Settings{}, err
		}
		values[k] = v
	}
	if err := rows.Err(); err != nil {
		return Settings{}, err
	}
	for _, key := range []string{"twilio_auth_token", "smtp_pass"} {
		decrypted, err := decryptNotificationSecret(values[key])
		if err != nil {
			return Settings{}, fmt.Errorf("load notification secret %s: %w", key, err)
		}
		values[key] = decrypted
	}
	port, _ := strconv.Atoi(values["smtp_port"])
	return Settings{
		Mode:               values["mode"],
		SMSEnabled:         values["sms_enabled"] == "1",
		WhatsAppEnabled:    values["whatsapp_enabled"] == "1",
		EmailEnabled:       values["email_enabled"] == "1",
		TwilioAccountSID:   values["twilio_account_sid"],
		TwilioAuthToken:    values["twilio_auth_token"],
		TwilioFrom:         values["twilio_from"],
		TwilioWhatsAppFrom: values["twilio_whatsapp_from"],
		SMTPHost:           values["smtp_host"],
		SMTPPort:           port,
		SMTPUser:           values["smtp_user"],
		SMTPPass:           values["smtp_pass"],
		SMTPFrom:           values["smtp_from"],
		SMTPSSL:            values["smtp_ssl"] == "1",
	}, nil
}

// Save enregistre tous les réglages. Les secrets (jeton Twilio, mot de passe SMTP) ne sont
// remplacés que s'ils sont fournis : l'écran ne les renvoie jamais.
func (r *SettingsRepository) Save(s Settings, newToken, newSMTPPass *string) error {
	values := map[string]string{
		"mode":                 s.Mode,
		"sms_enabled":          boolString(s.SMSEnabled),
		"whatsapp_enabled":     boolString(s.WhatsAppEnabled),
		"email_enabled":        boolString(s.EmailEnabled),
		"twilio_account_sid":   s.TwilioAccountSID,
		"twilio_from":          s.TwilioFrom,
		"twilio_whatsapp_from": s.TwilioWhatsAppFrom,
		"smtp_host":            s.SMTPHost,
		"smtp_port":            strconv.Itoa(s.SMTPPort),
		"smtp_user":            s.SMTPUser,
		"smtp_from":            s.SMTPFrom,
		"smtp_ssl":             boolString(s.SMTPSSL),
	}
	if newToken != nil {
		ciphertext, err := encryptNotificationSecret(*newToken)
		if err != nil {
			return fmt.Errorf("encrypt Twilio auth token: %w", err)
		}
		values["twilio_auth_token"] = ciphertext
	}
	if newSMTPPass != nil {
		ciphertext, err := encryptNotificationSecret(*newSMTPPass)
		if err != nil {
			return fmt.Errorf("encrypt SMTP password: %w", err)
		}
		values["smtp_pass"] = ciphertext
	}
	tx, err := r.store.DB().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, k := range settingKeys {
		v, ok := values[k]
		if !ok {
			continue
		}
		if _, err := tx.Exec(`
			INSERT INTO notification_settings (key, value, updated_at) VALUES ($1, $2, NOW())
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`, k, v); err != nil {
			return fmt.Errorf("save notification setting %s: %w", k, err)
		}
	}
	return tx.Commit()
}

func boolString(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// ValidateSettings renvoie une erreur lisible (en anglais, traduite par l'interface).
func ValidateSettings(s Settings) error {
	switch s.Mode {
	case ModeTest, ModeLive, ModeOff:
	default:
		return errors.New("mode must be one of: test, live, off")
	}
	if s.SMTPPort < 0 || s.SMTPPort > 65535 {
		return errors.New("smtp_port must be between 1 and 65535")
	}
	if s.TwilioAccountSID != "" && !strings.HasPrefix(s.TwilioAccountSID, "AC") {
		return errors.New("twilio_account_sid must start with AC")
	}
	return nil
}

// --- Source des réglages utilisée par l'envoi ---

var (
	settingsMu     sync.RWMutex
	settingsSource interface{ Load() (Settings, error) }
)

// UseSettings branche la base : à partir de là, l'envoi suit les réglages de l'administrateur.
func UseSettings(source interface{ Load() (Settings, error) }) {
	settingsMu.Lock()
	settingsSource = source
	settingsMu.Unlock()
}

// CurrentSettings : réglages enregistrés, ou le mode de NOTIFY_MODE si rien n'est enregistré.
func CurrentSettings() Settings {
	settingsMu.RLock()
	source := settingsSource
	settingsMu.RUnlock()
	var s Settings
	if source != nil {
		loaded, err := source.Load()
		if err == nil {
			s = loaded
		} else if !errors.Is(err, sql.ErrNoRows) {
			// Base indisponible : on retombe sur le mode du .env plutôt que d'envoyer à l'aveugle.
			s = Settings{}
		}
	}
	if s.Mode == "" {
		s.Mode = envMode()
	}
	return s
}

func envMode() string {
	switch strings.ToLower(envOrDefault("NOTIFY_MODE", ModeTest)) {
	case "off", "none", "false":
		return ModeOff
	case "live":
		return ModeLive
	default:
		return ModeTest
	}
}
