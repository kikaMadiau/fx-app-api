package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"fx-app-api/internal/api"
	"fx-app-api/internal/api/handlers"
	"fx-app-api/internal/api/middlewareInternal"
	storeentity "fx-app-api/internal/domain/store/entity"
	traderentity "fx-app-api/internal/domain/trader/entity"
	traderrepository "fx-app-api/internal/domain/trader/repository"
	traderservices "fx-app-api/internal/domain/trader/traderservices"
	"fx-app-api/internal/storage"
)

func TestCreateStoreWithSupervisorPostgres(t *testing.T) {
	if testing.Short() || os.Getenv("FXAPP_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set FXAPP_POSTGRES_INTEGRATION=1 to use the dedicated local PostgreSQL test database")
	}
	t.Setenv("APP_ENV", "development")
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_PORT", "55432")
	if user := os.Getenv("FXAPP_POSTGRES_USER"); user != "" {
		t.Setenv("DB_USER", user)
	} else {
		t.Setenv("DB_USER", "postgres")
	}
	t.Setenv("DB_NAME", "fx_app_test")
	t.Setenv("DB_SSLMODE", "disable")
	t.Setenv("DATABASE_URL", "")
	pg, err := storage.NewPostgresStore()
	if err != nil {
		t.Fatalf("connect to dedicated integration database: %v", err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	setupCreateStoreSchema(t, pg)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	email := "cli-supervisor-" + suffix + "@example.test"
	license := "CLI-" + suffix
	t.Cleanup(func() { cleanupCreateStoreRows(t, pg.DB(), email, license) })

	store := &storeentity.Store{Name: "CLI Test Store", BCCLicenseNumber: license, Status: "ACTIVE"}
	password, err := generatedPassword()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := traderservices.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	supervisor := &traderentity.Trader{FirstName: "CLI", LastName: "Supervisor", Email: email, Phone: "+243800000000", Status: "ACTIVE", Roles: "SUPERVISOR", PasswordHash: hash, IsActive: true, MustChangePassword: true}
	if err := createStoreWithSupervisor(pg.DB(), store, supervisor); err != nil {
		t.Fatalf("create store: %v", err)
	}
	if supervisor.StoreId != store.ID {
		t.Fatalf("store id is not assigned on entity: %d", supervisor.StoreId)
	}
	var count int
	if err := pg.DB().QueryRow(`SELECT COUNT(*) FROM traders WHERE email=$1 AND store_id=$2 AND role='SUPERVISOR' AND roles=ARRAY['SUPERVISOR']::TEXT[] AND status='ACTIVE' AND is_active`, email, store.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one supervisor attached to store, got %d", count)
	}
	if err := pg.DB().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE store_id=$1 AND action='store.created' AND after->>'actor'='cli'`, store.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected cli store.created audit record, got %d", count)
	}

	if err := setupJWTKeys(t); err != nil {
		t.Fatal(err)
	}
	traders := traderrepository.NewTraderRepository(pg)
	loaded, err := traders.GetTraderByEmail(email)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.StoreId != store.ID || loaded.Store == nil || loaded.Store.ID != store.ID {
		t.Fatalf("trader store not loaded: %+v", loaded)
	}
	login := handlers.New(traders, nil, nil, nil, nil, nil)
	loginRequest := httptest.NewRequest(http.MethodPost, "/auth/trader/login", strings.NewReader(fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)))
	loginResponse := httptest.NewRecorder()
	api.MakeHttpHandleFunc(login.HandleTraderLogin).ServeHTTP(loginResponse, loginRequest)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status %d: %s", loginResponse.Code, loginResponse.Body.String())
	}
	var loginBody struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(loginResponse.Body.Bytes(), &loginBody); err != nil || loginBody.AccessToken == "" {
		t.Fatalf("invalid login response: %v", err)
	}
	meHandler := middlewareInternal.AuthMiddleware(traders)(api.MakeHttpHandleFunc(login.HandleGetCurrentTrader))
	meRequest := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	meRequest.Header.Set("Authorization", "Bearer "+loginBody.AccessToken)
	meResponse := httptest.NewRecorder()
	meHandler.ServeHTTP(meResponse, meRequest)
	if meResponse.Code != http.StatusOK {
		t.Fatalf("GET /auth/me status %d: %s", meResponse.Code, meResponse.Body.String())
	}
	var meBody struct {
		StoreID int `json:"store_id"`
		Store   *struct {
			ID int `json:"id"`
		} `json:"store"`
	}
	if err := json.Unmarshal(meResponse.Body.Bytes(), &meBody); err != nil {
		t.Fatal(err)
	}
	if meBody.StoreID != store.ID || meBody.Store == nil || meBody.Store.ID != store.ID {
		t.Fatalf("GET /auth/me omitted store: %s", meResponse.Body.String())
	}

	duplicateLicenseStore := &storeentity.Store{Name: "Duplicate License", BCCLicenseNumber: license, Status: "ACTIVE"}
	duplicateLicenseTrader := &traderentity.Trader{FirstName: "Other", LastName: "Supervisor", Email: "other-" + suffix + "@example.test", Phone: "+243800000001", PasswordHash: hash}
	if err := createStoreWithSupervisor(pg.DB(), duplicateLicenseStore, duplicateLicenseTrader); err == nil || !strings.Contains(err.Error(), "agrément déjà utilisé") {
		t.Fatalf("expected duplicate license error, got %v", err)
	}
	if err := pg.DB().QueryRow(`SELECT COUNT(*) FROM stores WHERE name='Duplicate License' AND bcc_license_number=$1`, license).Scan(&count); err != nil || count != 0 {
		t.Fatalf("duplicate license left an orphan store: count=%d err=%v", count, err)
	}

	duplicateEmailStore := &storeentity.Store{Name: "Duplicate Email", BCCLicenseNumber: license + "-EMAIL", Status: "ACTIVE"}
	duplicateEmailTrader := &traderentity.Trader{FirstName: "Other", LastName: "Supervisor", Email: email, Phone: "+243800000002", PasswordHash: hash}
	if err := createStoreWithSupervisor(pg.DB(), duplicateEmailStore, duplicateEmailTrader); err == nil || !strings.Contains(err.Error(), "e-mail déjà utilisé") {
		t.Fatalf("expected duplicate email error, got %v", err)
	}
	if err := pg.DB().QueryRow(`SELECT COUNT(*) FROM stores WHERE bcc_license_number=$1`, duplicateEmailStore.BCCLicenseNumber).Scan(&count); err != nil || count != 0 {
		t.Fatalf("duplicate email left an orphan store: count=%d err=%v", count, err)
	}
}

func setupCreateStoreSchema(t *testing.T, pg *storage.PostgresStore) {
	t.Helper()
	db := pg.DB()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS stores (id SERIAL PRIMARY KEY, name VARCHAR(255) NOT NULL, bcc_license_number VARCHAR(255) NOT NULL, license_issued_at TIMESTAMPTZ, license_expires_at TIMESTAMPTZ, status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE', created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, deleted_at TIMESTAMPTZ)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS stores_bcc_license_active_idx ON stores(bcc_license_number) WHERE deleted_at IS NULL`); err != nil {
		t.Fatal(err)
	}
	traders := traderrepository.NewTraderRepository(pg)
	if err := traders.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE traders ADD COLUMN IF NOT EXISTS store_id INT REFERENCES stores(id), ADD COLUMN IF NOT EXISTS roles TEXT[] NOT NULL DEFAULT ARRAY['TRADER']::TEXT[], ADD COLUMN IF NOT EXISTS is_platform_admin BOOLEAN NOT NULL DEFAULT FALSE, ADD COLUMN IF NOT EXISTS must_change_password BOOLEAN NOT NULL DEFAULT FALSE`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS audit_log (id BIGSERIAL PRIMARY KEY, store_id INT REFERENCES stores(id), actor_id INT REFERENCES traders(id), action TEXT NOT NULL, entity TEXT NOT NULL, entity_id BIGINT, before JSONB, after JSONB, created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
}

func cleanupCreateStoreRows(t *testing.T, db *sql.DB, email, license string) {
	t.Helper()
	_, _ = db.Exec(`DELETE FROM audit_log WHERE store_id IN (SELECT id FROM stores WHERE bcc_license_number=$1 OR bcc_license_number=$1||'-EMAIL')`, license)
	_, _ = db.Exec(`DELETE FROM traders WHERE email=$1`, email)
	_, _ = db.Exec(`DELETE FROM stores WHERE bcc_license_number=$1 OR bcc_license_number=$1||'-EMAIL'`, license)
}

func setupJWTKeys(t *testing.T) error {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	publicBytes, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return err
	}
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicBytes})
	t.Setenv("JWT_PRIVATE_KEY", jwtBase64(privatePEM))
	t.Setenv("JWT_PUBLIC_KEY", jwtBase64(publicPEM))
	return nil
}

func jwtBase64(data []byte) string { return base64.StdEncoding.EncodeToString(data) }
