package api

import (
	"encoding/json"
	"fmt"
	"fx-app-api/internal/api/handlers"
	"fx-app-api/internal/api/middlewareInternal"
	traderentity "fx-app-api/internal/domain/trader/entity"
	"fx-app-api/internal/domain/trader/permissions"
	traderservices "fx-app-api/internal/domain/trader/traderservices"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/lib/pq"
)

func TestMakeHTTPHandleFuncMapsUniqueViolationToConflict(t *testing.T) {
	response := executeFailingHandler(fmt.Errorf("create trader: %w", &pq.Error{Code: "23505"}))

	if response.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d", response.Code)
	}
	assertAPIErrorMessage(t, response, "duplicate value violates unique constraint")
}

func TestMakeHTTPHandleFuncMapsForeignKeyViolationToBadRequest(t *testing.T) {
	response := executeFailingHandler(fmt.Errorf("create transaction: %w", &pq.Error{Code: "23503"}))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", response.Code)
	}
	assertAPIErrorMessage(t, response, "referenced resource does not exist")
}

func TestMakeHTTPHandleFuncDoesNotExposeUnexpectedErrors(t *testing.T) {
	response := executeFailingHandler(fmt.Errorf("repository unavailable"))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", response.Code)
	}
	assertAPIErrorMessage(t, response, "internal server error")
}

func TestMakeHTTPHandleFuncMapsHandlerHTTPError(t *testing.T) {
	response := executeFailingHandler(&handlers.HTTPError{
		StatusCode: http.StatusBadRequest,
		Message:    "invalid request body",
	})

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", response.Code)
	}
	assertAPIErrorMessage(t, response, "invalid request body")
}

func executeFailingHandler(err error) *httptest.ResponseRecorder {
	handler := MakeHttpHandleFunc(func(w http.ResponseWriter, r *http.Request) error {
		return err
	})

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler(response, request)

	return response
}

func assertAPIErrorMessage(t *testing.T, response *httptest.ResponseRecorder, expected string) {
	t.Helper()

	var apiErr ApiError
	if err := json.NewDecoder(response.Body).Decode(&apiErr); err != nil {
		t.Fatalf("failed to decode API error: %v", err)
	}
	if apiErr.Message != expected {
		t.Fatalf("expected message %q, got %q", expected, apiErr.Message)
	}
}

func TestProtectedRoutePermissionMatrix(t *testing.T) {
	allowed := map[string][]permissions.Role{
		permissions.ProfileRead:          {permissions.Trader, permissions.ComplianceOfficer, permissions.Supervisor, permissions.PlatformAdmin},
		permissions.ProfileUpdate:        {permissions.Trader, permissions.ComplianceOfficer, permissions.Supervisor, permissions.PlatformAdmin},
		permissions.StoreRead:            {permissions.Trader, permissions.ComplianceOfficer, permissions.Supervisor},
		permissions.StoreManageMembers:   {permissions.Supervisor},
		permissions.PlatformManageStores: {permissions.PlatformAdmin},
		permissions.DashboardRead:        {permissions.Trader, permissions.ComplianceOfficer, permissions.Supervisor, permissions.PlatformAdmin},
		permissions.AnalysisRead:         {permissions.Trader, permissions.ComplianceOfficer, permissions.Supervisor, permissions.PlatformAdmin},
		permissions.TraderManage:         {permissions.PlatformAdmin},
		permissions.CustomerCreate:       {permissions.Trader, permissions.ComplianceOfficer, permissions.Supervisor, permissions.PlatformAdmin},
		permissions.CustomerRead:         {permissions.Trader, permissions.ComplianceOfficer, permissions.Supervisor, permissions.PlatformAdmin},
		permissions.CustomerUpdate:       {permissions.Trader, permissions.ComplianceOfficer, permissions.PlatformAdmin},
		permissions.KYCRead:              {permissions.Trader, permissions.ComplianceOfficer, permissions.PlatformAdmin},
		permissions.KYCUpdate:            {permissions.Trader, permissions.ComplianceOfficer, permissions.PlatformAdmin},
		permissions.KYCVerify:            {permissions.ComplianceOfficer},
		permissions.KYCReassess:          {permissions.ComplianceOfficer, permissions.PlatformAdmin},
		permissions.TransactionCreate:    {permissions.Trader, permissions.ComplianceOfficer, permissions.Supervisor, permissions.PlatformAdmin},
		permissions.TransactionRead:      {permissions.Trader, permissions.ComplianceOfficer, permissions.Supervisor, permissions.PlatformAdmin},
		permissions.RiskFlagCreate:       {permissions.Trader, permissions.ComplianceOfficer, permissions.Supervisor},
		permissions.RiskFlagRead:         {permissions.Trader, permissions.ComplianceOfficer, permissions.Supervisor, permissions.PlatformAdmin},
		permissions.RiskFlagInvestigate:  {permissions.ComplianceOfficer},
		permissions.RiskFlagDelete:       {permissions.PlatformAdmin},
	}
	seen := map[string]bool{}
	router := chi.NewRouter()
	registerProtectedRoutes(router, &handlers.Handler{})
	registered := map[string]bool{}
	if err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		registered[method+" "+route] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, route := range protectedRouteDefinitions {
		key := route.Method + " " + route.Path
		if seen[key] {
			t.Errorf("duplicate protected route %s", key)
		}
		seen[key] = true
		if !registered[key] {
			t.Errorf("declared route %s is missing from router", key)
		}
		expected, exists := allowed[route.Permission]
		if !exists {
			t.Errorf("route %s has unknown or missing permission %q", key, route.Permission)
			continue
		}
		want := map[permissions.Role]bool{}
		for _, role := range expected {
			want[role] = true
		}
		for _, role := range permissions.Roles() {
			if got := permissions.Has(string(role), route.Permission); got != want[role] {
				t.Errorf("%s for %s: permission %s = %v, want %v", key, role, route.Permission, got, want[role])
			}
			permissionHandler := middlewareInternal.RequirePermission(route.Permission)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
			baseRequest := httptest.NewRequest(route.Method, route.Path, nil)
			request := baseRequest.WithContext(traderservices.WithUser(baseRequest.Context(), &traderentity.Trader{Roles: string(role), Store: &traderentity.Store{Status: "ACTIVE"}}))
			response := httptest.NewRecorder()
			permissionHandler.ServeHTTP(response, request)
			wantStatus := http.StatusForbidden
			if want[role] {
				wantStatus = http.StatusOK
			}
			if response.Code != wantStatus {
				t.Errorf("%s for %s returned %d, want %d", key, role, response.Code, wantStatus)
			}
		}
	}
	if len(seen) != len(protectedRouteDefinitions) {
		t.Fatal("every protected route must have an explicit permission")
	}
	if len(registered) != len(seen) {
		t.Errorf("router has %d routes, permission declarations have %d", len(registered), len(seen))
	}
}
