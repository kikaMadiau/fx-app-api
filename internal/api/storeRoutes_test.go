package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"fx-app-api/internal/api/middlewareInternal"
	traderentity "fx-app-api/internal/domain/trader/entity"
	"fx-app-api/internal/domain/trader/permissions"
	traderservices "fx-app-api/internal/domain/trader/traderservices"
)

// The full role matrix is checked in TestProtectedRoutePermissionMatrix. Keep
// this test for the platform-admin account shape, whose Roles field can be empty.
func TestPlatformAdminStorePermission(t *testing.T) {
	cases := []struct {
		platform bool
		want     int
	}{
		{platform: true, want: http.StatusOK},
		{platform: false, want: http.StatusUnauthorized},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/stores", nil)
		if tc.platform {
			req = req.WithContext(traderservices.WithUser(req.Context(), &traderentity.Trader{
				IsPlatformAdmin: true,
				Store:           &traderentity.Store{ID: 8, Status: "ACTIVE"},
				StoreId:         8,
			}))
		}
		response := httptest.NewRecorder()
		handler := middlewareInternal.RequirePermission(permissions.PlatformManageStores)(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
		)
		handler.ServeHTTP(response, req)
		if response.Code != tc.want {
			t.Errorf("platform=%v: got %d, want %d", tc.platform, response.Code, tc.want)
		}
	}
}
