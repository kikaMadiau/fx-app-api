package handlers

import (
	"testing"

	traderentity "fx-app-api/internal/domain/trader/entity"
	"fx-app-api/internal/domain/transaction/entity"
	"fx-app-api/internal/storage"
)

func TestTraderTransactionScope(t *testing.T) {
	filter := storage.TransactionFilter{TraderID: 99, CustomerID: 7}
	got := scopeTransactionFilter(filter, &traderentity.Trader{Id: 12, Roles: "TRADER"})
	if got.TraderID != 12 || got.CustomerID != 7 {
		t.Fatalf("filter scope = %+v", got)
	}
	if !canReadTransaction(&entity.Transaction{TraderID: 12}, &traderentity.Trader{Id: 12, Roles: "TRADER"}) {
		t.Fatal("trader should see own transaction")
	}
	if canReadTransaction(&entity.Transaction{TraderID: 99}, &traderentity.Trader{Id: 12, Roles: "TRADER"}) {
		t.Fatal("trader can see another trader's transaction")
	}
	if !canReadTransaction(&entity.Transaction{TraderID: 99}, &traderentity.Trader{Id: 12, Roles: "COMPLIANCE_OFFICER"}) {
		t.Fatal("compliance should see all transactions")
	}
}
