package handlers

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	customerentity "fx-app-api/internal/domain/customer/entity"
	storeentity "fx-app-api/internal/domain/store/entity"
	"fx-app-api/internal/domain/trader/profile"
	traderauthservice "fx-app-api/internal/domain/trader/traderservices"
	"fx-app-api/internal/storage"

	"github.com/go-chi/chi/v5"
	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/crypto/bcrypt"
)

// --- Profil personnel du cambiste connecté (/auth/me/...) ---

const (
	maxPhotoBytes   = 2 << 20 // 2 Mio
	twoFactorIssuer = "FX Change"
)

var allowedPhotoTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true}

func (h *Handler) SetProfileRepository(repository *profile.Repository) { h.profileRepo = repository }

type ProfileResponse struct {
	Trader         TraderResponse     `json:"trader"`
	ExchangeOffice string             `json:"exchange_office"`
	Store          *storeentity.Store `json:"store,omitempty"` // bureau de change agréé du compte
	Profile        *profile.Profile   `json:"profile"`
}

type UpdateProfileRequest struct {
	FirstName      string `json:"first_name"`
	LastName       string `json:"last_name"`
	Phone          string `json:"phone"`
	ExchangeOffice string `json:"exchange_office"`
	BirthDate      string `json:"birth_date"`
	IDType         string `json:"id_type"`
	IDNumber       string `json:"id_number"`
	// Absents : inchangés (anciens écrans qui ne les envoient pas).
	Gender  *string          `json:"gender"`
	Address *profile.Address `json:"address"`
}

type UpdateLocationRequest struct {
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
	Accuracy  *float64 `json:"accuracy"`
}

type UpdateNotificationsRequest struct {
	Email        *bool `json:"email"`
	SMS          *bool `json:"sms"`
	WeeklyReport *bool `json:"weekly_report"`
}

type TwoFactorCodeRequest struct {
	Code string `json:"code"`
}

type TwoFactorSetupResponse struct {
	Secret     string `json:"secret"`
	OTPAuthURL string `json:"otpauth_url"`
	QRCode     string `json:"qr_code"` // image PNG en data URL
}

type TwoFactorEnabledResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

func (h *Handler) currentTraderID(r *http.Request) (int, error) {
	if h.profileRepo == nil {
		return 0, fmt.Errorf("profile repository is not configured")
	}
	user := traderauthservice.GetUser(r)
	if user == nil {
		return 0, &HTTPError{StatusCode: http.StatusUnauthorized, Message: "unauthenticated"}
	}
	return user.Id, nil
}

func (h *Handler) profileResponse(traderID int) (*ProfileResponse, error) {
	trader, err := h.traderStore.GetTrader(traderID)
	if err != nil {
		return nil, fmt.Errorf("get trader: %w", err)
	}
	p, err := h.profileRepo.Get(traderID)
	if err != nil {
		return nil, err
	}
	resp := &ProfileResponse{Trader: traderResponse(trader), ExchangeOffice: trader.Name, Profile: p}
	if h.storeRepo != nil && trader.StoreId > 0 {
		if store, err := h.storeRepo.Get(trader.StoreId); err == nil {
			resp.Store = store
		}
	}
	return resp, nil
}

func (h *Handler) HandleGetProfile(w http.ResponseWriter, r *http.Request) error {
	id, err := h.currentTraderID(r)
	if err != nil {
		return err
	}
	resp, err := h.profileResponse(id)
	if err != nil {
		return err
	}
	return WriteJson(w, http.StatusOK, resp)
}

