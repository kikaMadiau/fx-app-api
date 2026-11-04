package handlers

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"fx-app-api/internal/domain/notification"
	"fx-app-api/internal/domain/trader/passwordreset"
	traderauthservice "fx-app-api/internal/domain/trader/traderservices"
)

// SetPasswordResetRepository branche « Mot de passe oublié ».
func (h *Handler) SetPasswordResetRepository(repository *passwordreset.Repository) {
	h.passwordResetRepo = repository
}

type ForgotPasswordRequest struct {
	Email string `json:"email"`
}

type ResetPasswordRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// forgotPasswordReply est toujours la même, que le compte existe ou non : on ne révèle pas
// quelles adresses ont un compte.
type forgotPasswordReply struct {
	Message string `json:"message"`
	// "test" : l'e-mail n'est pas envoyé, il est écrit dans le dossier outbox de l'API.
	Mode string `json:"mode"`
}

// HandleForgotPassword : POST /auth/password/forgot (public, limité comme la connexion).
func (h *Handler) HandleForgotPassword(w http.ResponseWriter, r *http.Request) error {
	var req ForgotPasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" {
		return badRequest("email is required", nil)
	}
	reply := forgotPasswordReply{
		Message: "Si un compte existe pour cette adresse, un lien de réinitialisation vient d'être envoyé.",
		Mode:    "off",
	}
	sender := notification.NewSenderFromEnv()
	if sender != nil {
		reply.Mode = sender.Mode()
	}
	if h.passwordResetRepo == nil || !notification.Active(sender) {
		return WriteJson(w, http.StatusOK, reply)
	}

	trader, err := h.traderStore.GetTraderByEmail(email)
	if err != nil || trader == nil || !trader.IsActive || trader.DeletedAt != nil {
		return WriteJson(w, http.StatusOK, reply)
	}
	token, err := h.passwordResetRepo.Create(trader.Id, time.Now())
	if err != nil {
		return err
	}
	link := notification.AssetsBaseURL() + "/reset?token=" + url.QueryEscape(token)
	name := strings.TrimSpace(trader.FirstName + " " + trader.LastName)
	if name == "" {
		name = trader.Email
	}
	subject, html, err := notification.RenderEmail(notification.TemplatePasswordReset, notification.Data{
		"ClientName":    name,
		"Email":         trader.Email,
		"ResetURL":      link,
		"ExpiryMinutes": fmt.Sprint(int(passwordreset.Validity.Minutes())),
		"LogoURL":       notification.AssetsBaseURL() + "/branding/motuya-mark.png",
		"BCCLogoURL":    notification.AssetsBaseURL() + "/branding/logo-bcc.png",
	})
	if err != nil {
		return err
	}
	if _, err := sender.Send(notification.Message{Channel: notification.ChannelEmail, Template: notification.TemplatePasswordReset,
		To: trader.Email, Subject: subject, Body: html, CreatedAt: time.Now()}); err != nil {
		// La réponse reste la même : l'échec est seulement journalisé.
		log.Printf("password reset e-mail: %v", err)
	}
	if h.auditLogger != nil {
		_ = h.auditLogger.Record(trader.Id, "auth.password_reset.request", "trader", trader.Id, nil, nil)
	}
	return WriteJson(w, http.StatusOK, reply)
}

// HandleResetPassword : POST /auth/password/reset (public) — nouveau mot de passe avec le lien reçu.
func (h *Handler) HandleResetPassword(w http.ResponseWriter, r *http.Request) error {
	var req ResetPasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	if strings.TrimSpace(req.Token) == "" {
		return badRequest("token is required", nil)
	}
	hash, err := traderauthservice.HashPassword(req.Password)
	if err != nil {
		return badRequest(err.Error(), nil)
	}
	if h.passwordResetRepo == nil {
		return badRequest(passwordreset.ErrInvalidToken.Error(), nil)
	}
	traderID, err := h.passwordResetRepo.Consume(strings.TrimSpace(req.Token), hash, time.Now())
	if errors.Is(err, passwordreset.ErrInvalidToken) {
		return badRequest(err.Error(), nil)
	}
	if err != nil {
		return err
	}
	if h.auditLogger != nil {
		_ = h.auditLogger.Record(traderID, "auth.password_reset.complete", "trader", traderID, nil, nil)
	}
	return WriteJson(w, http.StatusOK, map[string]string{"message": "Mot de passe modifié. Vous pouvez vous connecter."})
}
