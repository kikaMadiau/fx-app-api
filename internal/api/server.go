package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"fx-app-api/internal/api/handlers"
	customerservice "fx-app-api/internal/domain/customer/service"
	riskservice "fx-app-api/internal/domain/riskflag/service"
	"fx-app-api/internal/storage"
	tokenservice "fx-app-api/internal/utils/services"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/lib/pq"
)

type ApiServer struct {
	listenAddr string
	handlers   *handlers.Handler
}

func (s *ApiServer) Start() error {
	h := s.handlers

	router := chi.NewRouter()

	router.Post("/auth/trader/login", MakeHttpHandleFunc(h.HandleTraderLogin))
	router.Get("/analysais", MakeHttpHandleFunc(h.HandleListAnalysais))

	router.Post("/traders", MakeHttpHandleFunc(h.HandleCreateTrader))
	router.Get("/traders/{id}", MakeProtectedHttpHandleFunc(h.HandleGetTrader))
	router.Put("/traders/{id}", MakeProtectedHttpHandleFunc(h.HandleUpdateTrader))
	router.Delete("/traders/{id}", MakeProtectedHttpHandleFunc(h.HandleDeleteTrader))

	router.Post("/customers", MakeProtectedHttpHandleFunc(h.HandleCreateCustomer))
	router.Post("/customers/kyc", MakeProtectedHttpHandleFunc(h.HandleCreateCustomerWithKYC))
	router.Get("/customers/{id}", MakeProtectedHttpHandleFunc(h.HandleGetCustomer))
	router.Put("/customers/{id}", MakeProtectedHttpHandleFunc(h.HandleUpdateCustomer))
	router.Get("/customers/{id}/kyc", MakeProtectedHttpHandleFunc(h.HandleGetKYCProfile))
	router.Put("/customers/{id}/kyc", MakeProtectedHttpHandleFunc(h.HandleUpdateKYCProfile))
	router.Post("/customers/{id}/kyc/reassess", MakeProtectedHttpHandleFunc(h.HandleTriggerKYCReassessment))

	router.Post("/transactions", MakeProtectedHttpHandleFunc(h.HandleCreateTransaction))
	router.Get("/transactions/{id}", MakeProtectedHttpHandleFunc(h.HandleGetTransaction))

	router.Post("/risk-flags", MakeProtectedHttpHandleFunc(h.HandleCreateRiskFlag))
	router.Get("/risk-flags", MakeProtectedHttpHandleFunc(h.HandleListRiskFlags))
	router.Get("/risk-flags/{id}", MakeProtectedHttpHandleFunc(h.HandleGetRiskFlag))
	router.Delete("/risk-flags/{id}", MakeProtectedHttpHandleFunc(h.HandleDeleteRiskFlag))

	log.Println("API server running on", s.listenAddr)

	return http.ListenAndServe(s.listenAddr, router)
}

func WriteJson(w http.ResponseWriter, statusCode int, value any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	return json.NewEncoder(w).Encode(value)
}

type ApiFunc func(http.ResponseWriter, *http.Request) error
type ApiError struct {
	Message string `json:"message"`
}

type HTTPError struct {
	StatusCode int
	Message    string
	Err        error
}

type statusCodeError interface {
	error
	HTTPStatusCode() int
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

func MakeHttpHandleFunc(f ApiFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := f(w, r); err != nil {
			writeAPIError(w, err)
		}
	}
}

func MakeProtectedHttpHandleFunc(f ApiFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			WriteJson(w, http.StatusUnauthorized, ApiError{Message: "Missing authorization header"})
			return
		}

		tokenString, ok := strings.CutPrefix(authHeader, "Bearer ")
		if !ok || strings.TrimSpace(tokenString) == "" {
			WriteJson(w, http.StatusUnauthorized, ApiError{Message: "Invalid authorization header"})
			return
		}

		if err := tokenservice.VerifyToken(tokenString); err != nil {
			WriteJson(w, http.StatusUnauthorized, ApiError{Message: "Invalid token"})
			return
		}

		if err := f(w, r); err != nil {
			writeAPIError(w, err)
		}
	}
}

func writeAPIError(w http.ResponseWriter, err error) {
	statusCode := http.StatusInternalServerError
	message := err.Error()

	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		statusCode = httpErr.StatusCode
		message = httpErr.Error()
	} else {
		var statusErr statusCodeError
		if errors.As(err, &statusErr) {
			statusCode = statusErr.HTTPStatusCode()
			message = statusErr.Error()
		}
	}

	if statusCode == http.StatusInternalServerError {
		if sqlStatusCode, sqlMessage, ok := sqlConstraintHTTPError(err); ok {
			statusCode = sqlStatusCode
			message = sqlMessage
		}
	}

	WriteJson(w, statusCode, ApiError{Message: message})
}

func sqlConstraintHTTPError(err error) (int, string, bool) {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return 0, "", false
	}

	switch pqErr.Code {
	case "23505":
		return http.StatusConflict, "duplicate value violates unique constraint", true
	case "23503":
		return http.StatusBadRequest, "referenced resource does not exist", true
	case "23502":
		return http.StatusBadRequest, "required field is missing", true
	case "23514":
		return http.StatusBadRequest, "request violates check constraint", true
	case "23P01":
		return http.StatusConflict, "request conflicts with an existing record", true
	default:
		if strings.HasPrefix(string(pqErr.Code), "23") {
			return http.StatusBadRequest, "request violates database constraint", true
		}
	}

	return 0, "", false
}

func NewServer(listenAddr string, traderStore storage.TraderStorage, customerStore storage.CustomerStorage, transactionStore storage.TransactionStorage, riskFlagStore storage.RiskFlagStorage, riskService *riskservice.RiskService, customerService *customerservice.CustomerService) *ApiServer {
	return &ApiServer{
		listenAddr: listenAddr,
		handlers:   handlers.New(traderStore, customerStore, transactionStore, riskFlagStore, riskService, customerService),
	}
}
