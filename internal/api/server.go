package api

import (
	"encoding/json"
	"errors"
	"fmt"
	customerentity "fx-app-api/internal/domain/customer/entity"
	customerservice "fx-app-api/internal/domain/customer/service"
	riskflagentity "fx-app-api/internal/domain/riskflag/entity"
	riskservice "fx-app-api/internal/domain/riskflag/service"
	traderentity "fx-app-api/internal/domain/trader/entity"
	traderauthservice "fx-app-api/internal/domain/trader/traderservices"
	transactionentity "fx-app-api/internal/domain/transaction/entity"
	transactionservice "fx-app-api/internal/domain/transaction/service"
	"fx-app-api/internal/storage"
	tokenservice "fx-app-api/internal/utils/services"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ApiServer struct {
	listenAddr       string
	traderStore      storage.TraderStorage
	customerStore    storage.CustomerStorage
	transactionStore storage.TransactionStorage
	riskFlagStore    storage.RiskFlagStorage
	riskService      *riskservice.RiskService
	customerService  *customerservice.CustomerService
	authService      *traderauthservice.AuthService
}

func (s *ApiServer) Start() error {
	// Définir les routes de l'API
	http.HandleFunc("POST /auth/trader/login", MakeHttpHandleFunc(s.handleTraderLogin))

	http.HandleFunc("POST /traders", MakeHttpHandleFunc(s.handleCreateTrader))
	http.HandleFunc("GET /traders/{id}", MakeProtectedHttpHandleFunc(s.handleGetTrader))
	http.HandleFunc("PUT /traders/{id}", MakeProtectedHttpHandleFunc(s.handleUpdateTrader))
	http.HandleFunc("DELETE /traders/{id}", MakeProtectedHttpHandleFunc(s.handleDeleteTrader))

	http.HandleFunc("POST /customers", MakeProtectedHttpHandleFunc(s.handleCreateCustomer))
	http.HandleFunc("POST /customers/kyc", MakeProtectedHttpHandleFunc(s.handleCreateCustomerWithKYC))
	http.HandleFunc("GET /customers/{id}", MakeProtectedHttpHandleFunc(s.handleGetCustomer))
	http.HandleFunc("PUT /customers/{id}", MakeProtectedHttpHandleFunc(s.handleUpdateCustomer))
	http.HandleFunc("GET /customers/{id}/kyc", MakeProtectedHttpHandleFunc(s.handleGetKYCProfile))
	http.HandleFunc("PUT /customers/{id}/kyc", MakeProtectedHttpHandleFunc(s.handleUpdateKYCProfile))
	http.HandleFunc("POST /customers/{id}/kyc/reassess", MakeProtectedHttpHandleFunc(s.handleTriggerKYCReassessment))

	http.HandleFunc("POST /transactions", MakeProtectedHttpHandleFunc(s.handleCreateTransaction))
	http.HandleFunc("GET /transactions/{id}", MakeProtectedHttpHandleFunc(s.handleGetTransaction))

	http.HandleFunc("POST /risk-flags", MakeProtectedHttpHandleFunc(s.handleCreateRiskFlag))
	http.HandleFunc("GET /risk-flags", MakeProtectedHttpHandleFunc(s.handleListRiskFlags))
	http.HandleFunc("GET /risk-flags/{id}", MakeProtectedHttpHandleFunc(s.handleGetRiskFlag))
	http.HandleFunc("DELETE /risk-flags/{id}", MakeProtectedHttpHandleFunc(s.handleDeleteRiskFlag))

	log.Println("API server running on", s.listenAddr)

	return http.ListenAndServe(s.listenAddr, nil)
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

func (e *HTTPError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}

	return e.Message
}

func MakeHttpHandleFunc(f ApiFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := f(w, r); err != nil {
			statusCode := http.StatusInternalServerError
			if httpErr, ok := err.(*HTTPError); ok {
				statusCode = httpErr.StatusCode
			}
			WriteJson(w, statusCode, ApiError{Message: err.Error()})
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
			statusCode := http.StatusInternalServerError
			if httpErr, ok := err.(*HTTPError); ok {
				statusCode = httpErr.StatusCode
			}
			WriteJson(w, statusCode, ApiError{Message: err.Error()})
		}
	}
}

