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
}

type TraderLoginResponse struct {
	AccessToken string         `json:"access_token"`
	TokenType   string         `json:"token_type"`
	ExpiresIn   int            `json:"expires_in"`
	Trader      TraderResponse `json:"trader"`
}

type TraderResponse struct {
	ID          int        `json:"id"`
	Name        string     `json:"name"`
	FirstName   string     `json:"first_name"`
	LastName    string     `json:"last_name"`
	Email       string     `json:"email"`
	Phone       string     `json:"phone"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DeletedAt   *time.Time `json:"deleted_at"`
	Role        string     `json:"role"`
	StoreID     int        `json:"store_id"`
	IsActive    bool       `json:"is_active"`
	Permissions []string   `json:"permissions"`
}

type UpsertTraderRequest struct {
	Name      string `json:"name"`
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
	Name      string `json:"name"`
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
	return TraderResponse{
		ID:          trader.Id,
		Name:        trader.Name,
		FirstName:   trader.FirstName,
		LastName:    trader.LastName,
		Email:       trader.Email,
		Phone:       trader.Phone,
		Status:      trader.Status,
		CreatedAt:   trader.CreatedAt,
		UpdatedAt:   trader.UpdatedAt,
		DeletedAt:   trader.DeletedAt,
		Role:        trader.Roles,
		StoreID:     trader.StoreId,
		IsActive:    trader.IsActive,
		Permissions: permissions.ForRole(trader.Roles),
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
	if err != nil {
		return &HTTPError{StatusCode: http.StatusUnauthorized, Message: err.Error()}
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
	if strings.TrimSpace(req.Email) == "" {
		return badRequest("email is required", nil)
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

	trader := &traderentity.Trader{Name: req.Name, FirstName: req.FirstName, LastName: req.LastName, Email: strings.TrimSpace(req.Email), Phone: req.Phone, Status: req.Status, Roles: role, PasswordHash: passwordHash, StoreId: req.StoreID, IsActive: true}
	if err := h.traderStore.CreateTrader(trader); err != nil {
		return fmt.Errorf("failed to create trader: %w", err)
	}
	if err := h.recordAudit(r, "trader.create", "trader", trader.Id, nil, traderResponse(trader)); err != nil {
		return fmt.Errorf("failed to audit trader creation: %w", err)
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

	existingTrader.Name = req.Name
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

	if err := h.traderStore.UpdateTrader(existingTrader); err != nil {
		if errors.Is(err, storage.ErrLastActiveAdmin) {
			return conflict("the last active administrator cannot be disabled or demoted")
		}
		return fmt.Errorf("failed to update trader: %w", err)
	}
	if oldRole != existingTrader.Roles || oldActive != existingTrader.IsActive {
		if err := h.recordAudit(r, "trader.access.update", "trader", id, before, traderResponse(existingTrader)); err != nil {
			return fmt.Errorf("failed to audit trader access update: %w", err)
		}
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
	if err := h.traderStore.DeleteTrader(id); err != nil {
		if errors.Is(err, storage.ErrLastActiveAdmin) {
			return conflict("the last active administrator cannot be deleted")
		}
		return fmt.Errorf("failed to delete trader: %w", err)
	}
	if err := h.recordAudit(r, "trader.delete", "trader", id, traderResponse(trader), map[string]bool{"deleted": true}); err != nil {
		return fmt.Errorf("failed to audit trader deletion: %w", err)
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}
