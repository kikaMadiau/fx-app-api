package handlers

import (
	"errors"
	"fmt"
	customerentity "fx-app-api/internal/domain/customer/entity"
	transactionentity "fx-app-api/internal/domain/transaction/entity"
	transactionservice "fx-app-api/internal/domain/transaction/service"
	"fx-app-api/internal/storage"
	"net/http"
	"strings"
)

// --- Handlers de Transaction ---

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
	FullName  string `json:"full_name"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	IDNumber  string `json:"id_number"`
	IDType    string `json:"id_type"`
	Phone     string `json:"phone"`
	Address   string `json:"address"`
}

func (h *Handler) HandleCreateTransaction(w http.ResponseWriter, r *http.Request) error {
	var payload CreateTransactionPayload
	if err := decodeJSON(r, &payload); err != nil {
		return badRequest("invalid request body", err)
	}

	req := payload.transactionRequest()
	if err := validateTransactionRequest(req); err != nil {
		return err
	}

	convertedAmount, err := transactionservice.CalculateConvertedAmount(req.Amount, req.Rate)
	if err != nil {
		return badRequest("invalid conversion input", err)
	}

	tx := &transactionentity.Transaction{
		Amount:          req.Amount,
		Currency:        strings.ToUpper(strings.TrimSpace(req.Currency)),
		TargetCurrency:  strings.ToUpper(strings.TrimSpace(req.TargetCurrency)),
		Type:            req.Type,
		Rate:            req.Rate,
		ConvertedAmount: convertedAmount,
		Status:          req.Status,
		TraderID:        req.TraderID,
		CustomerID:      req.CustomerID,
	}

	switch {
	case payload.Client != nil:
		if strings.TrimSpace(payload.ClientPhone) != "" || req.CustomerID > 0 {
			return badRequest("provide either client, client_phone, or customer_id, not multiple customer references", nil)
		}
		customer, err := customerFromTransactionRequest(*payload.Client)
		if err != nil {
			return err
		}
		if err := h.transactionStore.CreateTransactionWithNewCustomer(tx, customer); err != nil {
			if errors.Is(err, storage.ErrCustomerPhoneExists) {
				return conflict("customer with this phone already exists")
			}
			return fmt.Errorf("failed to create transaction with new customer: %w", err)
		}
	case strings.TrimSpace(payload.ClientPhone) != "":
		if req.CustomerID > 0 {
			return badRequest("provide either client_phone or customer_id, not both", nil)
		}
		if _, err := storage.NormalizePhone(payload.ClientPhone); err != nil {
			return badRequest("invalid client_phone", err)
		}
		if _, err := h.transactionStore.CreateTransactionForExistingCustomerPhone(tx, payload.ClientPhone); err != nil {
			if errors.Is(err, storage.ErrCustomerNotFound) {
				return notFound("no customer found with this phone")
			}
			return fmt.Errorf("failed to create transaction for existing customer: %w", err)
		}
	default:
		if req.CustomerID <= 0 {
			return badRequest("customer_id, client_phone, or client is required", nil)
		}
		if err := h.transactionStore.CreateTransaction(tx); err != nil {
			return fmt.Errorf("failed to create transaction: %w", err)
		}
	}

	if err := h.riskService.AnalyzeTransactionAndCustomer(tx); err != nil {
		return fmt.Errorf("failed to analyze transaction risk: %w", err)
	}

	return WriteJson(w, http.StatusCreated, tx)
}

func (h *Handler) HandleGetTransaction(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid transaction id", err)
	}

	tx, err := h.transactionStore.GetTransaction(id)
	if err != nil {
		return notFound(err.Error())
	}

	return WriteJson(w, http.StatusOK, tx)
}

func validateTransactionRequest(req CreateTransactionRequest) error {
	if strings.TrimSpace(req.Currency) == "" {
		return badRequest("currency is required", nil)
	}
	if strings.TrimSpace(req.TargetCurrency) == "" {
		return badRequest("target_currency is required", nil)
	}
	if req.TraderID <= 0 {
		return badRequest("trader_id is required", nil)
	}

	return nil
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
	if idNumber == "" {
		idNumber = normalizedPhone
	}

	return &customerentity.Customer{
		FullName: fullName,
		IDNumber: idNumber,
		IDType:   req.IDType,
		Phone:    normalizedPhone,
		Address:  req.Address,
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
