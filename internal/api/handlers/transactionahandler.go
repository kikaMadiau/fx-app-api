package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	customerentity "fx-app-api/internal/domain/customer/entity"
	traderentity "fx-app-api/internal/domain/trader/entity"
	"fx-app-api/internal/domain/trader/permissions"
	traderservices "fx-app-api/internal/domain/trader/traderservices"
	transactionentity "fx-app-api/internal/domain/transaction/entity"
	transactionservice "fx-app-api/internal/domain/transaction/service"
	"fx-app-api/internal/storage"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// --- Handlers de Transaction ---

const (
	defaultTransactionPageSize = 20
	maxTransactionPageSize     = 100
)

type CreateTransactionRequest struct {
	Amount         float64 `json:"amount"`
	Currency       string  `json:"currency"`
	TargetCurrency string  `json:"target_currency"`
	Type           string  `json:"type"`
	Rate           float64 `json:"rate"`
	Status         string  `json:"status"`
	TraderID       int     `json:"trader_id"`
	CustomerID     int     `json:"customer_id"`
}

type CreateTransactionPayload struct {
	Client      *CreateTransactionCustomerRequest `json:"client"`
	ClientPhone string                            `json:"client_phone"`
	Transaction *CreateTransactionRequest         `json:"transaction"`

	Amount         float64 `json:"amount"`
	Currency       string  `json:"currency"`
	TargetCurrency string  `json:"target_currency"`
	Type           string  `json:"type"`
	Rate           float64 `json:"rate"`
	Status         string  `json:"status"`
	TraderID       int     `json:"trader_id"`
	CustomerID     int     `json:"customer_id"`
}

type CreateTransactionCustomerRequest struct {
	FullName    string     `json:"full_name"`
	FirstName   string     `json:"first_name"`
	LastName    string     `json:"last_name"`
	IDNumber    string     `json:"id_number"`
	IDType      string     `json:"id_type"`
	Phone       string     `json:"phone"`
	Address     string     `json:"address"`
	DateOfBirth *time.Time `json:"date_of_birth,omitempty"`
}

// TransactionListResponse est la page de résultats renvoyée par GET /transactions.
type TransactionListResponse struct {
	Items  []*transactionentity.TransactionListItem `json:"items"`
	Total  int                                      `json:"total"`
	Limit  int                                      `json:"limit"`
	Offset int                                      `json:"offset"`
}

func (h *Handler) HandleCreateTransaction(w http.ResponseWriter, r *http.Request) error {
	var payload CreateTransactionPayload
	if err := decodeJSON(r, &payload); err != nil {
		return badRequest("invalid request body", err)
	}

	trader := traderservices.GetUser(r)
	if trader == nil {
		return &HTTPError{StatusCode: http.StatusUnauthorized, Message: "authentication required"}
	}

	req := payload.transactionRequest()
	if err := validateTransactionRequest(&req, trader.Id); err != nil {
		return err
	}

	convertedAmount, err := transactionservice.CalculateConvertedAmount(req.Amount, req.Rate)
	if err != nil {
		return badRequest("invalid conversion input", err)
	}

	tx := &transactionentity.Transaction{
		Amount:          req.Amount,
		Currency:        req.Currency,
		TargetCurrency:  req.TargetCurrency,
		Type:            req.Type,
		Rate:            req.Rate,
		ConvertedAmount: convertedAmount,
		Status:          req.Status,
		TraderID:        req.TraderID,
		CustomerID:      req.CustomerID,
	}
	storeID, err := authenticatedStoreID(r)
	if err != nil {
		return err
	}
	tx.StoreID = storeID

	var dbTx *sql.Tx
	var errCreate error

	switch {
	case payload.Client != nil:
		if strings.TrimSpace(payload.ClientPhone) != "" || req.CustomerID > 0 {
			return badRequest("provide either client, client_phone, or customer_id, not multiple customer references", nil)
		}
		customer, err := customerFromTransactionRequest(*payload.Client)
		if err != nil {
			return err
		}
		dbTx, errCreate = h.transactionStore.CreateTransactionWithNewCustomer(tx, customer)
	case strings.TrimSpace(payload.ClientPhone) != "":
		if req.CustomerID > 0 {
			return badRequest("provide either client_phone or customer_id, not both", nil)
		}
		if _, err := storage.NormalizePhone(payload.ClientPhone); err != nil {
			return badRequest("invalid client_phone", err)
		}
		dbTx, _, errCreate = h.transactionStore.CreateTransactionForExistingCustomerPhone(tx, payload.ClientPhone)
	default:
		if req.CustomerID <= 0 {
			return badRequest("customer_id, client_phone, or client is required", nil)
		}
		// La transaction de change est insérée DANS la transaction SQL : si l'analyse AML échoue,
		// le rollback l'annule aussi.
		dbTx, errCreate = h.startTransaction()
		if errCreate == nil {
			if errCreate = h.transactionStore.CreateTransactionWithTx(dbTx, tx); errCreate != nil {
				_ = dbTx.Rollback()
				dbTx = nil
			}
		}
	}
	if errCreate != nil {
		return transactionWriteError(errCreate, "create transaction")
	}

	// Sans effet après un Commit réussi ; annule tout dans les autres cas (erreur, panic).
	if dbTx != nil {
		defer func() { _ = dbTx.Rollback() }()
	}

	// Analyse AML dans la même transaction SQL
	// TODO: appliquer une éventuelle décision de mise en PENDING selon le niveau de risque.
	if err := h.riskService.AnalyzeTransactionAndCustomerWithTx(dbTx, tx); err != nil {
		return fmt.Errorf("failed to analyze transaction risk: %w", err)
	}

	// Commit si l'analyse réussit
	if dbTx != nil {
		if err := dbTx.Commit(); err != nil {
			return fmt.Errorf("failed to commit transaction: %w", err)
		}
	}

	return WriteJson(w, http.StatusCreated, tx)
}

