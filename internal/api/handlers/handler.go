package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	customerentity "fx-app-api/internal/domain/customer/entity"
	"fx-app-api/internal/domain/customer/iddocument"
	"fx-app-api/internal/domain/customer/kycrisk"
	customerservice "fx-app-api/internal/domain/customer/service"
	dashboardrepository "fx-app-api/internal/domain/dashboard/repository"
	"fx-app-api/internal/domain/notification"
	"fx-app-api/internal/domain/report"
	riskservice "fx-app-api/internal/domain/riskflag/service"
	storerepository "fx-app-api/internal/domain/store/repository"
	traderentity "fx-app-api/internal/domain/trader/entity"
	"fx-app-api/internal/domain/trader/passwordreset"
	"fx-app-api/internal/domain/trader/profile"
	"fx-app-api/internal/domain/trader/registration"
	traderauthservice "fx-app-api/internal/domain/trader/traderservices"
	"fx-app-api/internal/storage"
	"net/http"
	"strconv"
	"strings"
)

type Handler struct {
	traderStore       storage.TraderStorage
	customerStore     storage.CustomerStorage
	transactionStore  storage.TransactionStorage
	riskFlagStore     storage.RiskFlagStorage
	riskService       *riskservice.RiskService
	customerService   *customerservice.CustomerService
	authService       *traderauthservice.AuthService
	dashboardRepo     *dashboardrepository.DashboardRepository
	profileRepo       *profile.Repository
	idDocumentRepo    *iddocument.Repository
	reportRepo        *report.Repository
	kycRiskRepo       *kycrisk.Repository
	notificationRepo  *notification.Repository
	notifier          *notification.Service
	passwordResetRepo *passwordreset.Repository
	registrationRepo  *registration.Repository
	auditLogger       AuditLogger
	storeRepo         *storerepository.StoreRepository
	notifySettings    *notification.SettingsRepository
}

func (h *Handler) SetStoreRepository(repository *storerepository.StoreRepository) {
	h.storeRepo = repository
}

type AuditLogger interface {
	Record(actorID int, action, resourceType string, resourceID int, before, after any) error
}

type transactionalAuditLogger interface {
	RecordWithTx(tx *sql.Tx, actorID int, action, resourceType string, resourceID int, before, after any) error
}

type transactionalTraderStore interface {
	BeginTx() (*sql.Tx, error)
	CreateTraderWithTx(*sql.Tx, *traderentity.Trader) error
	UpdateTraderWithTx(*sql.Tx, *traderentity.Trader) error
	DeleteTraderWithTx(*sql.Tx, int) error
}

type transactionalRiskFlagStore interface {
	BeginTx() (*sql.Tx, error)
	DeleteRiskFlagWithTx(*sql.Tx, int) error
}

func (h *Handler) SetAuditLogger(logger AuditLogger) { h.auditLogger = logger }

func (h *Handler) recordAudit(r *http.Request, action, resourceType string, resourceID int, before, after any) error {
	if h.auditLogger == nil {
		return nil
	}
	user := traderauthservice.GetUser(r)
	if user == nil {
		return fmt.Errorf("authenticated user is unavailable for audit")
	}
	return h.auditLogger.Record(user.Id, action, resourceType, resourceID, before, after)
}

func authenticatedStoreID(r *http.Request) (int, error) {
	user := traderauthservice.GetUser(r)
	if user == nil {
		return 0, &HTTPError{StatusCode: http.StatusUnauthorized, Message: "authentication required"}
	}
	if user.IsPlatformAdmin || user.StoreId <= 0 {
		return 0, notFound("store not found")
	}
	return user.StoreId, nil
}

func (h *Handler) recordAuditWithTx(r *http.Request, tx *sql.Tx, action, resourceType string, resourceID int, before, after any) error {
	if h.auditLogger == nil {
		return nil
	}
	user := traderauthservice.GetUser(r)
	if user == nil {
		return fmt.Errorf("authenticated user is unavailable for audit")
	}
	logger, ok := h.auditLogger.(transactionalAuditLogger)
	if !ok {
		return fmt.Errorf("audit logger does not support transactions")
	}
	return logger.RecordWithTx(tx, user.Id, action, resourceType, resourceID, before, after)
}

type HTTPError struct {
	StatusCode int
	Message    string
	Code       string
	Err        error
}

func (h *Handler) SetDashboardRepository(repository *dashboardrepository.DashboardRepository) {
	h.dashboardRepo = repository
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

func (e *HTTPError) PublicMessage() string { return e.Message }
func (e *HTTPError) PublicCode() string    { return e.Code }

func WriteJson(w http.ResponseWriter, statusCode int, value any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	return json.NewEncoder(w).Encode(value)
}

func decodeJSON(r *http.Request, destination any) error {
	const maxJSONBodyBytes = 1 << 20 // 1 MiB
	r.Body = http.MaxBytesReader(nil, r.Body, maxJSONBodyBytes)
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
		FullName:    req.FullName,
		IDNumber:    req.IDNumber,
		IDType:      req.IDType,
		Phone:       req.Phone,
		Address:     req.Address,
		DateOfBirth: req.DateOfBirth,
	}
}

// startTransaction démarre une nouvelle transaction SQL.
func (h *Handler) startTransaction() (*sql.Tx, error) {
	return h.transactionStore.BeginTx()
}