func (h *Handler) HandleUpdateProfile(w http.ResponseWriter, r *http.Request) error {
	id, err := h.currentTraderID(r)
	if err != nil {
		return err
	}
	var req UpdateProfileRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}

	firstName, lastName := strings.TrimSpace(req.FirstName), strings.TrimSpace(req.LastName)
	office := strings.TrimSpace(req.ExchangeOffice)
	idNumber := strings.TrimSpace(req.IDNumber)
	switch {
	case firstName == "" || lastName == "":
		return badRequest("first_name and last_name are required", nil)
	case utf8.RuneCountInString(firstName) > 100 || utf8.RuneCountInString(lastName) > 100:
		return badRequest("first_name and last_name must be at most 100 characters", nil)
	case utf8.RuneCountInString(office) > 120:
		return badRequest("exchange_office must be at most 120 characters", nil)
	case utf8.RuneCountInString(idNumber) > 60:
		return badRequest("id_number must be at most 60 characters", nil)
	}

	phone := strings.TrimSpace(req.Phone)
	if phone != "" {
		if phone, err = storage.NormalizePhone(phone); err != nil {
			return badRequest("invalid phone", err)
		}
	}

	idType, err := customerentity.NormalizeIDType(req.IDType)
	if err != nil {
		return badRequest(err.Error(), nil)
	}
	if (idType == "") != (idNumber == "") {
		return badRequest("id_type and id_number must be provided together", nil)
	}

	var birthDate *time.Time
	if raw := strings.TrimSpace(req.BirthDate); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			return badRequest("birth_date must use the YYYY-MM-DD format", err)
		}
		now := time.Now()
		age := now.Year() - parsed.Year()
		if parsed.AddDate(age, 0, 0).After(now) {
			age-- // anniversaire pas encore passé cette année
		}
		if age < 18 || age > 120 {
			return badRequest("birth_date must correspond to an age between 18 and 120", nil)
		}
		birthDate = &parsed
	}

	gender, address, err := personalFields(req.Gender, req.Address)
	if err != nil {
		return err
	}

	trader, err := h.traderStore.GetTrader(id)
	if err != nil {
		return fmt.Errorf("get trader: %w", err)
	}
	before := traderResponse(trader)
	trader.FirstName, trader.LastName, trader.Phone, trader.Name = firstName, lastName, phone, office
	if err := h.traderStore.UpdateTrader(trader); err != nil {
		return fmt.Errorf("update trader: %w", err)
	}
	if err := h.profileRepo.UpdateIdentity(id, birthDate, idType, idNumber); err != nil {
		return fmt.Errorf("update profile identity: %w", err)
	}
	if req.Gender != nil || req.Address != nil {
		current, err := h.profileRepo.Get(id)
		if err != nil {
			return err
		}
		if req.Gender == nil {
			gender = current.Gender
		}
		if req.Address == nil {
			address = current.Address
		}
		if err := h.profileRepo.UpdatePersonal(id, gender, address); err != nil {
			return fmt.Errorf("update profile address: %w", err)
		}
	}
	_ = h.recordAudit(r, "profile.update", "trader", id, before, traderResponse(trader))

	resp, err := h.profileResponse(id)
	if err != nil {
		return err
	}
	return WriteJson(w, http.StatusOK, resp)
}

func (h *Handler) HandleUploadProfilePhoto(w http.ResponseWriter, r *http.Request) error {
	id, err := h.currentTraderID(r)
	if err != nil {
		return err
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotoBytes+64<<10)
	if err := r.ParseMultipartForm(maxPhotoBytes); err != nil {
		return badRequest("photo must be sent as multipart/form-data and weigh at most 2 MB", err)
	}
	file, _, err := r.FormFile("photo")
	if err != nil {
		return badRequest("photo file is required (field \"photo\")", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxPhotoBytes+1))
	if err != nil {
		return badRequest("could not read photo", err)
	}
	if len(data) == 0 || len(data) > maxPhotoBytes {
		return badRequest("photo must weigh at most 2 MB", nil)
	}
	mime := http.DetectContentType(data)
	if !allowedPhotoTypes[mime] {
		return badRequest("photo must be a JPEG, PNG or WebP image", nil)
	}
	if err := h.profileRepo.SetPhoto(id, data, mime); err != nil {
		return fmt.Errorf("save photo: %w", err)
	}
	resp, err := h.profileResponse(id)
	if err != nil {
		return err
	}
	return WriteJson(w, http.StatusOK, resp)
}

func (h *Handler) HandleGetProfilePhoto(w http.ResponseWriter, r *http.Request) error {
	id, err := h.currentTraderID(r)
	if err != nil {
		return err
	}
	data, mime, err := h.profileRepo.Photo(id)
	if errors.Is(err, profile.ErrNoPhoto) {
		return notFound("no profile photo")
	}
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, err = io.Copy(w, bytes.NewReader(data))
	return err
}

