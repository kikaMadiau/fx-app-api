package middlewareInternal

import (
	traderentity "fx-app-api/internal/domain/trader/entity"
	"testing"
)

func TestIsActiveTrader(t *testing.T) {
	if isActiveTrader(nil) {
		t.Fatal("nil trader was accepted")
	}
	if isActiveTrader(&traderentity.Trader{IsActive: false}) {
		t.Fatal("inactive trader was accepted")
	}
	if !isActiveTrader(&traderentity.Trader{IsActive: true}) {
		t.Fatal("active trader was rejected")
	}
}
