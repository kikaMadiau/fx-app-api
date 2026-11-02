package middlewareInternal

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCorsMiddleware(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://front.example")
	nextCalled := false
	handler := CorsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { nextCalled = true; w.WriteHeader(http.StatusOK) }))

	t.Run("allowed origin echoes exact origin and varies", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://front.example")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if got := res.Header().Get("Access-Control-Allow-Origin"); got != "https://front.example" {
			t.Fatalf("allow origin = %q", got)
		}
		if got := res.Header().Get("Vary"); got != "Origin" {
			t.Fatalf("vary = %q", got)
		}
	})
	t.Run("unknown origin is not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://unknown.example")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if got := res.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("allow origin = %q", got)
		}
	})
	t.Run("patch preflight short circuits with 204", func(t *testing.T) {
		nextCalled = false
		req := httptest.NewRequest(http.MethodOptions, "/risk-flags/1/status", nil)
		req.Header.Set("Origin", "https://front.example")
		req.Header.Set("Access-Control-Request-Method", "PATCH")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusNoContent {
			t.Fatalf("status = %d", res.Code)
		}
		if nextCalled {
			t.Fatal("preflight reached the next handler")
		}
		if got := res.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST, PUT, PATCH, DELETE, OPTIONS" {
			t.Fatalf("methods = %q", got)
		}
	})
}
