package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	traderentity "fx-app-api/internal/domain/trader/entity"
)

func TestTraderResponseDoesNotExposePasswordHash(t *testing.T) {
	trader := &traderentity.Trader{Id: 7, Email: "trader@example.com", PasswordHash: "secret-hash"}
	encoded, err := json.Marshal(traderResponse(trader))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "password_hash") || strings.Contains(string(encoded), "secret-hash") {
		t.Fatalf("sensitive hash exposed: %s", encoded)
	}
}
