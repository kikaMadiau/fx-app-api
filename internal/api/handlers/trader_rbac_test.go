package handlers

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	traderentity "fx-app-api/internal/domain/trader/entity"
	"fx-app-api/internal/storage"
)

type rbacTraderStore struct {
	trader           *traderentity.Trader
	count            int
	updated, deleted bool
}

func (s *rbacTraderStore) Init() error                             { return nil }
func (s *rbacTraderStore) CreateTrader(*traderentity.Trader) error { return nil }
func (s *rbacTraderStore) GetTrader(int) (*traderentity.Trader, error) {
	if s.trader == nil {
		return nil, storage.ErrTraderNotFound
	}
	c := *s.trader
	return &c, nil
}
func (s *rbacTraderStore) GetTraderByEmail(string) (*traderentity.Trader, error) {
	return s.GetTrader(1)
}
func (s *rbacTraderStore) FindOrCreateTrader(t *traderentity.Trader) (*traderentity.Trader, error) {
	return t, nil
}
func (s *rbacTraderStore) UpdateTrader(*traderentity.Trader) error { s.updated = true; return nil }
func (s *rbacTraderStore) DeleteTrader(int) error                  { s.deleted = true; return nil }
func (s *rbacTraderStore) CountActiveAdmins() (int, error)         { return s.count, nil }

func TestLastAdminCannotBeDemotedOrDisabled(t *testing.T) {
	store := &rbacTraderStore{trader: &traderentity.Trader{Id: 1, Email: "admin@example.com", Roles: "ADMIN", IsActive: true}, count: 1}
	h := &Handler{traderStore: store}
	r := httptest.NewRequest("PUT", "/traders/1", strings.NewReader(`{"email":"admin@example.com","role":"TRADER","is_active":false}`))
	r.SetPathValue("id", "1")
	err := h.HandleUpdateTrader(httptest.NewRecorder(), r)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 409 {
		t.Fatalf("error = %v, want conflict", err)
	}
	if store.updated {
		t.Fatal("last administrator update was persisted")
	}
}