func (h *Handler) HandleDeleteProfilePhoto(w http.ResponseWriter, r *http.Request) error {
	id, err := h.currentTraderID(r)
	if err != nil {
		return err
	}
	if err := h.profileRepo.DeletePhoto(id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) HandleUpdateNotifications(w http.ResponseWriter, r *http.Request) error {
	id, err := h.currentTraderID(r)
	if err != nil {
		return err
	}
	var req UpdateNotificationsRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	if req.Email == nil || req.SMS == nil || req.WeeklyReport == nil {
		return badRequest("email, sms and weekly_report are required", nil)
	}
	n := profile.Notifications{Email: *req.Email, SMS: *req.SMS, WeeklyReport: *req.WeeklyReport}
	if err := h.profileRepo.SetNotifications(id, n); err != nil {
		return err
	}
	return WriteJson(w, http.StatusOK, n)
}

func (h *Handler) HandleStartTwoFactorSetup(w http.ResponseWriter, r *http.Request) error {
	id, err := h.currentTraderID(r)
	if err != nil {
		return err
	}
	enabled, err := h.profileRepo.TwoFactorEnabled(id)
	if err != nil {
		return err
	}
	if enabled {
		return conflict("two-factor authentication is already enabled")
	}
	secret, err := h.profileRepo.StartTwoFactorSetup(id)
	if err != nil {
		return err
	}
	account := traderauthservice.GetUser(r).Email
	link := profile.OTPAuthURL(twoFactorIssuer, account, secret)
	png, err := qrcode.Encode(link, qrcode.Medium, 256)
	if err != nil {
		return fmt.Errorf("generate qr code: %w", err)
	}
	return WriteJson(w, http.StatusOK, TwoFactorSetupResponse{
		Secret:     secret,
		OTPAuthURL: link,
		QRCode:     "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
	})
}

func (h *Handler) HandleConfirmTwoFactorSetup(w http.ResponseWriter, r *http.Request) error {
	id, err := h.currentTraderID(r)
	if err != nil {
		return err
	}
	var req TwoFactorCodeRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	codes, ok, err := h.profileRepo.ConfirmTwoFactorSetup(id, req.Code, time.Now())
	if errors.Is(err, profile.ErrTwoFactorNotPending) {
		return conflict("start the two-factor setup first")
	}
	if err != nil {
		return err
	}
	if !ok {
		return badRequest("invalid two-factor code", nil)
	}
	_ = h.recordAudit(r, "profile.2fa_enabled", "trader", id, nil, nil)
	return WriteJson(w, http.StatusOK, TwoFactorEnabledResponse{RecoveryCodes: codes})
}

func (h *Handler) HandleDisableTwoFactor(w http.ResponseWriter, r *http.Request) error {
	id, err := h.currentTraderID(r)
	if err != nil {
		return err
	}
	var req TwoFactorCodeRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	ok, _, err := h.profileRepo.VerifySecondFactor(id, req.Code, time.Now())
	if errors.Is(err, profile.ErrTwoFactorDisabled) {
		return conflict("two-factor authentication is not enabled")
	}
	if err != nil {
		return err
	}
	if !ok {
		return badRequest("invalid two-factor code", nil)
	}
	if err := h.profileRepo.DisableTwoFactor(id); err != nil {
		return err
	}
	_ = h.recordAudit(r, "profile.2fa_disabled", "trader", id, nil, nil)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type UpdatePreferencesRequest struct {
	Currency        string `json:"currency"`
	Timezone        string `json:"timezone"`
	DateFormat      string `json:"date_format"`
	Theme           string `json:"theme"`
	DashboardAlerts *bool  `json:"dashboard_alerts"`
}

type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (h *Handler) HandleUpdatePreferences(w http.ResponseWriter, r *http.Request) error {
	id, err := h.currentTraderID(r)
	if err != nil {
		return err
	}
	var req UpdatePreferencesRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	if req.DashboardAlerts == nil {
		return badRequest("dashboard_alerts is required", nil)
	}
	prefs := profile.Preferences{
		Currency:        strings.ToUpper(strings.TrimSpace(req.Currency)),
		Timezone:        strings.TrimSpace(req.Timezone),
		DateFormat:      strings.ToUpper(strings.TrimSpace(req.DateFormat)),
		Theme:           strings.ToLower(strings.TrimSpace(req.Theme)),
		DashboardAlerts: *req.DashboardAlerts,
	}
	if err := profile.ValidatePreferences(prefs); err != nil {
		return badRequest(err.Error(), nil)
	}
	if err := h.profileRepo.SetPreferences(id, prefs); err != nil {
		return err
	}
	return WriteJson(w, http.StatusOK, prefs)
}

// HandleChangePassword permet au cambiste connecté de changer son mot de passe (ancien mot de passe exigé).
func (h *Handler) HandleChangePassword(w http.ResponseWriter, r *http.Request) error {
	id, err := h.currentTraderID(r)
	if err != nil {
		return err
	}
	var req ChangePasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	trader, err := h.traderStore.GetTrader(id)
	if err != nil {
		return fmt.Errorf("get trader: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(trader.PasswordHash), []byte(req.CurrentPassword)) != nil {
		return badRequest("current password is incorrect", nil)
	}
	if req.NewPassword == req.CurrentPassword {
		return badRequest("new password must be different from the current one", nil)
	}
	hash, err := traderauthservice.HashPassword(req.NewPassword)
	if err != nil {
		return badRequest(err.Error(), nil)
	}
	trader.PasswordHash = hash
	trader.MustChangePassword = false
	if err := h.traderStore.UpdateTrader(trader); err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	_ = h.recordAudit(r, "profile.password_changed", "trader", id, nil, nil)
	// Les autres sessions sont coupées ; celle-ci reçoit un nouveau jeton.
	token, err := h.authService.IssueToken(trader)
	if err != nil {
		return err
	}
	return WriteJson(w, http.StatusOK, map[string]any{"access_token": token, "token_type": "Bearer", "expires_in": 86400})
}

// personalFields contrôle le sexe (M, F ou vide) et la longueur des champs de l'adresse.
func personalFields(rawGender *string, rawAddress *profile.Address) (string, profile.Address, error) {
	gender := ""
	if rawGender != nil {
		gender = strings.ToUpper(strings.TrimSpace(*rawGender))
		if gender != "" && gender != "M" && gender != "F" {
			return "", profile.Address{}, badRequest("gender must be M or F", nil)
		}
	}
	var a profile.Address
	if rawAddress != nil {
		a = profile.Address{
			Commune:   strings.TrimSpace(rawAddress.Commune),
			Quartier:  strings.TrimSpace(rawAddress.Quartier),
			Avenue:    strings.TrimSpace(rawAddress.Avenue),
			Number:    strings.TrimSpace(rawAddress.Number),
			Reference: strings.TrimSpace(rawAddress.Reference),
		}
		limits := []struct {
			value string
			max   int
		}{{a.Commune, 80}, {a.Quartier, 80}, {a.Avenue, 120}, {a.Number, 20}, {a.Reference, 160}}
		for _, l := range limits {
			if utf8.RuneCountInString(l.value) > l.max {
				return "", profile.Address{}, badRequest("address fields are too long", nil)
			}
		}
	}
	return gender, a, nil
}

// HandleUpdateLocation : l'appareil envoie sa position (le partage est alors activé).
func (h *Handler) HandleUpdateLocation(w http.ResponseWriter, r *http.Request) error {
	id, err := h.currentTraderID(r)
	if err != nil {
		return err
	}
	var req UpdateLocationRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	if req.Latitude == nil || req.Longitude == nil ||
		*req.Latitude < -90 || *req.Latitude > 90 || *req.Longitude < -180 || *req.Longitude > 180 {
		return badRequest("latitude and longitude are required and must be valid coordinates", nil)
	}
	if req.Accuracy != nil && (*req.Accuracy < 0 || *req.Accuracy > 1e6) {
		req.Accuracy = nil
	}
	if err := h.profileRepo.SetLocation(id, *req.Latitude, *req.Longitude, req.Accuracy, time.Now()); err != nil {
		return fmt.Errorf("save location: %w", err)
	}
	p, err := h.profileRepo.Get(id)
	if err != nil {
		return err
	}
	return WriteJson(w, http.StatusOK, p.Location)
}

// HandleStopLocation coupe le partage de position et efface la dernière position connue.
func (h *Handler) HandleStopLocation(w http.ResponseWriter, r *http.Request) error {
	id, err := h.currentTraderID(r)
	if err != nil {
		return err
	}
	if err := h.profileRepo.StopLocation(id); err != nil {
		return fmt.Errorf("stop location: %w", err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// HandleListStaff : annuaire du personnel (adresse, sexe, position) pour l'administrateur.
func (h *Handler) HandleListStaff(w http.ResponseWriter, r *http.Request) error {
	if h.profileRepo == nil {
		return fmt.Errorf("profile repository is not configured")
	}
	storeID := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("store_id")); raw != "" {
		if _, err := fmt.Sscan(raw, &storeID); err != nil || storeID < 0 {
			return badRequest("store_id must be a positive number", nil)
		}
	}
	staff, err := h.profileRepo.Staff(storeID)
	if err != nil {
		return err
	}
	return WriteJson(w, http.StatusOK, map[string]any{"items": staff, "server_time": time.Now()})
}

// HandleGetStaffPhoto : photo d'un membre du personnel (annuaire de l'administrateur).
func (h *Handler) HandleGetStaffPhoto(w http.ResponseWriter, r *http.Request) error {
	var id int
	if _, err := fmt.Sscan(chi.URLParam(r, "id"), &id); err != nil || id <= 0 {
		return badRequest("invalid id", nil)
	}
	data, mime, err := h.profileRepo.Photo(id)
	if errors.Is(err, profile.ErrNoPhoto) {
		return notFound("no profile photo")
	}
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, no-store")
	_, err = w.Write(data)
	return err
}
