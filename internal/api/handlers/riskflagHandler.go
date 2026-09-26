package handlers

import (
	"fmt"
	riskflagentity "fx-app-api/internal/domain/riskflag/entity"
	"fx-app-api/internal/storage"
	traderservices "fx-app-api/internal/domain/trader/traderservices"
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

type TransitionRiskFlagRequest struct {
	Status         string  `json:"status"`
	AssignedTo     *int    `json:"assigned_to,omitempty"`
	ResolutionNote string  `json:"resolution_note,omitempty"`
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
	if filter.Status, err = optionalString(query.Get("status"), "status"); err != nil {
		return filter, err
	}
	if filter.AssignedTo, err = optionalPositiveInt(query.Get("assigned_to"), "assigned_to"); err != nil {
		return filter, err
	}

	return filter, nil
}

func optionalString(rawValue, fieldName string) (string, error) {
	if strings.TrimSpace(rawValue) == "" {
		return "", nil
	}
	return rawValue, nil
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

func (h *Handler) HandleTransitionRiskFlagStatus(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid risk flag id", err)
	}

	var req TransitionRiskFlagRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}

	if !riskflagentity.IsValidRiskFlagStatus(req.Status) {
		return badRequest("invalid status: must be OPEN, IN_REVIEW, RESOLVED or FALSE_POSITIVE", nil)
	}

	// Récupérer le trader authentifié
	user := traderservices.GetUser(r)
	if user == nil {
		return &HTTPError{StatusCode: http.StatusUnauthorized, Message: "unauthenticated"}
	}

	// Récupérer le trader_id de l'utilisateur
	traderID := user.Id

	// Valider la transition
	note := req.ResolutionNote
	if req.Status == string(riskflagentity.RiskFlagStatusResolved) || req.Status == string(riskflagentity.RiskFlagStatusFalsePositive) {
		if strings.TrimSpace(note) == "" {
			return badRequest("resolution_note is required for RESOLVED or FALSE_POSITIVE status", nil)
		}
	}

	// Effectuer la transition
	updatedFlag, err := h.riskService.Investigation.TransitionRiskFlag(id, req.Status, traderID, note, req.AssignedTo)
	if err != nil {
		if strings.Contains(err.Error(), "cannot transition") {
			return &HTTPError{StatusCode: http.StatusConflict, Message: err.Error()}
		}
		return &HTTPError{StatusCode: http.StatusBadRequest, Message: err.Error()}
	}

	return WriteJson(w, http.StatusOK, updatedFlag)
}

func (h *Handler) HandleGetRiskFlagHistory(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid risk flag id", err)
	}

	history, err := h.riskService.Investigation.GetStatusHistory(id)
	if err != nil {
		return notFound("failed to get status history")
	}

	return WriteJson(w, http.StatusOK, history)
}
