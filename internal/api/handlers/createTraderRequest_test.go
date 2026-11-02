package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateTraderRequestRejectsPasswordHashInput(t *testing.T) {
	var request CreateTraderRequest
	r := httptest.NewRequest("POST", "/traders", strings.NewReader(`{"email":"a@example.com","password":"a valid password","password_hash":"attacker-controlled"}`))
	err := decodeJSON(r, &request)
	if err == nil {
		t.Fatal("password_hash must not be accepted from the API request")
	}
}