// HandleListTransactions renvoie une page de transactions avec le client associé.
//
// Paramètres de requête (tous optionnels) : q, status, type, risk_level, customer_id, limit, offset.
func (h *Handler) HandleListTransactions(w http.ResponseWriter, r *http.Request) error {
	filter, err := transactionFilterFromQuery(r)
	if err != nil {
		return err
	}
	filter = scopeTransactionFilter(filter, traderservices.GetUser(r))
	filter.StoreID, err = authenticatedStoreID(r)
	if err != nil {
		return err
	}

	items, total, err := h.transactionStore.SearchTransactions(filter)
	if err != nil {
		return fmt.Errorf("failed to list transactions: %w", err)
	}

	return WriteJson(w, http.StatusOK, TransactionListResponse{
		Items:  items,
		Total:  total,
		Limit:  filter.Limit,
		Offset: filter.Offset,
	})
}

func (h *Handler) HandleGetTransaction(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid transaction id", err)
	}

	storeID, storeErr := authenticatedStoreID(r)
	if storeErr != nil {
		return storeErr
	}
	tx, err := getTransactionInStore(h.transactionStore, id, storeID)
	if err != nil {
		return transactionWriteError(err, "get transaction")
	}
	if !canReadTransaction(tx, traderservices.GetUser(r)) {
		return notFound("transaction not found")
	}

	return WriteJson(w, http.StatusOK, tx)
}

func getTransactionInStore(store storage.TransactionStorage, id, storeID int) (*transactionentity.Transaction, error) {
	if scoped, ok := store.(storage.ScopedTransactionStorage); ok {
		return scoped.GetTransactionInStore(id, storeID)
	}
	return nil, storage.ErrTransactionNotFound
}

func scopeTransactionFilter(filter storage.TransactionFilter, user *traderentity.Trader) storage.TransactionFilter {
	if user != nil && !canReadWholeStoreTransactions(user) {
		filter.TraderID = user.Id
	}
	return filter
}
func canReadTransaction(tx *transactionentity.Transaction, user *traderentity.Trader) bool {
	return tx != nil && (user == nil || canReadWholeStoreTransactions(user) || tx.TraderID == user.Id)
}

func canReadWholeStoreTransactions(user *traderentity.Trader) bool {
	return containsRole(traderservices.RoleList(user), string(permissions.ComplianceOfficer)) || containsRole(traderservices.RoleList(user), string(permissions.Supervisor))
}

// validateTransactionRequest nettoie et valide les champs de la transaction (devises, type, statut)
// et rattache l'opération au trader authentifié : un trader ne peut pas enregistrer une opération
// au nom d'un autre. Un trader_id absent vaut le trader connecté.
func validateTransactionRequest(req *CreateTransactionRequest, authenticatedTraderID int) error {
	currency, err := transactionentity.NormalizeCurrency("currency", req.Currency)
	if err != nil {
		return badRequest(err.Error(), nil)
	}
	targetCurrency, err := transactionentity.NormalizeCurrency("target_currency", req.TargetCurrency)
	if err != nil {
		return badRequest(err.Error(), nil)
	}
	if currency == targetCurrency {
		return badRequest("currency and target_currency must be different", nil)
	}

	transactionType, err := transactionentity.NormalizeType(req.Type)
	if err != nil {
		return badRequest(err.Error(), nil)
	}
	status, err := transactionentity.NormalizeStatus(req.Status)
	if err != nil {
		return badRequest(err.Error(), nil)
	}

	switch {
	case req.TraderID == 0:
		req.TraderID = authenticatedTraderID
	case req.TraderID != authenticatedTraderID:
		return &HTTPError{StatusCode: http.StatusForbidden, Message: "trader_id must match the authenticated trader"}
	}

	req.Currency = currency
	req.TargetCurrency = targetCurrency
	req.Type = transactionType
	req.Status = status

	return nil
}

