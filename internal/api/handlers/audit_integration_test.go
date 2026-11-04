package handlers

import (
	"database/sql"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	traderentity "fx-app-api/internal/domain/trader/entity"
	traderrepository "fx-app-api/internal/domain/trader/repository"
	traderservices "fx-app-api/internal/domain/trader/traderservices"
	"fx-app-api/internal/storage"

	auditrepository "fx-app-api/internal/domain/audit/repository"
)

type rejectingTransactionalAudit struct{ err error }

func (a rejectingTransactionalAudit) Record(int, string, string, int, any, any) error { return a.err }
func (a rejectingTransactionalAudit) RecordWithTx(*sql.Tx, int, string, string, int, any, any) error {
	return a.err
}

func TestPostgresTraderMutationAndAuditAreAtomic(t *testing.T) {
	if testing.Short() {
		t.Skip("PostgreSQL integration test")
	}
	if os.Getenv("FXAPP_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set FXAPP_POSTGRES_INTEGRATION=1 to run against the dedicated PostgreSQL test database")
	}

	// This test must only run against a dedicated test database; it initializes app tables.
	t.Setenv("APP_ENV", "development")
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_PORT", "55432")
	t.Setenv("DB_USER", "postgres")
	t.Setenv("DB_NAME", "fx_app_test")
	t.Setenv("DB_SSLMODE", "disable")
	t.Setenv("DATABASE_URL", "")

	store, err := storage.NewPostgresStore()
	if err != nil {
		t.Fatalf("connect to integration database: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	traderStore := traderrepository.NewTraderRepository(store)
	if err := traderStore.Init(); err != nil {
		t.Fatalf("initialize traders: %v", err)
	}
	suffix := time.Now().Format("150405.000000000")
	actorEmail := "integration-actor-" + suffix + "@example.test"
	if err := traderStore.CreateTrader(&traderentity.Trader{
		Email: actorEmail, Roles: "ADMIN", IsActive: true,
		Status: "ACTIVE", PasswordHash: "not-used-by-this-test",
	}); err != nil {
		t.Fatalf("create audit actor: %v", err)
	}
	audit := auditrepository.NewAuditRepository(store)
	if err := audit.Init(); err != nil {
		t.Fatalf("initialize audit log: %v", err)
	}

	actor, err := traderStore.GetTraderByEmail(actorEmail)
	if err != nil {
		t.Fatalf("load audit actor: %v", err)
	}

	newHandler := func(logger AuditLogger) *Handler {
		h := New(traderStore, nil, nil, nil, nil, nil)
		h.SetAuditLogger(logger)
		return h
	}
	invokeCreate := func(h *Handler, email string) error {
		req := httptest.NewRequest("POST", "/traders", strings.NewReader(`{"first_name":"Integration","last_name":"Trader","email":"`+email+`","phone":"+243800000000","password":"integration-password-123","role":"TRADER"}`))
		req = req.WithContext(traderservices.WithUser(req.Context(), actor))
		return h.HandleCreateTrader(httptest.NewRecorder(), req)
	}

	failedEmail := "rollback-" + suffix + "@example.test"
	if err := invokeCreate(newHandler(rejectingTransactionalAudit{err: errors.New("audit unavailable")}), failedEmail); err == nil {
		t.Fatal("expected audit failure")
	}
	var persisted int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM traders WHERE email=$1`, failedEmail).Scan(&persisted); err != nil {
		t.Fatalf("check rolled back trader: %v", err)
	}
	if persisted != 0 {
		t.Fatalf("trader persisted despite audit failure; count=%d", persisted)
	}

	successEmail := "commit-" + suffix + "@example.test"
	if err := invokeCreate(newHandler(audit), successEmail); err != nil {
		t.Fatalf("create trader with audit: %v", err)
	}
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM traders WHERE email=$1`, successEmail).Scan(&persisted); err != nil {
		t.Fatalf("check committed trader: %v", err)
	}
	if persisted != 1 {
		t.Fatalf("expected created trader, count=%d", persisted)
	}
	var auditCount int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='trader.create' AND after_data->>'email'=$1`, successEmail).Scan(&auditCount); err != nil {
		t.Fatalf("check committed audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("expected one matching audit entry, count=%d", auditCount)
	}
}
