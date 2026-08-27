package api

import (
	"encoding/json"
	"fmt"
	"fx-app-api/internal/api/handlers"
	"net/http"
	"net/http/httptest"
	"testing"

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

func TestMakeHTTPHandleFuncKeepsUnexpectedErrorsInternal(t *testing.T) {
	response := executeFailingHandler(fmt.Errorf("repository unavailable"))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", response.Code)
	}
	assertAPIErrorMessage(t, response, "repository unavailable")
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
