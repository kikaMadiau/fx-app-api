package handlers

import (
	"encoding/json"
	"fmt"
	customerentity "fx-app-api/internal/domain/customer/entity"
	customerservice "fx-app-api/internal/domain/customer/service"
	riskservice "fx-app-api/internal/domain/riskflag/service"
	traderauthservice "fx-app-api/internal/domain/trader/traderservices"
	"fx-app-api/internal/storage"
	"net/http"
	"strconv"
	"strings"
)

type Handler struct {
	traderStore      storage.TraderStorage
	customerStore    storage.CustomerStorage
	transactionStore storage.TransactionStorage
	riskFlagStore    storage.RiskFlagStorage
	riskService      *riskservice.RiskService
	customerService  *customerservice.CustomerService
	authService      *traderauthservice.AuthService
}

type HTTPError struct {
	StatusCode int
	Message    string
	Err        error
}

func New(
	traderStore storage.TraderStorage,
	customerStore storage.CustomerStorage,
	transactionStore storage.TransactionStorage,
	riskFlagStore storage.RiskFlagStorage,
	riskService *riskservice.RiskService,
	customerService *customerservice.CustomerService,
) *Handler {
	return &Handler{
		traderStore:      traderStore,
		customerStore:    customerStore,
		transactionStore: transactionStore,
		riskFlagStore:    riskFlagStore,
		riskService:      riskService,
		customerService:  customerService,
		authService:      traderauthservice.NewAuthService(traderStore),
	}
}

func (e *HTTPError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}

	return e.Message
}

func (e *HTTPError) HTTPStatusCode() int {
	return e.StatusCode
}

func WriteJson(w http.ResponseWriter, statusCode int, value any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	return json.NewEncoder(w).Encode(value)
}

func decodeJSON(r *http.Request, destination any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(destination)
}

func pathID(r *http.Request) (int, error) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("id must be a positive integer")
	}

	return id, nil
}

func badRequest(message string, err error) error {
	return &HTTPError{StatusCode: http.StatusBadRequest, Message: message, Err: err}
}

func conflict(message string) error {
	return &HTTPError{StatusCode: http.StatusConflict, Message: message}
}

func notFound(message string) error {
	return &HTTPError{StatusCode: http.StatusNotFound, Message: message}
}

func optionalPositiveInt(rawValue, fieldName string) (int, error) {
	if strings.TrimSpace(rawValue) == "" {
		return 0, nil
	}

	value, err := strconv.Atoi(rawValue)
	if err != nil || value <= 0 {
		return 0, badRequest(fieldName+" must be a positive integer", err)
	}

	return value, nil
}

func customerFromRequest(req UpsertCustomerRequest) *customerentity.Customer {
	return &customerentity.Customer{
		FullName: req.FullName,
		IDNumber: req.IDNumber,
		IDType:   req.IDType,
		Phone:    req.Phone,
		Address:  req.Address,
	}
}
