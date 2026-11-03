package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"fx-app-api/internal/api/handlers"
	"fx-app-api/internal/api/middlewareInternal"
	traderentity "fx-app-api/internal/domain/trader/entity"
	"fx-app-api/internal/domain/trader/permissions"
	traderservices "fx-app-api/internal/domain/trader/traderservices"
	"github.com/go-chi/chi/v5"
)

func TestStoreRoutesRegistered(t *testing.T) {
	router := chi.NewRouter()
	registerProtectedRoutes(router, &handlers.Handler{})
	want := map[string]bool{
		"POST /stores":                          true,
		"GET /stores":                           true,
		"GET /stores/{id}":                      true,
		"PATCH /stores/{id}":                    true,
		"GET /stores/{id}/members":              true,
		"POST /stores/{id}/members":             true,
		"PATCH /stores/{id}/members/{traderId}": true,
	}
	if err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		delete(want, method+" "+route)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(want) != 0 {
		t.Fatalf("store routes missing: %v", want)
	}
}

func TestStorePermissionMatrix(t *testing.T) {
	cases := []struct {
		permission, role  string
		platform, allowed bool
	}{
		{permissions.PlatformManageStores, "", true, true},
		{permissions.PlatformManageStores, "TRADER", false, false},
		{permissions.StoreRead, "TRADER", false, true},
		{permissions.StoreRead, "COMPLIANCE_OFFICER", false, true},
		{permissions.StoreRead, "SUPERVISOR", false, true},
		{permissions.StoreManageMembers, "SUPERVISOR", false, true},
		{permissions.StoreManageMembers, "TRADER", false, false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if tc.role != "" || tc.platform {
			req = req.WithContext(traderservices.WithUser(req.Context(), &traderentity.Trader{Roles: tc.role, IsPlatformAdmin: tc.platform, Store: &traderentity.Store{ID: 8, Status: "ACTIVE"}, StoreId: 8}))
		}
		rr := httptest.NewRecorder()
		middlewareInternal.RequirePermission(tc.permission)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })).ServeHTTP(rr, req)
		want := http.StatusForbidden
		if tc.allowed {
			want = http.StatusOK
		}
		if tc.role == "" && !tc.platform {
			want = http.StatusUnauthorized
		}
		if rr.Code != want {
			t.Errorf("permission=%s role=%s platform=%v: got %d, want %d", tc.permission, tc.role, tc.platform, rr.Code, want)
		}
	}
}
