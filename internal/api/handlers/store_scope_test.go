package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	customerentity "fx-app-api/internal/domain/customer/entity"
	customerservice "fx-app-api/internal/domain/customer/service"
	riskflagentity "fx-app-api/internal/domain/riskflag/entity"
	riskservice "fx-app-api/internal/domain/riskflag/service"
	traderentity "fx-app-api/internal/domain/trader/entity"
	traderservices "fx-app-api/internal/domain/trader/traderservices"
	transactionentity "fx-app-api/internal/domain/transaction/entity"
	"fx-app-api/internal/storage"
)

type scopedTransactionMemory struct {
	storage.TransactionStorage
	storeID int
	missing bool
}

func (s *scopedTransactionMemory) GetTransactionInStore(_ int, storeID int) (*transactionentity.Transaction, error) {
	s.storeID = storeID
	if s.missing {
		return nil, storage.ErrTransactionNotFound
	}
	return &transactionentity.Transaction{ID: 10, StoreID: storeID, TraderID: 4}, nil
}

type scopedRiskMemory struct {
	storage.RiskFlagStorage
	storage.ScopedRiskFlagStorage
	storeID int
	missing bool
}

type scopedKYCMemory struct {
	customerservice.KYCProfileStorage
	storeID int
}

func (s *scopedKYCMemory) GetKYCProfileByCustomerIDWithTxInStore(_ *sql.Tx, customerID, storeID int) (*customerservice.CustomerKYCProfile, error) {
	s.storeID = storeID
	return &customerservice.CustomerKYCProfile{CustomerID: customerID, StoreID: storeID, KYCStatus: customerservice.Unverified}, nil
}

func (s *scopedRiskMemory) GetRiskFlagInStore(_ int, storeID int) (*riskflagentity.RiskFlag, error) {
	s.storeID = storeID
	if s.missing {
		return nil, storage.ErrRiskFlagNotFound
	}
	return &riskflagentity.RiskFlag{ID: 11, StoreID: storeID}, nil
}

func TestTransactionReadUsesStoreAndHidesForeignRecord(t *testing.T) {
	store := &scopedTransactionMemory{missing: true}
	h := &Handler{transactionStore: store}
	req := httptest.NewRequest("GET", "/transactions/10", nil)
	req.SetPathValue("id", "10")
	req = req.WithContext(traderservices.WithUser(req.Context(), &traderentity.Trader{Id: 3, StoreId: 7, Roles: "TRADER"}))
	err := h.HandleGetTransaction(httptest.NewRecorder(), req)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 404 || store.storeID != 7 {
		t.Fatalf("error=%v scoped store=%d, want 404 and store 7", err, store.storeID)
	}
}

func TestRiskFlagReadUsesStoreAndHidesForeignRecord(t *testing.T) {
	store := &scopedRiskMemory{missing: true}
	h := &Handler{riskFlagStore: store}
	req := httptest.NewRequest("GET", "/risk-flags/11", nil)
	req.SetPathValue("id", "11")
	req = req.WithContext(traderservices.WithUser(req.Context(), &traderentity.Trader{Id: 3, StoreId: 9, Roles: "COMPLIANCE_OFFICER"}))
	err := h.HandleGetRiskFlag(httptest.NewRecorder(), req)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 404 || store.storeID != 9 {
		t.Fatalf("error=%v scoped store=%d, want 404 and store 9", err, store.storeID)
	}
}

func TestRiskFlagHistoryAndDeleteHideForeignRecord(t *testing.T) {
	store := &scopedRiskMemory{missing: true}
	h := &Handler{riskFlagStore: store, riskService: riskservice.NewRiskService(nil, nil, store, nil)}
	user := &traderentity.Trader{Id: 3, StoreId: 9, Roles: "COMPLIANCE_OFFICER"}

	for _, tc := range []struct {
		path, method string
		run          func(http.ResponseWriter, *http.Request) error
	}{
		{"/risk-flags/11/history", "GET", h.HandleGetRiskFlagHistory},
		{"/risk-flags/11", "DELETE", h.HandleDeleteRiskFlag},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.SetPathValue("id", "11")
		req = req.WithContext(traderservices.WithUser(req.Context(), user))
		err := tc.run(httptest.NewRecorder(), req)
		var httpErr *HTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != 404 || store.storeID != 9 {
			t.Fatalf("%s %s: error=%v scoped store=%d, want 404 and store 9", tc.method, tc.path, err, store.storeID)
		}
	}
}

func TestKYCReadUsesStoreAndHidesForeignCustomer(t *testing.T) {
	customers := &scopedCustomerMemory{customer: &customerentity.Customer{StoreID: 7}}
	kyc := &scopedKYCMemory{}
	h := &Handler{customerStore: customers, customerService: customerservice.NewCustomerServiceWithKYC(customers, kyc)}
	req := httptest.NewRequest("GET", "/customers/12/kyc", nil)
	req.SetPathValue("id", "12")
	req = req.WithContext(traderservices.WithUser(req.Context(), &traderentity.Trader{Id: 3, StoreId: 7, Roles: "TRADER"}))
	if err := h.HandleGetKYCProfile(httptest.NewRecorder(), req); err != nil {
		t.Fatal(err)
	}
	if kyc.storeID != 7 {
		t.Fatalf("KYC store scope=%d, want 7", kyc.storeID)
	}

	kyc.storeID = 0
	req = httptest.NewRequest("GET", "/customers/12/kyc", nil)
	req.SetPathValue("id", "12")
	req = req.WithContext(traderservices.WithUser(req.Context(), &traderentity.Trader{Id: 3, StoreId: 8, Roles: "TRADER"}))
	err := h.HandleGetKYCProfile(httptest.NewRecorder(), req)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 404 || kyc.storeID != 0 {
		t.Fatalf("error=%v KYC store scope=%d, want 404 without KYC lookup", err, kyc.storeID)
	}
}

func TestTransactionVisibilityByRole(t *testing.T) {
	tx := &transactionentity.Transaction{ID: 10, StoreID: 7, TraderID: 4}
	trader := &traderentity.Trader{Id: 3, StoreId: 7, Roles: "TRADER"}
	filter := scopeTransactionFilter(storage.TransactionFilter{StoreID: 7}, trader)
	if filter.StoreID != 7 || filter.TraderID != trader.Id || canReadTransaction(tx, trader) {
		t.Fatalf("TRADER scope/filter=%+v should not read another trader's transaction", filter)
	}
	supervisor := &traderentity.Trader{Id: 5, StoreId: 7, Roles: "TRADER,SUPERVISOR"}
	filter = scopeTransactionFilter(storage.TransactionFilter{StoreID: 7}, supervisor)
	if filter.StoreID != 7 || filter.TraderID != 0 || !canReadTransaction(tx, supervisor) {
		t.Fatalf("SUPERVISOR scope/filter=%+v should read transactions in the store", filter)
	}
}
