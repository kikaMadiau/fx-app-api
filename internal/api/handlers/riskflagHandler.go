package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	riskflagentity "fx-app-api/internal/domain/riskflag/entity"
	"fx-app-api/internal/domain/trader/permissions"
	traderservices "fx-app-api/internal/domain/trader/traderservices"
	"fx-app-api/internal/storage"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

type CreateRiskFlagRequest struct {
	Flag          string  `json:"flag"`
	Reason        string  `json:"reason"`
	Score         float64 `json:"score"`
	Level         string  `json:"level"`
	TransactionID *int    `json:"transaction_id"`
	TraderID      int     `json:"trader_id"`
	CustomerID    int     `json:"customer_id"`
}

type TransitionRiskFlagRequest struct {
	Status         string `json:"status"`
	AssignedTo     *int   `json:"assigned_to,omitempty"`
	ResolutionNote string `json:"resolution_note,omitempty"`
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
	if filter.Level, err = optionalString(query.Get("level"), "level"); err != nil {
		return filter, err
	}
	if filter.Status != "" && !riskflagentity.IsValidRiskFlagStatus(filter.Status) {
		return filter, badRequest("invalid status", nil)
	}
	if filter.Level != "" && !contains([]string{"LOW", "MEDIUM", "HIGH", "CRITICAL"}, strings.ToUpper(filter.Level)) {
		return filter, badRequest("invalid level", nil)
	}
	filter.Level = strings.ToUpper(filter.Level)
	filter.Query = strings.TrimSpace(query.Get("q"))
	if raw := query.Get("limit"); raw != "" {
		n, parseErr := strconv.Atoi(raw)
		if parseErr != nil || n <= 0 {
			return filter, badRequest("limit must be a positive integer", parseErr)
		}
		filter.Limit = n
	}
	if raw := query.Get("offset"); raw != "" {
		n, parseErr := strconv.Atoi(raw)
		if parseErr != nil || n < 0 {
			return filter, badRequest("offset must be a non-negative integer", parseErr)
		}
		filter.Offset = n
	}
	if filter.AssignedTo, err = optionalPositiveInt(query.Get("assigned_to"), "assigned_to"); err != nil {
		return filter, err
	}

	return filter, nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
func boundedLimit(value, fallback, maximum int) int {
	if value <= 0 {
		return fallback
	}
	if value > maximum {
		return maximum
	}
	return value
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
	level := strings.ToUpper(strings.TrimSpace(req.Level))
	if !contains([]string{"LOW", "MEDIUM", "HIGH", "CRITICAL"}, level) {
		return badRequest("level must be LOW, MEDIUM, HIGH or CRITICAL", nil)
	}
	if req.CustomerID <= 0 {
		return badRequest("customer_id is required", nil)
	}
	if req.TraderID < 0 {
		return badRequest("trader_id must be a positive integer", nil)
	}
	if utf8.RuneCountInString(strings.TrimSpace(req.Flag)) > 255 {
		return badRequest("flag must be at most 255 characters", nil)
	}
	user := traderservices.GetUser(r)
	if user == nil {
		return &HTTPError{StatusCode: http.StatusUnauthorized, Message: "unauthenticated"}
	}
	if req.TraderID > 0 && req.TraderID != user.Id {
		return &HTTPError{StatusCode: http.StatusForbidden, Message: "trader_id must match the authenticated trader"}
	}
	if err := h.requireInStore(r, "customers", req.CustomerID, "customer not found"); err != nil {
		return err
	}
	if _, err := h.customerStore.GetCustomer(req.CustomerID); err != nil {
		if errors.Is(err, storage.ErrCustomerIDNotFound) {
			return notFound("customer not found")
		}
		return fmt.Errorf("failed to check customer: %w", err)
	}
	if req.TransactionID != nil {
		tx, err := h.transactionStore.GetTransaction(*req.TransactionID)
		if err != nil {
			if errors.Is(err, storage.ErrTransactionNotFound) {
				return badRequest("transaction_id does not belong to customer", err)
			}
			return fmt.Errorf("failed to check transaction: %w", err)
		}
		if tx.CustomerID != req.CustomerID {
			return badRequest("transaction_id does not belong to customer", nil)
		}
	}

	flag := &riskflagentity.RiskFlag{
		Flag:          strings.TrimSpace(req.Flag),
		Reason:        req.Reason,
		Score:         req.Score,
		Level:         level,
		TransactionId: req.TransactionID,
		TraderId:      user.Id,
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

	// Comme pour les transactions, un trader ne voit que les alertes de ses propres opérations.
	if user := traderservices.GetUser(r); user != nil && strings.EqualFold(user.Roles, string(permissions.Trader)) {
		filter.TraderID = user.Id
	}
	filter.StoreID = traderservices.RequestStoreScope(r)
	filter.Limit = boundedLimit(filter.Limit, 20, 100)
	flags, total, err := h.riskFlagStore.ListRiskFlagsWithDetails(filter)
	if err != nil {
		return fmt.Errorf("failed to list risk flags: %w", err)
	}
	if flags == nil {
		flags = []*storage.RiskFlagWithDetails{}
	}
	return WriteJson(w, http.StatusOK, struct {
		Items  any `json:"items"`
		Total  int `json:"total"`
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
	}{flags, total, filter.Limit, filter.Offset})
}

func (h *Handler) HandleGetRiskFlag(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid risk flag id", err)
	}

	scoped, ok := h.riskFlagStore.(storage.ScopedRiskFlagStorage)
	if !ok {
		return fmt.Errorf("risk flag repository does not support store-scoped reads")
	}
	storeID := traderservices.RequestStoreScope(r)
	flag, err := scoped.GetRiskFlagInStore(id, storeID)
	if err != nil {
		if errors.Is(err, storage.ErrRiskFlagNotFound) {
			return notFound("risk flag not found")
		}
		return fmt.Errorf("failed to get risk flag: %w", err)
	}
	if user := traderservices.GetUser(r); user != nil && strings.EqualFold(user.Roles, string(permissions.Trader)) && flag.TraderId != user.Id {
		return notFound("risk flag not found")
	}

	return WriteJson(w, http.StatusOK, flag)
}

func (h *Handler) HandleDeleteRiskFlag(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid risk flag id", err)
	}

	scoped, ok := h.riskFlagStore.(storage.ScopedRiskFlagStorage)
	if !ok {
		return fmt.Errorf("risk flag repository does not support store-scoped operations")
	}
	storeID := traderservices.RequestStoreScope(r)
	before, err := scoped.GetRiskFlagInStore(id, storeID)
	if err != nil {
		if errors.Is(err, storage.ErrRiskFlagNotFound) {
			return notFound("risk flag not found")
		}
		return fmt.Errorf("failed to get risk flag: %w", err)
	}
	if h.auditLogger != nil {
		store, ok := h.riskFlagStore.(transactionalRiskFlagStore)
		if !ok {
			return fmt.Errorf("risk flag repository does not support transactions")
		}
		tx, err := store.BeginTx()
		if err != nil {
			return fmt.Errorf("begin risk flag deletion: %w", err)
		}
		defer tx.Rollback()
		err = store.DeleteRiskFlagWithTxInStore(tx, id, storeID)
		if err == nil {
			err = h.recordAuditWithTx(r, tx, "risk_flag.delete", "risk_flag", id, before, map[string]bool{"deleted": true})
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			if errors.Is(err, storage.ErrRiskFlagNotFound) {
				return notFound("risk flag not found")
			}
			return fmt.Errorf("failed to delete risk flag or write audit: %w", err)
		}
	} else if err := scoped.DeleteRiskFlagInStore(id, storeID); err != nil {
		if errors.Is(err, storage.ErrRiskFlagNotFound) {
			return notFound("risk flag not found")
		}
		return fmt.Errorf("failed to delete risk flag: %w", err)
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
	if req.AssignedTo != nil && *req.AssignedTo <= 0 {
		return badRequest("assigned_to must be a positive integer", nil)
	}

	// Récupérer le trader authentifié
	user := traderservices.GetUser(r)
	if user == nil {
		return &HTTPError{StatusCode: http.StatusUnauthorized, Message: "unauthenticated"}
	}

	// Récupérer le trader_id de l'utilisateur
	traderID := user.Id
	var beforeFlag *riskflagentity.RiskFlag
	if req.Status == string(riskflagentity.RiskFlagStatusResolved) || req.Status == string(riskflagentity.RiskFlagStatusFalsePositive) {
		scoped, ok := h.riskFlagStore.(storage.ScopedRiskFlagStorage)
		if !ok {
			return fmt.Errorf("risk flag repository does not support store-scoped reads")
		}
		flag, err := scoped.GetRiskFlagInStore(id, traderservices.RequestStoreScope(r))
		if err != nil {
			if errors.Is(err, storage.ErrRiskFlagNotFound) {
				return notFound("risk flag not found")
			}
			return fmt.Errorf("failed to get risk flag: %w", err)
		}
		beforeFlag = flag
		if flag.TransactionId != nil {
			transaction, err := h.transactionStore.GetTransaction(*flag.TransactionId)
			if err != nil {
				return fmt.Errorf("failed to get related transaction: %w", err)
			}
			if transaction.TraderID == traderID {
				return &HTTPError{StatusCode: http.StatusForbidden, Message: "the transaction creator cannot close its risk flag"}
			}
		}
	}
	// Valider la transition
	note := strings.TrimSpace(req.ResolutionNote)
	if req.Status == string(riskflagentity.RiskFlagStatusResolved) || req.Status == string(riskflagentity.RiskFlagStatusFalsePositive) {
		if strings.TrimSpace(note) == "" {
			return badRequest("resolution_note is required for RESOLVED or FALSE_POSITIVE status", nil)
		}
	}

	// Effectuer la transition
	var updatedFlag *riskflagentity.RiskFlag
	var tx *sql.Tx
	transactionalClose := beforeFlag != nil && h.auditLogger != nil
	if transactionalClose {
		store, ok := h.riskFlagStore.(transactionalRiskFlagStore)
		if !ok {
			return fmt.Errorf("risk flag repository does not support transactions")
		}
		tx, err = store.BeginTx()
		if err != nil {
			return fmt.Errorf("begin risk flag closure: %w", err)
		}
		defer tx.Rollback()
		updatedFlag, err = h.riskService.Investigation.TransitionRiskFlagInStoreWithTx(tx, id, traderservices.RequestStoreScope(r), req.Status, traderID, note, req.AssignedTo)
	} else {
		updatedFlag, err = h.riskService.Investigation.TransitionRiskFlagInStore(nil, id, traderservices.RequestStoreScope(r), req.Status, traderID, note, req.AssignedTo)
	}
	if err != nil {
		if errors.Is(err, storage.ErrRiskFlagNotFound) {
			return notFound("risk flag not found")
		}
		if errors.Is(err, storage.ErrInvalidStatusTransition) {
			return conflict(err.Error())
		}
		if errors.Is(err, storage.ErrResolutionNoteRequired) {
			return badRequest(err.Error(), nil)
		}
		if errors.Is(err, storage.ErrAssignedToRequired) {
			return badRequest(err.Error(), nil)
		}
		return fmt.Errorf("failed to transition risk flag: %w", err)
	}
	if beforeFlag != nil {
		if transactionalClose {
			if err := h.recordAuditWithTx(r, tx, "risk_flag.close", "risk_flag", id, beforeFlag, updatedFlag); err != nil {
				return fmt.Errorf("failed to audit risk flag closure: %w", err)
			}
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("commit risk flag closure: %w", err)
			}
		} else if err := h.recordAudit(r, "risk_flag.close", "risk_flag", id, beforeFlag, updatedFlag); err != nil {
			return fmt.Errorf("failed to audit risk flag closure: %w", err)
		}
	}

	return WriteJson(w, http.StatusOK, updatedFlag)
}

func (h *Handler) HandleGetRiskFlagHistory(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid risk flag id", err)
	}

	scoped, ok := h.riskFlagStore.(storage.ScopedRiskFlagStorage)
	if !ok {
		return fmt.Errorf("risk flag repository does not support store-scoped reads")
	}
	storeID := traderservices.RequestStoreScope(r)
	if _, err := scoped.GetRiskFlagInStore(id, storeID); err != nil {
		if errors.Is(err, storage.ErrRiskFlagNotFound) {
			return notFound("risk flag not found")
		}
		return fmt.Errorf("failed to check risk flag: %w", err)
	}
	history, err := h.riskService.Investigation.GetStatusHistoryInStore(id, storeID)
	if err != nil {
		return fmt.Errorf("failed to get status history: %w", err)
	}

	return WriteJson(w, http.StatusOK, history)
}
