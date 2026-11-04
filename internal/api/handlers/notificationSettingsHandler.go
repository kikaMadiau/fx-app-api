package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"fx-app-api/internal/domain/notification"
	"fx-app-api/internal/storage"
)

// --- Réglages d'envoi des notifications (administrateur de la plateforme) ---

func (h *Handler) SetNotificationSettings(repository *notification.SettingsRepository) {
	h.notifySettings = repository
}

// NotificationSettingsResponse ne renvoie jamais les secrets : seulement s'ils sont enregistrés.
type NotificationSettingsResponse struct {
	notification.Settings
	TwilioAuthTokenSet bool `json:"twilio_auth_token_set"`
	SMTPPassSet        bool `json:"smtp_pass_set"`
	SMSReady           bool `json:"sms_ready"`
	WhatsAppReady      bool `json:"whatsapp_ready"`
	EmailReady         bool `json:"email_ready"`
}

type UpdateNotificationSettingsRequest struct {
	notification.Settings
	TwilioAuthToken *string `json:"twilio_auth_token"` // absent ou vide : on garde l'actuel
	SMTPPass        *string `json:"smtp_pass"`
}

type NotificationTestRequest struct {
	Channel string `json:"channel"` // sms, whatsapp ou email
	To      string `json:"to"`
}

type NotificationTestResponse struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func settingsResponse(s notification.Settings) NotificationSettingsResponse {
	return NotificationSettingsResponse{
		Settings:           s,
		TwilioAuthTokenSet: s.TwilioAuthToken != "",
		SMTPPassSet:        s.SMTPPass != "",
		SMSReady:           s.SMSReady(),
		WhatsAppReady:      s.WhatsAppReady(),
		EmailReady:         s.EmailReady(),
	}
}

func (h *Handler) requireNotificationSettings() error {
	if h.notifySettings == nil {
		return fmt.Errorf("notification settings repository is not configured")
	}
	return nil
}

func (h *Handler) HandleGetNotificationSettings(w http.ResponseWriter, r *http.Request) error {
	if err := h.requireNotificationSettings(); err != nil {
		return err
	}
	return WriteJson(w, http.StatusOK, settingsResponse(notification.CurrentSettings()))
}

func (h *Handler) HandleUpdateNotificationSettings(w http.ResponseWriter, r *http.Request) error {
	if err := h.requireNotificationSettings(); err != nil {
		return err
	}
	var req UpdateNotificationSettingsRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	s := req.Settings
	s.Mode = strings.ToLower(strings.TrimSpace(s.Mode))
	s.TwilioAccountSID = strings.TrimSpace(s.TwilioAccountSID)
	s.SMTPHost = strings.TrimSpace(s.SMTPHost)
	s.SMTPUser = strings.TrimSpace(s.SMTPUser)
	s.SMTPFrom = strings.TrimSpace(s.SMTPFrom)
	for _, number := range []*string{&s.TwilioFrom, &s.TwilioWhatsAppFrom} {
		*number = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(*number), "whatsapp:"))
		// Un numéro (pas un nom d'expéditeur alphanumérique) est mis au format international.
		if *number != "" && strings.ContainsAny((*number)[:1], "+0123456789") {
			normalized, err := storage.NormalizePhone(*number)
			if err != nil {
				return badRequest("invalid sender phone number", err)
			}
			*number = normalized
		}
	}
	if err := notification.ValidateSettings(s); err != nil {
		return badRequest(err.Error(), nil)
	}
	if s.Mode == notification.ModeLive && !s.SMSEnabled && !s.WhatsAppEnabled && !s.EmailEnabled {
		return badRequest("enable at least one channel for live mode", nil)
	}
	token := trimmedSecret(req.TwilioAuthToken)
	pass := trimmedSecret(req.SMTPPass)

	before := settingsResponse(notification.CurrentSettings())
	if err := h.notifySettings.Save(s, token, pass); err != nil {
		return err
	}
	after := settingsResponse(notification.CurrentSettings())
	_ = h.recordAudit(r, "notification_settings.update", "notification_settings", 0, before, after)
	return WriteJson(w, http.StatusOK, after)
}

// trimmedSecret : nil (on garde l'actuel) si le champ est absent ou vide.
func trimmedSecret(value *string) *string {
	if value == nil {
		return nil
	}
	v := strings.TrimSpace(*value)
	if v == "" {
		return nil
	}
	return &v
}

// HandleTestNotification envoie un vrai message d'essai avec les réglages enregistrés,
// quel que soit le mode, et renvoie l'erreur exacte du fournisseur.
func (h *Handler) HandleTestNotification(w http.ResponseWriter, r *http.Request) error {
	if err := h.requireNotificationSettings(); err != nil {
		return err
	}
	var req NotificationTestRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	to := strings.TrimSpace(req.To)
	s := notification.CurrentSettings()
	const text = "Test MOTUYA Fx. : si vous lisez ce message, l'envoi fonctionne."
	var err error
	detail := ""
	switch strings.ToLower(req.Channel) {
	case "sms", "whatsapp":
		if to, err = storage.NormalizePhone(to); err != nil {
			return badRequest("invalid phone", err)
		}
		detail, err = notification.SendTwilio(s, strings.ToLower(req.Channel), to, text)
	case "email":
		if to, err = notification.NormalizeEmail(to); err != nil || to == "" {
			return badRequest("email must be a valid e-mail address", err)
		}
		html := "<p>" + text + "</p>"
		err = notification.SendEmail(s, to, "Test MOTUYA Fx.", html)
	default:
		return badRequest("channel must be one of: sms, whatsapp, email", nil)
	}
	if err != nil {
		return WriteJson(w, http.StatusOK, NotificationTestResponse{OK: false, Detail: err.Error()})
	}
	return WriteJson(w, http.StatusOK, NotificationTestResponse{OK: true, Detail: detail})
}
