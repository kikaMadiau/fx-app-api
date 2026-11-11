package handlers

import (
	"errors"
	"fmt"
	customerentity "fx-app-api/internal/domain/customer/entity"
	customerservice "fx-app-api/internal/domain/customer/service"
	traderservices "fx-app-api/internal/domain/trader/traderservices"
	"fx-app-api/internal/storage"
	"net/http"
	"strconv"
	"strings"
)

// --- Handlers de Customer / KYC ---

const (
	defaultCustomerPageSize = 20
	maxCustomerPageSize     = 100
	maxCustomerSearchLength = 100
)

type UpsertCustomerRequest struct {
	FullName string `json:"full_name"`
	IDNumber string `json:"id_number"`
	IDType   string `json:"id_type"`
	Phone    string `json:"phone"`
	Address  string `json:"address"`
}

type CreateCustomerWithKYCRequest struct {
	Customer   UpsertCustomerRequest              `json:"customer"`
	KYCProfile customerservice.CustomerKYCProfile `json:"kyc_profile"`
}

// CustomerListResponse est la page de résultats renvoyée par GET /customers.
type CustomerListResponse struct {
	Items  []*customerentity.CustomerSummary `json:"items"`
	Total  int                               `json:"total"`
	Limit  int                               `json:"limit"`
	Offset int                               `json:"offset"`
}

func (h *Handler) HandleCreateCustomer(w http.ResponseWriter, r *http.Request) error {
	var req UpsertCustomerRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	if err := normalizeCustomerRequest(&req); err != nil {
		return err
	}

	customer := customerFromRequest(req)
	customer.StoreID = max(traderservices.RequestStoreScope(r), 0)
	if err := h.customerStore.CreateCustomer(customer); err != nil {
		return customerWriteError(err, "create customer")
	}

	return WriteJson(w, http.StatusCreated, customer)
}

func (h *Handler) HandleCreateCustomerWithKYC(w http.ResponseWriter, r *http.Request) error {
	var req CreateCustomerWithKYCRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	if err := normalizeCustomerRequest(&req.Customer); err != nil {
		return err
	}

	customer := customerFromRequest(req.Customer)
	customer.StoreID = max(traderservices.RequestStoreScope(r), 0)
	savedCustomer, created, err := h.customerService.CreateCustomerWithKYC(customer, &req.KYCProfile)
	if err != nil {
		return customerWriteError(err, "create customer with KYC")
	}

	// 201 pour un nouveau client, 200 quand un client existant (même téléphone) est réutilisé.
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}

	return WriteJson(w, status, savedCustomer)
}

// HandleListCustomers renvoie une page de clients avec leur statut KYC, leur nombre de
// transactions et leurs alertes de risque ouvertes.
//
// Paramètres de requête (tous optionnels) : q, kyc_status, risk_level, limit, offset.
func (h *Handler) HandleListCustomers(w http.ResponseWriter, r *http.Request) error {
	filter, err := customerFilterFromQuery(r)
	if err != nil {
		return err
	}

	filter.StoreID = traderservices.RequestStoreScope(r)
	customers, total, err := h.customerStore.ListCustomers(filter)
	if err != nil {
		return fmt.Errorf("failed to list customers: %w", err)
	}

	return WriteJson(w, http.StatusOK, CustomerListResponse{
		Items:  customers,
		Total:  total,
		Limit:  filter.Limit,
		Offset: filter.Offset,
	})
}

// HandleSearchCustomerByPhone retrouve un client à partir de son numéro de téléphone.
func (h *Handler) HandleSearchCustomerByPhone(w http.ResponseWriter, r *http.Request) error {
	phone := strings.TrimSpace(r.URL.Query().Get("phone"))
	if phone == "" {
		return badRequest("phone query parameter is required", nil)
	}

	normalizedPhone, err := storage.NormalizePhone(phone)
	if err != nil {
		return badRequest("invalid phone", err)
	}

	customer, err := h.customerStore.GetCustomerByPhone(normalizedPhone, traderservices.RequestStoreScope(r))
	if err != nil {
		if errors.Is(err, storage.ErrCustomerNotFound) {
			return notFound("no customer found with this phone")
		}
		return fmt.Errorf("failed to search customer by phone: %w", err)
	}

	return WriteJson(w, http.StatusOK, customer)
}

func (h *Handler) HandleGetCustomer(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid customer id", err)
	}

	scoped, ok := h.customerStore.(storage.ScopedCustomerStorage)
	if !ok {
		return fmt.Errorf("customer repository does not support store-scoped reads")
	}
	customer, err := scoped.GetCustomerInStore(id, traderservices.RequestStoreScope(r))
	if err != nil {
		return customerWriteError(err, "get customer")
	}

	return WriteJson(w, http.StatusOK, customer)
}

