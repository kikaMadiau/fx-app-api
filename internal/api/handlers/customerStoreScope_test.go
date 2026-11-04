package handlers

import (
	"database/sql"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	customerentity "fx-app-api/internal/domain/customer/entity"
	traderentity "fx-app-api/internal/domain/trader/entity"
	traderservices "fx-app-api/internal/domain/trader/traderservices"
	"fx-app-api/internal/storage"
)

type scopedCustomerMemory struct {
	requestedStore int
	customer       *customerentity.Customer
	updatedStore   int
}

func (*scopedCustomerMemory) Init() error                                   { return nil }
func (*scopedCustomerMemory) BeginTx() (*sql.Tx, error)                     { return nil, nil }
func (*scopedCustomerMemory) CreateCustomer(*customerentity.Customer) error { return nil }
func (*scopedCustomerMemory) CreateCustomerWithTx(*sql.Tx, *customerentity.Customer) error {
	return nil
}
func (s *scopedCustomerMemory) UpdateCustomer(c *customerentity.Customer) error {
	s.updatedStore = c.StoreID
	return nil
}
func (*scopedCustomerMemory) UpdateCustomerWithTx(*sql.Tx, *customerentity.Customer) error {
	return nil
}
func (*scopedCustomerMemory) GetCustomer(int) (*customerentity.Customer, error) {
	panic("unscoped customer lookup must not be used")
}
func (*scopedCustomerMemory) GetCustomerWithTx(*sql.Tx, int) (*customerentity.Customer, error) {
	panic("unscoped customer lookup must not be used")
}
func (*scopedCustomerMemory) GetCustomerByPhone(string) (*customerentity.Customer, error) {
	panic("unscoped customer lookup must not be used")
}
func (*scopedCustomerMemory) ListCustomers(storage.CustomerFilter) ([]*customerentity.CustomerSummary, int, error) {
	panic("not used")
}
func (s *scopedCustomerMemory) GetCustomerInStore(id, storeID int) (*customerentity.Customer, error) {
	s.requestedStore = storeID
	if storeID != 7 {
		return nil, storage.ErrCustomerIDNotFound
	}
	c := *s.customer
	c.ID = id
	return &c, nil
}
func (s *scopedCustomerMemory) GetCustomerWithTxInStore(_ *sql.Tx, id, storeID int) (*customerentity.Customer, error) {
	return s.GetCustomerInStore(id, storeID)
}
func (*scopedCustomerMemory) GetCustomerByPhoneInStore(string, int) (*customerentity.Customer, error) {
	return nil, errors.New("unused")
}
func (*scopedCustomerMemory) FindOrCreateCustomerWithTx(*sql.Tx, *customerentity.Customer) (bool, error) {
	return false, nil
}

func TestCustomerReadUsesAuthenticatedStoreAndHidesOtherStores(t *testing.T) {
	store := &scopedCustomerMemory{customer: &customerentity.Customer{FullName: "Local"}}
	h := &Handler{customerStore: store}
	req := httptest.NewRequest("GET", "/customers/12", nil)
	req.SetPathValue("id", "12")
	req = req.WithContext(traderservices.WithUser(req.Context(), &traderentity.Trader{Id: 3, StoreId: 7, Roles: "TRADER"}))
	response := httptest.NewRecorder()
	if err := h.HandleGetCustomer(response, req); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || store.requestedStore != 7 {
		t.Fatalf("status=%d store scope=%d", response.Code, store.requestedStore)
	}

	req = httptest.NewRequest("GET", "/customers/12", nil)
	req.SetPathValue("id", "12")
	req = req.WithContext(traderservices.WithUser(req.Context(), &traderentity.Trader{Id: 3, StoreId: 8, Roles: "TRADER"}))
	err := h.HandleGetCustomer(httptest.NewRecorder(), req)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 404 {
		t.Fatalf("foreign store lookup error = %v, want 404", err)
	}
}

func TestCustomerUpdateKeepsAuthenticatedStore(t *testing.T) {
	store := &scopedCustomerMemory{customer: &customerentity.Customer{FullName: "Local", StoreID: 7, IDType: "PASSPORT", IDNumber: "P-1", Phone: "+243810000001"}}
	h := &Handler{customerStore: store}
	req := httptest.NewRequest("PUT", "/customers/12", strings.NewReader(`{"full_name":"Updated","id_number":"P-2","id_type":"PASSPORT","phone":"+243810000002"}`))
	req.SetPathValue("id", "12")
	req = req.WithContext(traderservices.WithUser(req.Context(), &traderentity.Trader{Id: 3, StoreId: 7, Roles: "TRADER"}))
	if err := h.HandleUpdateCustomer(httptest.NewRecorder(), req); err != nil {
		t.Fatal(err)
	}
	if store.requestedStore != 7 || store.updatedStore != 7 {
		t.Fatalf("lookup store=%d update store=%d", store.requestedStore, store.updatedStore)
	}
}
