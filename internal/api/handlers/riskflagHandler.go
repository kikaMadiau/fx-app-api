package handlers

import (
	"fmt"
	riskflagentity "fx-app-api/internal/domain/riskflag/entity"
	"fx-app-api/internal/storage"
	"net/http"
	"strings"
)

type CreateRiskFlagRequest struct {
	Flag          string  `json:"flag"`
	Reason        string  `json:"reason"`
	Score         float64 `json:"score"`
	Level         string  `json:"level"`
	TransactionID int     `json:"transaction_id"`
	TraderID      int     `json:"trader_id"`
	CustomerID    int     `json:"customer_id"`
}

func riskFlagFilterFromQuery(r *http.Request) (storage.RiskFlagFilter, error) {
	query := r.URL.Query()
	filter := storage.RiskFlagFilter{}

	var err error
	if filter.TransactionID, err = optionalPositiveInt(query.Get("transaction_id"), "transaction_id"); err != nil {
		return filter, err
	}
	if filter.TraderID, err = optionalPositiveInt(query.Get("trader_id"), "trader_id"); err != nil {
		return filter, err
	}
	if filter.CustomerID, err = optionalPositiveInt(query.Get("customer_id"), "customer_id"); err != nil {
		return filter, err
	}

	return filter, nil
}

func (h *Handler) HandleCreateRiskFlag(w http.ResponseWriter, r *http.Request) error {
	var req CreateRiskFlagRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	if strings.TrimSpace(req.Flag) == "" {
		return badRequest("flag is required", nil)
	}
	if req.TransactionID <= 0 {
		return badRequest("transaction_id is required", nil)
	}

	flag := &riskflagentity.RiskFlag{
		Flag:          strings.TrimSpace(req.Flag),
		Reason:        req.Reason,
		Score:         req.Score,
		Level:         strings.ToUpper(strings.TrimSpace(req.Level)),
		TransactionId: req.TransactionID,
		TraderId:      req.TraderID,
		CustomerId:    req.CustomerID,
	}

	if err := h.riskFlagStore.CreateRiskFlag(flag); err != nil {
		return fmt.Errorf("failed to create risk flag: %w", err)
	}

	return WriteJson(w, http.StatusCreated, flag)
}

func (h *Handler) HandleListRiskFlags(w http.ResponseWriter, r *http.Request) error {
	filter, err := riskFlagFilterFromQuery(r)
	if err != nil {
		return err
	}

	flags, err := h.riskFlagStore.ListRiskFlags(filter)
	if err != nil {
		return fmt.Errorf("failed to list risk flags: %w", err)
	}

	return WriteJson(w, http.StatusOK, flags)
}

func (h *Handler) HandleGetRiskFlag(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid risk flag id", err)
	}

	flag, err := h.riskFlagStore.GetRiskFlag(id)
	if err != nil {
		return notFound(err.Error())
	}

	return WriteJson(w, http.StatusOK, flag)
}

func (h *Handler) HandleDeleteRiskFlag(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid risk flag id", err)
	}

	if err := h.riskFlagStore.DeleteRiskFlag(id); err != nil {
		return notFound(err.Error())
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}