func (h *Handler) HandleUpdateCustomer(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid customer id", err)
	}

	scoped, ok := h.customerStore.(storage.ScopedCustomerStorage)
	if !ok {
		return fmt.Errorf("customer repository does not support store-scoped reads")
	}
	customer, err := scoped.GetCustomerInStore(id, traderservices.RequestStoreScope(r))
	if err != nil {
		return customerWriteError(err, "get customer")
	}

	var req UpsertCustomerRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	if err := normalizeCustomerRequest(&req); err != nil {
		return err
	}

	customer.FullName = req.FullName
	customer.IDNumber = req.IDNumber
	customer.IDType = req.IDType
	customer.Phone = req.Phone
	customer.Address = req.Address

	if err := h.customerStore.UpdateCustomer(customer); err != nil {
		return customerWriteError(err, "update customer")
	}

	return WriteJson(w, http.StatusOK, customer)
}

// normalizeCustomerRequest nettoie et valide le payload client : nom, numéro de pièce et
// téléphone obligatoires, téléphone mis sous forme normalisée, type de pièce dans l'enum.
func normalizeCustomerRequest(req *UpsertCustomerRequest) error {
	req.FullName = strings.TrimSpace(req.FullName)
	req.IDNumber = strings.TrimSpace(req.IDNumber)
	req.Address = strings.TrimSpace(req.Address)

	if req.FullName == "" {
		return badRequest("full_name is required", nil)
	}
	if req.IDNumber == "" {
		return badRequest("id_number is required", nil)
	}

	normalizedPhone, err := storage.NormalizePhone(req.Phone)
	if err != nil {
		return badRequest("invalid phone", err)
	}
	req.Phone = normalizedPhone

	idType, err := customerentity.NormalizeIDType(req.IDType)
	if err != nil {
		return badRequest(err.Error(), nil)
	}
	req.IDType = idType

	return nil
}

// customerFilterFromQuery lit et valide les paramètres de GET /customers.
func customerFilterFromQuery(r *http.Request) (storage.CustomerFilter, error) {
	query := r.URL.Query()
	filter := storage.CustomerFilter{
		Query:  strings.TrimSpace(query.Get("q")),
		Limit:  defaultCustomerPageSize,
		Offset: 0,
	}

	if len(filter.Query) > maxCustomerSearchLength {
		return filter, badRequest(fmt.Sprintf("q must be at most %d characters", maxCustomerSearchLength), nil)
	}

	if rawStatus := strings.ToUpper(strings.TrimSpace(query.Get("kyc_status"))); rawStatus != "" {
		if !customerservice.KYCStatus(rawStatus).Valid() {
			return filter, badRequest("kyc_status must be one of UNVERIFIED, PARTIALLY_VERIFIED, VERIFIED, ENHANCED_VERIFICATION", nil)
		}
		filter.KYCStatus = rawStatus
	}

	if rawLevel := strings.ToUpper(strings.TrimSpace(query.Get("risk_level"))); rawLevel != "" {
		switch rawLevel {
		case "LOW", "MEDIUM", "HIGH", "CRITICAL":
			filter.RiskLevel = rawLevel
		default:
			return filter, badRequest("risk_level must be one of LOW, MEDIUM, HIGH, CRITICAL", nil)
		}
	}

	if rawLimit := strings.TrimSpace(query.Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit <= 0 || limit > maxCustomerPageSize {
			return filter, badRequest(fmt.Sprintf("limit must be an integer between 1 and %d", maxCustomerPageSize), err)
		}
		filter.Limit = limit
	}

	if rawOffset := strings.TrimSpace(query.Get("offset")); rawOffset != "" {
		offset, err := strconv.Atoi(rawOffset)
		if err != nil || offset < 0 {
			return filter, badRequest("offset must be a non-negative integer", err)
		}
		filter.Offset = offset
	}

	return filter, nil
}

// customerWriteError traduit les erreurs métier du module customer/KYC en erreurs HTTP
// explicites (404, 409, 400). Toute autre erreur reste une 500.
func customerWriteError(err error, action string) error {
	switch {
	case errors.Is(err, storage.ErrCustomerPhoneExists):
		return conflict("customer with this phone already exists")
	case errors.Is(err, storage.ErrCustomerIDNumberExists):
		return conflict("customer with this id_number already exists")
	case errors.Is(err, storage.ErrCustomerIDNotFound):
		return notFound("customer not found")
	case errors.Is(err, customerservice.ErrKYCProfileNotFound):
		return notFound("KYC profile not found")
	case errors.Is(err, customerservice.ErrInvalidKYCProfile):
		return badRequest(err.Error(), nil)
	}

	return fmt.Errorf("failed to %s: %w", action, err)
}
