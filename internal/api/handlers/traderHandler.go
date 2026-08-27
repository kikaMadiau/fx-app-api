package handlers

import (
	"fmt"
	traderentity "fx-app-api/internal/domain/trader/entity"
	traderauthservice "fx-app-api/internal/domain/trader/traderservices"
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
	ID        int        `json:"id"`
	Name      string     `json:"name"`
	FirstName string     `json:"first_name"`
	LastName  string     `json:"last_name"`
	Email     string     `json:"email"`
	Phone     string     `json:"phone"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at"`
	Role      string     `json:"role"`
	StoreID   int        `json:"store_id"`
	IsActive  bool       `json:"is_active"`
}

type UpsertTraderRequest struct {
	Name         string `json:"name"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	Email        string `json:"email"`
	Phone        string `json:"phone"`
	Status       string `json:"status"`
	Role         string `json:"role"`
	Password     string `json:"password"`
	PasswordHash string `json:"password_hash"`
	StoreID      int    `json:"store_id"`
	IsActive     bool   `json:"is_active"`
}

func traderFromRequest(req UpsertTraderRequest) *traderentity.Trader {
	return &traderentity.Trader{
		Name:         req.Name,
		FirstName:    req.FirstName,
		LastName:     req.LastName,
		Email:        strings.TrimSpace(req.Email),
		Phone:        req.Phone,
		Status:       req.Status,
		Roles:        req.Role,
		PasswordHash: req.PasswordHash,
		StoreId:      req.StoreID,
		IsActive:     req.IsActive,
	}
}

func traderResponse(trader *traderentity.Trader) TraderResponse {
	return TraderResponse{
		ID:        trader.Id,
		Name:      trader.Name,
		FirstName: trader.FirstName,
		LastName:  trader.LastName,
		Email:     trader.Email,
		Phone:     trader.Phone,
		Status:    trader.Status,
		CreatedAt: trader.CreatedAt,
		UpdatedAt: trader.UpdatedAt,
		DeletedAt: trader.DeletedAt,
		Role:      trader.Roles,
		StoreID:   trader.StoreId,
		IsActive:  trader.IsActive,
	}
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
	var req UpsertTraderRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	if strings.TrimSpace(req.Email) == "" {
		return badRequest("email is required", nil)
	}
	if strings.TrimSpace(req.Password) == "" && strings.TrimSpace(req.PasswordHash) == "" {
		return badRequest("password is required", nil)
	}
	if strings.TrimSpace(req.Password) != "" {
		passwordHash, err := traderauthservice.HashPassword(req.Password)
		if err != nil {
			return badRequest("invalid password", err)
		}
		req.PasswordHash = passwordHash
	}

	trader := traderFromRequest(req)
	if err := h.traderStore.CreateTrader(trader); err != nil {
		return fmt.Errorf("failed to create trader: %w", err)
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
		return notFound(err.Error())
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
		return notFound(err.Error())
	}

	var req UpsertTraderRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	if strings.TrimSpace(req.Email) == "" {
		return badRequest("email is required", nil)
	}

	existingTrader.Name = req.Name
	existingTrader.FirstName = req.FirstName
	existingTrader.LastName = req.LastName
	existingTrader.Email = strings.TrimSpace(req.Email)
	existingTrader.Phone = req.Phone
	existingTrader.Status = req.Status
	existingTrader.Roles = req.Role
	existingTrader.StoreId = req.StoreID
	existingTrader.IsActive = req.IsActive

	if err := h.traderStore.UpdateTrader(existingTrader); err != nil {
		return fmt.Errorf("failed to update trader: %w", err)
	}

	return WriteJson(w, http.StatusOK, traderResponse(existingTrader))
}

func (h *Handler) HandleDeleteTrader(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid trader id", err)
	}

	if _, err := h.traderStore.GetTrader(id); err != nil {
		return notFound(err.Error())
	}
	if err := h.traderStore.DeleteTrader(id); err != nil {
		return fmt.Errorf("failed to delete trader: %w", err)
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}
