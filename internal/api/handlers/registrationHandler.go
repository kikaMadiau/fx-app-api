package handlers

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"fx-app-api/internal/domain/notification"
	"fx-app-api/internal/domain/trader/registration"
	traderauthservice "fx-app-api/internal/domain/trader/traderservices"
	"fx-app-api/internal/storage"
)

// SetRegistrationRepository branche « Créer un compte ».
func (h *Handler) SetRegistrationRepository(repository *registration.Repository) {
	h.registrationRepo = repository
}

type RegisterRequest struct {
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
	Email      string `json:"email"`
	Phone      string `json:"phone"`
	BCCLicense string `json:"bcc_license_number"`
	Password   string `json:"password"`
}

type RegisterResponse struct {
	Message   string `json:"message"`
	StoreName string `json:"store_name"`
}

// HandleRegister : POST /auth/register (public, limité comme la connexion). Le compte n'est
// créé que si le numéro d'agrément correspond à un bureau dont l'agrément est valable.
func (h *Handler) HandleRegister(w http.ResponseWriter, r *http.Request) error {
	var req RegisterRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	req.FirstName = strings.TrimSpace(req.FirstName)
	req.LastName = strings.TrimSpace(req.LastName)
	if req.FirstName == "" || req.LastName == "" {
		return badRequest("Le prénom et le nom sont obligatoires.", nil)
	}
	email, err := notification.NormalizeEmail(req.Email)
	if err != nil || email == "" {
		return badRequest("Adresse e-mail invalide.", nil)
	}
	phone := ""
	if strings.TrimSpace(req.Phone) != "" {
		if phone, err = storage.NormalizePhone(req.Phone); err != nil {
			return badRequest("Numéro de téléphone invalide.", nil)
		}
	}
	license, err := storage.NormalizeBCCLicense(req.BCCLicense)
	if err != nil {
		return badRequest("Saisissez le numéro d'agrément BCC de votre bureau.", nil)
	}
	hash, err := traderauthservice.HashPassword(req.Password)
	if err != nil {
		return badRequest("Le mot de passe doit contenir entre 12 et 72 caractères.", nil)
	}
	if h.registrationRepo == nil {
		return &HTTPError{StatusCode: http.StatusServiceUnavailable, Message: "Inscription indisponible."}
	}

	store, err := h.registrationRepo.CheckLicense(license, time.Now())
	var expired registration.ErrLicenseExpired
	switch {
	case errors.Is(err, registration.ErrUnknownLicense):
		return &HTTPError{StatusCode: http.StatusUnprocessableEntity,
			Message: "Numéro d'agrément inconnu. Vérifiez le numéro délivré par la BCC à votre bureau de change."}
	case errors.Is(err, registration.ErrLicenseSuspended):
		return &HTTPError{StatusCode: http.StatusUnprocessableEntity,
			Message: "L'agrément de ce bureau est suspendu ou retiré : l'inscription est impossible."}
	case errors.As(err, &expired):
		return &HTTPError{StatusCode: http.StatusUnprocessableEntity,
			Message: "L'agrément de ce bureau a expiré le " + expired.ExpiredAt.Format("02/01/2006") + " : l'inscription est impossible."}
	case err != nil:
		return err
	}

	id, err := h.registrationRepo.Create(store, registration.Account{
		FirstName: req.FirstName, LastName: req.LastName, Email: email, Phone: phone, PasswordHash: hash,
	})
	if errors.Is(err, registration.ErrEmailTaken) {
		return &HTTPError{StatusCode: http.StatusConflict, Message: "Un compte existe déjà avec cette adresse e-mail."}
	}
	if err != nil {
		return err
	}
	if h.auditLogger != nil {
		_ = h.auditLogger.Record(id, "auth.register", "trader", id, nil,
			map[string]any{"store_id": store.ID, "bcc_license_number": license, "role": "TRADER", "status": "PENDING"})
	}
	return WriteJson(w, http.StatusCreated, RegisterResponse{
		Message:   "Demande envoyée. Un superviseur de " + store.Name + " doit accepter votre compte avant votre première connexion.",
		StoreName: store.Name,
	})
}
