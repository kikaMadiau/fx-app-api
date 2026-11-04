package notification

import (
	"database/sql"
	"errors"
	"fmt"
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
	return nil
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
		values["twilio_auth_token"] = *newToken
	}
	if newSMTPPass != nil {
		values["smtp_pass"] = *newSMTPPass
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