func NewServer(
	listenAddr string,
	traderStore storage.TraderStorage,
	customerStore storage.CustomerStorage,
	transactionStore storage.TransactionStorage,
	riskFlagStore storage.RiskFlagStorage,
	riskService *riskservice.RiskService,
	customerService *customerservice.CustomerService,
) *ApiServer {
	return &ApiServer{
		listenAddr:       listenAddr,
		traderStore:      traderStore,
		customerStore:    customerStore,
		transactionStore: transactionStore,
		riskFlagStore:    riskFlagStore,
		riskService:      riskService,
		customerService:  customerService,
		authService:      traderauthservice.NewAuthService(traderStore),
	}
}

// --- Handlers d'Authentification ---

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

func (s *ApiServer) handleTraderLogin(w http.ResponseWriter, r *http.Request) error {
	var req TraderLoginRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}

	result, err := s.authService.AuthenticateTrader(req.Email, req.Password)
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

// --- Handlers de Trader ---

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

func (s *ApiServer) handleCreateTrader(w http.ResponseWriter, r *http.Request) error {
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
	if err := s.traderStore.CreateTrader(trader); err != nil {
		return fmt.Errorf("failed to create trader: %w", err)
	}

	return WriteJson(w, http.StatusCreated, traderResponse(trader))
}

func (s *ApiServer) handleGetTrader(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid trader id", err)
	}

	trader, err := s.traderStore.GetTrader(id)
	if err != nil {
		return notFound(err.Error())
	}

	return WriteJson(w, http.StatusOK, traderResponse(trader))
}

func (s *ApiServer) handleUpdateTrader(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid trader id", err)
	}

	existingTrader, err := s.traderStore.GetTrader(id)
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

	if err := s.traderStore.UpdateTrader(existingTrader); err != nil {
		return fmt.Errorf("failed to update trader: %w", err)
	}

	return WriteJson(w, http.StatusOK, traderResponse(existingTrader))
}

func (s *ApiServer) handleDeleteTrader(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid trader id", err)
	}

	if _, err := s.traderStore.GetTrader(id); err != nil {
		return notFound(err.Error())
	}
	if err := s.traderStore.DeleteTrader(id); err != nil {
		return fmt.Errorf("failed to delete trader: %w", err)
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}

// --- Handlers de Customer / KYC ---

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

func (s *ApiServer) handleCreateCustomer(w http.ResponseWriter, r *http.Request) error {
	var req UpsertCustomerRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	if err := validateCustomerRequest(req); err != nil {
		return err
	}

	customer := customerFromRequest(req)
	if err := s.customerStore.CreateCustomer(customer); err != nil {
		return fmt.Errorf("failed to create customer: %w", err)
	}

	return WriteJson(w, http.StatusCreated, customer)
}

func (s *ApiServer) handleCreateCustomerWithKYC(w http.ResponseWriter, r *http.Request) error {
	var req CreateCustomerWithKYCRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	if err := validateCustomerRequest(req.Customer); err != nil {
		return err
	}

	customer := customerFromRequest(req.Customer)
	createdCustomer, err := s.customerService.CreateCustomerWithKYC(customer, &req.KYCProfile)
	if err != nil {
		return fmt.Errorf("failed to create customer with KYC: %w", err)
	}

	return WriteJson(w, http.StatusCreated, createdCustomer)
}

func (s *ApiServer) handleGetCustomer(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid customer id", err)
	}

	customer, err := s.customerStore.GetCustomer(id)
	if err != nil {
		return notFound(err.Error())
	}

	return WriteJson(w, http.StatusOK, customer)
}

func (s *ApiServer) handleUpdateCustomer(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid customer id", err)
	}

	customer, err := s.customerStore.GetCustomer(id)
	if err != nil {
		return notFound(err.Error())
	}

	var req UpsertCustomerRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	if err := validateCustomerRequest(req); err != nil {
		return err
	}

	customer.FullName = req.FullName
	customer.IDNumber = req.IDNumber
	customer.IDType = req.IDType
	customer.Phone = req.Phone
	customer.Address = req.Address

	if err := s.customerStore.UpdateCustomer(customer); err != nil {
		return fmt.Errorf("failed to update customer: %w", err)
	}

	return WriteJson(w, http.StatusOK, customer)
}

func (s *ApiServer) handleGetKYCProfile(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid customer id", err)
	}

	profile, err := s.customerService.GetKYCProfile(id)
	if err != nil {
		return notFound(err.Error())
	}

	return WriteJson(w, http.StatusOK, profile)
}

