package handlers

import (
	"errors"
	"testing"

	"fx-app-api/internal/storage"
)

func TestTransactionCustomerDoesNotUsePhoneAsIdentityDocument(t *testing.T) {
	customer, err := customerFromTransactionRequest(CreateTransactionCustomerRequest{
		FullName: "Client Test",
		Phone:    "+243810000001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if customer.IDNumber != "" {
		t.Fatalf("missing identity document became %q", customer.IDNumber)
	}
}

func TestIdentityConflictMapsToHTTP409Code(t *testing.T) {
	err := customerWriteError(storage.ErrIdentityConflict, "create customer")
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error type = %T, want *HTTPError", err)
	}
	if httpErr.StatusCode != 409 || httpErr.Code != "identity_conflict" {
		t.Fatalf("status/code = %d/%q, want 409/identity_conflict", httpErr.StatusCode, httpErr.Code)
	}
}
