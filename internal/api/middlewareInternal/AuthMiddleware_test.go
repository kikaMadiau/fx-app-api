package middlewareInternal

import (
	traderentity "fx-app-api/internal/domain/trader/entity"
	"testing"
	"time"
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

func TestStoreLicenseGuard(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	if invalidStoreLicense(&traderentity.Store{Status: "ACTIVE"}, now) {
		t.Fatal("active store without expiration should be valid")
	}
	if !invalidStoreLicense(&traderentity.Store{Status: "SUSPENDED"}, now) {
		t.Fatal("suspended store should be invalid")
	}
	expired := now.Add(-time.Second)
	if !invalidStoreLicense(&traderentity.Store{Status: "ACTIVE", LicenseExpiresAt: &expired}, now) {
		t.Fatal("expired license should be invalid")
	}
	if !requiresValidStoreLicense("transaction:create") || requiresValidStoreLicense("transaction:read") {
		t.Fatal("license guard permissions are incorrect")
	}
}