// transactionFilterFromQuery lit et valide les paramètres de GET /transactions.
func transactionFilterFromQuery(r *http.Request) (storage.TransactionFilter, error) {
	query := r.URL.Query()
	filter := storage.TransactionFilter{
		Query: strings.TrimSpace(query.Get("q")),
		Limit: defaultTransactionPageSize,
	}

	if len(filter.Query) > maxCustomerSearchLength {
		return filter, badRequest(fmt.Sprintf("q must be at most %d characters", maxCustomerSearchLength), nil)
	}

	if raw := strings.TrimSpace(query.Get("status")); raw != "" {
		status, err := transactionentity.NormalizeStatus(raw)
		if err != nil {
			return filter, badRequest(err.Error(), nil)
		}
		filter.Status = status
	}

	if raw := strings.TrimSpace(query.Get("type")); raw != "" {
		transactionType, err := transactionentity.NormalizeType(raw)
		if err != nil {
			return filter, badRequest(err.Error(), nil)
		}
		filter.Type = transactionType
	}

	if raw := strings.ToUpper(strings.TrimSpace(query.Get("risk_level"))); raw != "" {
		switch raw {
		case "LOW", "MEDIUM", "HIGH", "CRITICAL":
			filter.RiskLevel = raw
		default:
			return filter, badRequest("risk_level must be one of LOW, MEDIUM, HIGH, CRITICAL", nil)
		}
	}

	customerID, err := optionalPositiveInt(query.Get("customer_id"), "customer_id")
	if err != nil {
		return filter, err
	}
	filter.CustomerID = customerID

	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 || limit > maxTransactionPageSize {
			return filter, badRequest(fmt.Sprintf("limit must be an integer between 1 and %d", maxTransactionPageSize), err)
		}
		filter.Limit = limit
	}

	if raw := strings.TrimSpace(query.Get("offset")); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return filter, badRequest("offset must be a non-negative integer", err)
		}
		filter.Offset = offset
	}

	return filter, nil
}

// transactionWriteError traduit les erreurs métier du module transaction en erreurs HTTP
// explicites (404, 409, 400). Toute autre erreur reste une 500.
func transactionWriteError(err error, action string) error {
	switch {
	case errors.Is(err, storage.ErrIdentityConflict):
		return &HTTPError{StatusCode: http.StatusConflict, Code: "identity_conflict", Message: "phone is already associated with a different identity"}
	case errors.Is(err, storage.ErrCustomerPhoneExists):
		return conflict("customer with this phone already exists")
	case errors.Is(err, storage.ErrCustomerIDNumberExists):
		return conflict("customer with this id_number already exists")
	case errors.Is(err, storage.ErrCustomerNotFound):
		return notFound("no customer found with this phone")
	case errors.Is(err, storage.ErrCustomerIDNotFound):
		return notFound("customer not found")
	case errors.Is(err, storage.ErrTraderNotFound):
		return badRequest("trader not found", nil)
	case errors.Is(err, storage.ErrTransactionNotFound):
		return notFound("transaction not found")
	}

	return fmt.Errorf("failed to %s: %w", action, err)
}

func customerFromTransactionRequest(req CreateTransactionCustomerRequest) (*customerentity.Customer, error) {
	normalizedPhone, err := storage.NormalizePhone(req.Phone)
	if err != nil {
		return nil, badRequest("invalid customer phone", err)
	}

	fullName := strings.TrimSpace(req.FullName)
	if fullName == "" {
		fullName = strings.TrimSpace(strings.Join([]string{strings.TrimSpace(req.FirstName), strings.TrimSpace(req.LastName)}, " "))
	}
	if fullName == "" {
		return nil, badRequest("client full_name or first_name/last_name is required", nil)
	}

	idNumber := strings.TrimSpace(req.IDNumber)

	idType, err := customerentity.NormalizeIDType(req.IDType)
	if err != nil {
		return nil, badRequest(err.Error(), nil)
	}

	return &customerentity.Customer{
		FullName:    fullName,
		IDNumber:    idNumber,
		IDType:      idType,
		Phone:       normalizedPhone,
		Address:     req.Address,
		DateOfBirth: req.DateOfBirth,
	}, nil
}

func (p CreateTransactionPayload) transactionRequest() CreateTransactionRequest {
	if p.Transaction != nil {
		req := *p.Transaction
		if req.TargetCurrency == "" {
			req.TargetCurrency = p.TargetCurrency
		}
		if req.Rate == 0 {
			req.Rate = p.Rate
		}
		if req.Status == "" {
			req.Status = p.Status
		}
		if req.TraderID == 0 {
			req.TraderID = p.TraderID
		}
		if req.CustomerID == 0 {
			req.CustomerID = p.CustomerID
		}
		return req
	}

	return CreateTransactionRequest{
		Amount:         p.Amount,
		Currency:       p.Currency,
		TargetCurrency: p.TargetCurrency,
		Type:           p.Type,
		Rate:           p.Rate,
		Status:         p.Status,
		TraderID:       p.TraderID,
		CustomerID:     p.CustomerID,
	}
}