func (s *ApiServer) handleUpdateKYCProfile(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid customer id", err)
	}

	var profile customerservice.CustomerKYCProfile
	if err := decodeJSON(r, &profile); err != nil {
		return badRequest("invalid request body", err)
	}

	updatedProfile, err := s.customerService.UpdateKYCProfile(id, &profile)
	if err != nil {
		return fmt.Errorf("failed to update KYC profile: %w", err)
	}

	return WriteJson(w, http.StatusOK, updatedProfile)
}

func (s *ApiServer) handleTriggerKYCReassessment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid customer id", err)
	}

	customer, err := s.customerService.TriggerRiskReassessment(id)
	if err != nil {
		return fmt.Errorf("failed to trigger KYC risk reassessment: %w", err)
	}

	return WriteJson(w, http.StatusOK, customer)
}

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

func (s *ApiServer) handleCreateTransaction(w http.ResponseWriter, r *http.Request) error {
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
		if err := s.transactionStore.CreateTransactionWithNewCustomer(tx, customer); err != nil {
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
		if _, err := s.transactionStore.CreateTransactionForExistingCustomerPhone(tx, payload.ClientPhone); err != nil {
			if errors.Is(err, storage.ErrCustomerNotFound) {
				return notFound("no customer found with this phone")
			}
			return fmt.Errorf("failed to create transaction for existing customer: %w", err)
		}
	default:
		if req.CustomerID <= 0 {
			return badRequest("customer_id, client_phone, or client is required", nil)
		}
		if err := s.transactionStore.CreateTransaction(tx); err != nil {
			return fmt.Errorf("failed to create transaction: %w", err)
		}
	}

	if err := s.riskService.AnalyzeTransactionAndCustomer(tx); err != nil {
		return fmt.Errorf("failed to analyze transaction risk: %w", err)
	}

	return WriteJson(w, http.StatusCreated, tx)
}

func (s *ApiServer) handleGetTransaction(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid transaction id", err)
	}

	tx, err := s.transactionStore.GetTransaction(id)
	if err != nil {
		return notFound(err.Error())
	}

	return WriteJson(w, http.StatusOK, tx)
}

// --- Handlers de RiskFlag ---

type CreateRiskFlagRequest struct {
	Flag          string  `json:"flag"`
	Reason        string  `json:"reason"`
	Score         float64 `json:"score"`
	Level         string  `json:"level"`
	TransactionID int     `json:"transaction_id"`
	TraderID      int     `json:"trader_id"`
	CustomerID    int     `json:"customer_id"`
}

func (s *ApiServer) handleCreateRiskFlag(w http.ResponseWriter, r *http.Request) error {
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

	if err := s.riskFlagStore.CreateRiskFlag(flag); err != nil {
		return fmt.Errorf("failed to create risk flag: %w", err)
	}

	return WriteJson(w, http.StatusCreated, flag)
}

func (s *ApiServer) handleListRiskFlags(w http.ResponseWriter, r *http.Request) error {
	filter, err := riskFlagFilterFromQuery(r)
	if err != nil {
		return err
	}

	flags, err := s.riskFlagStore.ListRiskFlags(filter)
	if err != nil {
		return fmt.Errorf("failed to list risk flags: %w", err)
	}

	return WriteJson(w, http.StatusOK, flags)
}

func (s *ApiServer) handleGetRiskFlag(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid risk flag id", err)
	}

	flag, err := s.riskFlagStore.GetRiskFlag(id)
	if err != nil {
		return notFound(err.Error())
	}

	return WriteJson(w, http.StatusOK, flag)
}

func (s *ApiServer) handleDeleteRiskFlag(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid risk flag id", err)
	}

	if err := s.riskFlagStore.DeleteRiskFlag(id); err != nil {
		return notFound(err.Error())
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
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

func customerFromRequest(req UpsertCustomerRequest) *customerentity.Customer {
	return &customerentity.Customer{
		FullName: req.FullName,
		IDNumber: req.IDNumber,
		IDType:   req.IDType,
		Phone:    req.Phone,
		Address:  req.Address,
	}
}

func validateCustomerRequest(req UpsertCustomerRequest) error {
	if strings.TrimSpace(req.FullName) == "" {
		return badRequest("full_name is required", nil)
	}
	if strings.TrimSpace(req.IDNumber) == "" {
		return badRequest("id_number is required", nil)
	}

	return nil
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
