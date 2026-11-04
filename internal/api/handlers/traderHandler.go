package handlers

import (
	"errors"
	"fmt"
	traderentity "fx-app-api/internal/domain/trader/entity"
	"fx-app-api/internal/domain/trader/permissions"
	traderauthservice "fx-app-api/internal/domain/trader/traderservices"
	"fx-app-api/internal/storage"
	"net/http"
	"strings"
	"time"
)

type TraderLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	// OTPCode : code de l'application d'authentification (ou code de secours) si la 2FA est active.
	OTPCode string `json:"otp_code"`
}

// TwoFactorChallengeResponse indique que le mot de passe est correct mais qu'un code est attendu.
type TwoFactorChallengeResponse struct {
	TwoFactorRequired bool   `json:"two_factor_required"`
	Message           string `json:"message"`
}

type TraderLoginResponse struct {
	AccessToken string         `json:"access_token"`
	TokenType   string         `json:"token_type"`
	ExpiresIn   int            `json:"expires_in"`
	Trader      TraderResponse `json:"trader"`
}

type TraderResponse struct {
	ID              int                 `json:"id"`
	FirstName       string              `json:"first_name"`
	LastName        string              `json:"last_name"`
	Email           string              `json:"email"`
	Phone           string              `json:"phone"`
	Status          string              `json:"status"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
	DeletedAt       *time.Time          `json:"deleted_at"`
	Role            string              `json:"role"`
	StoreID         int                 `json:"store_id"`
	IsActive        bool                `json:"is_active"`
	Permissions     []string            `json:"permissions"`
	Roles           []string            `json:"roles"`
	Store           *traderentity.Store `json:"store,omitempty"`
	IsPlatformAdmin bool                `json:"is_platform_admin"`
}

type UpsertTraderRequest struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Email     string `json:"email"`
	Phone     string `json:"phone"`
	Status    string `json:"status"`
	Role      string `json:"role"`
	StoreID   int    `json:"store_id"`
	IsActive  *bool  `json:"is_active"`
}

type CreateTraderRequest struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Email     string `json:"email"`
	Phone     string `json:"phone"`
	Status    string `json:"status"`
	Role      string `json:"role"`
	Password  string `json:"password"`
	StoreID   int    `json:"store_id"`
}

func traderResponse(trader *traderentity.Trader) TraderResponse {
	perms := permissions.ForRole(traderauthservice.EffectiveRoles(trader))
	// L'interface affiche « Administrateur » pour ADMIN : l'admin plateforme n'a pas de rôle de store.
	role := trader.Roles
	if trader.IsPlatformAdmin && strings.TrimSpace(role) == "" {
		role = string(permissions.Admin)
	}
	return TraderResponse{
		ID:              trader.Id,
		FirstName:       trader.FirstName,
		LastName:        trader.LastName,
		Email:           trader.Email,
		Phone:           trader.Phone,
		Status:          trader.Status,
		CreatedAt:       trader.CreatedAt,
		UpdatedAt:       trader.UpdatedAt,
		DeletedAt:       trader.DeletedAt,
		Role:            role,
		StoreID:         trader.StoreId,
		IsActive:        trader.IsActive,
		Permissions:     perms,
		Roles:           traderauthservice.RoleList(trader),
		Store:           trader.Store,
		IsPlatformAdmin: trader.IsPlatformAdmin,
	}
}

func (h *Handler) HandleGetCurrentTrader(w http.ResponseWriter, r *http.Request) error {
	trader := traderauthservice.GetUser(r)
	if trader == nil {
		return &HTTPError{StatusCode: http.StatusUnauthorized, Message: "unauthenticated"}
	}
	return WriteJson(w, http.StatusOK, traderResponse(trader))
}

func (h *Handler) HandleTraderLogin(w http.ResponseWriter, r *http.Request) error {
	var req TraderLoginRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}

	result, err := h.authService.AuthenticateTrader(req.Email, req.Password)
	if errors.Is(err, traderauthservice.ErrAccountPending) {
		return &HTTPError{StatusCode: http.StatusForbidden, Message: "account pending approval"}
	}
	if err != nil {
		return &HTTPError{StatusCode: http.StatusUnauthorized, Message: err.Error()}
	}

	if h.profileRepo != nil {
		enabled, err := h.profileRepo.TwoFactorEnabled(result.Trader.Id)
		if err != nil {
			return fmt.Errorf("check two-factor: %w", err)
		}
		if enabled {
			// Mot de passe correct : on ne délivre le jeton qu'avec un code valide.
			if strings.TrimSpace(req.OTPCode) == "" {
				return WriteJson(w, http.StatusOK, TwoFactorChallengeResponse{
					TwoFactorRequired: true,
					Message:           "two-factor code required",
				})
			}
			ok, _, err := h.profileRepo.VerifySecondFactor(result.Trader.Id, req.OTPCode, time.Now())
			if err != nil {
				return fmt.Errorf("verify two-factor: %w", err)
			}
			if !ok {
				return &HTTPError{StatusCode: http.StatusUnauthorized, Message: "invalid two-factor code"}
			}
		}
		if err := h.profileRepo.RecordLogin(result.Trader.Id, r.UserAgent()); err != nil {
			return fmt.Errorf("record login: %w", err)
		}
	}

	return WriteJson(w, http.StatusOK, TraderLoginResponse{
		AccessToken: result.Token,
		TokenType:   "Bearer",
		ExpiresIn:   86400,
		Trader:      traderResponse(result.Trader),
	})
}

func (h *Handler) HandleCreateTrader(w http.ResponseWriter, r *http.Request) error {
	var req CreateTraderRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	email, phone, identityErr := traderauthservice.NormalizeTraderIdentity(req.FirstName, req.LastName, req.Email, req.Phone)
	if identityErr != nil {
		return badRequest("invalid trader identity", identityErr)
	}
	if strings.TrimSpace(req.Password) == "" {
		return badRequest("password is required", nil)
	}
	if len([]byte(req.Password)) < 12 || len([]byte(req.Password)) > 72 {
		return badRequest("password must be between 12 and 72 bytes", nil)
	}
	role := strings.TrimSpace(req.Role)
	if role == "" {
		role = string(permissions.Trader)
	}
	if normalized, ok := permissions.NormalizeRole(role); !ok {
		return badRequest("role must be TRADER, COMPLIANCE_OFFICER, SUPERVISOR or ADMIN", nil)
	} else {
		role = string(normalized)
	}
	passwordHash, err := traderauthservice.HashPassword(req.Password)
	if err != nil {
		return badRequest("invalid password", err)
	}

	trader := &traderentity.Trader{FirstName: strings.TrimSpace(req.FirstName), LastName: strings.TrimSpace(req.LastName), Email: email, Phone: phone, Status: req.Status, Roles: role, PasswordHash: passwordHash, StoreId: req.StoreID, IsActive: true}
	if h.auditLogger != nil {
		store, ok := h.traderStore.(transactionalTraderStore)
		if !ok {
			return fmt.Errorf("trader repository does not support transactions")
		}
		tx, err := store.BeginTx()
		if err != nil {
			return fmt.Errorf("begin trader creation: %w", err)
		}
		defer tx.Rollback()
		if err := store.CreateTraderWithTx(tx, trader); err != nil {
			return fmt.Errorf("failed to create trader: %w", err)
		}
		if err := h.recordAuditWithTx(r, tx, "trader.create", "trader", trader.Id, nil, traderResponse(trader)); err != nil {
			return fmt.Errorf("failed to audit trader creation: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit trader creation: %w", err)
		}
	} else {
		if err := h.traderStore.CreateTrader(trader); err != nil {
			return fmt.Errorf("failed to create trader: %w", err)
		}
	}

	return WriteJson(w, http.StatusCreated, traderResponse(trader))
}

func (h *Handler) HandleGetTrader(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid trader id", err)
	}

	trader, err := h.traderStore.GetTrader(id)
	if err != nil {
		if errors.Is(err, storage.ErrTraderNotFound) {
			return notFound("trader not found")
		}
		return fmt.Errorf("failed to get trader: %w", err)
	}

	return WriteJson(w, http.StatusOK, traderResponse(trader))
}

func (h *Handler) HandleUpdateTrader(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid trader id", err)
	}

	existingTrader, err := h.traderStore.GetTrader(id)
	if err != nil {
		if errors.Is(err, storage.ErrTraderNotFound) {
			return notFound("trader not found")
		}
		return fmt.Errorf("failed to get trader: %w", err)
	}
	before := traderResponse(existingTrader)
	oldRole, oldActive := existingTrader.Roles, existingTrader.IsActive

	var req UpsertTraderRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	if strings.TrimSpace(req.Email) == "" {
		return badRequest("email is required", nil)
	}
	role := strings.TrimSpace(req.Role)
	if role == "" {
		role = existingTrader.Roles
	}
	normalizedRole, ok := permissions.NormalizeRole(role)
	if !ok {
		return badRequest("role must be TRADER, COMPLIANCE_OFFICER, SUPERVISOR or ADMIN", nil)
	}
	deactivate := req.IsActive != nil && !*req.IsActive
	if existingTrader.IsActive && existingTrader.Roles == string(permissions.Admin) && (deactivate || normalizedRole != permissions.Admin) {
		count, countErr := h.traderStore.CountActiveAdmins()
		if countErr != nil {
			return fmt.Errorf("failed to count active administrators: %w", countErr)
		}
		if count <= 1 {
			return conflict("the last active administrator cannot be disabled or demoted")
		}
	}

	existingTrader.FirstName = req.FirstName
	existingTrader.LastName = req.LastName
	existingTrader.Email = strings.TrimSpace(req.Email)
	existingTrader.Phone = req.Phone
	existingTrader.Status = req.Status
	existingTrader.Roles = string(normalizedRole)
	existingTrader.StoreId = req.StoreID
	if req.IsActive != nil {
		existingTrader.IsActive = *req.IsActive
	}

	var txErr error
	if h.auditLogger != nil && (oldRole != existingTrader.Roles || oldActive != existingTrader.IsActive) {
		store, ok := h.traderStore.(transactionalTraderStore)
		if !ok {
			return fmt.Errorf("trader repository does not support transactions")
		}
		tx, err := store.BeginTx()
		if err != nil {
			return fmt.Errorf("begin trader update: %w", err)
		}
		defer tx.Rollback()
		txErr = store.UpdateTraderWithTx(tx, existingTrader)
		if txErr == nil {
			txErr = h.recordAuditWithTx(r, tx, "trader.access.update", "trader", id, before, traderResponse(existingTrader))
		}
		if txErr == nil {
			txErr = tx.Commit()
		}
	} else {
		txErr = h.traderStore.UpdateTrader(existingTrader)
	}
	if err := txErr; err != nil {
		if errors.Is(err, storage.ErrLastActiveAdmin) {
			return conflict("the last active administrator cannot be disabled or demoted")
		}
		return fmt.Errorf("failed to update trader: %w", err)
	}

	return WriteJson(w, http.StatusOK, traderResponse(existingTrader))
}

func (h *Handler) HandleDeleteTrader(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid trader id", err)
	}

	trader, err := h.traderStore.GetTrader(id)
	if err != nil {
		if errors.Is(err, storage.ErrTraderNotFound) {
			return notFound("trader not found")
		}
		return fmt.Errorf("failed to get trader: %w", err)
	}
	if trader.IsActive && trader.Roles == string(permissions.Admin) {
		count, countErr := h.traderStore.CountActiveAdmins()
		if countErr != nil {
			return fmt.Errorf("failed to count active administrators: %w", countErr)
		}
		if count <= 1 {
			return conflict("the last active administrator cannot be deleted")
		}
	}
	if h.auditLogger != nil {
		store, ok := h.traderStore.(transactionalTraderStore)
		if !ok {
			return fmt.Errorf("trader repository does not support transactions")
		}
		tx, err := store.BeginTx()
		if err != nil {
			return fmt.Errorf("begin trader deletion: %w", err)
		}
		defer tx.Rollback()
		err = store.DeleteTraderWithTx(tx, id)
		if err == nil {
			err = h.recordAuditWithTx(r, tx, "trader.delete", "trader", id, traderResponse(trader), map[string]bool{"deleted": true})
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			if errors.Is(err, storage.ErrLastActiveAdmin) {
				return conflict("the last active administrator cannot be deleted")
			}
			return fmt.Errorf("failed to delete trader: %w", err)
		}
	} else if err := h.traderStore.DeleteTrader(id); err != nil {
		if errors.Is(err, storage.ErrLastActiveAdmin) {
			return conflict("the last active administrator cannot be deleted")
		}
		return fmt.Errorf("failed to delete trader: %w", err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
