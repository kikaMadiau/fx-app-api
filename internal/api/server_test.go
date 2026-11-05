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
		permissionHasRole := false
		for _, role := range permissions.Roles() {
			want := permissions.Has(string(role), route.Permission)
			permissionHasRole = permissionHasRole || want
			permissionHandler := middlewareInternal.RequirePermission(route.Permission)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
			baseRequest := httptest.NewRequest(route.Method, route.Path, nil)
			request := baseRequest.WithContext(traderservices.WithUser(baseRequest.Context(), &traderentity.Trader{Roles: string(role), Store: &traderentity.Store{Status: "ACTIVE"}}))
			response := httptest.NewRecorder()
			permissionHandler.ServeHTTP(response, request)
			wantStatus := http.StatusForbidden
			if want {
				wantStatus = http.StatusOK
			}
			if response.Code != wantStatus {
				t.Errorf("%s for %s returned %d, want %d", key, role, response.Code, wantStatus)
			}
		}
		if !permissionHasRole {
			t.Errorf("route %s has unknown permission or no authorized role: %q", key, route.Permission)
		}
	}
	if len(seen) != len(protectedRouteDefinitions) {
		t.Fatal("every protected route must have an explicit permission")
	}
	if len(registered) != len(seen) {
		t.Errorf("router has %d routes, permission declarations have %d", len(registered), len(seen))
	}
}
