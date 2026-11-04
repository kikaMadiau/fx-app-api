package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	customerentity "fx-app-api/internal/domain/customer/entity"
	customerrepository "fx-app-api/internal/domain/customer/repository"
	customerservice "fx-app-api/internal/domain/customer/service"
	riskentity "fx-app-api/internal/domain/riskflag/entity"
	riskrepository "fx-app-api/internal/domain/riskflag/repository"
	traderrepository "fx-app-api/internal/domain/trader/repository"
	transactionentity "fx-app-api/internal/domain/transaction/entity"
	transactionrepository "fx-app-api/internal/domain/transaction/repository"
	"fx-app-api/internal/storage"
)

func TestPostgresSharedPersonAndStoreIsolation(t *testing.T) {
	if testing.Short() || os.Getenv("FXAPP_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set FXAPP_POSTGRES_INTEGRATION=1 to use the dedicated local PostgreSQL test database")
	}
	t.Setenv("APP_ENV", "development")
	t.Setenv("DB_HOST", "127.0.0.1")
	t.Setenv("DB_PORT", "55432")
	dbUser := os.Getenv("FXAPP_POSTGRES_USER")
	if dbUser == "" {
		dbUser = "postgres"
	}
	t.Setenv("DB_USER", dbUser)
	t.Setenv("DB_NAME", "fx_app_tenant_test")
	t.Setenv("DB_SSLMODE", "disable")
	t.Setenv("DATABASE_URL", "")

	pg, err := storage.NewPostgresStore()
	if err != nil {
		t.Fatalf("connect to disposable PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	// This flag opts into a database dedicated to this test; reset only its public schema.
	if _, err := pg.DB().Exec(`DROP SCHEMA public CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.DB().Exec(`CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}

	traders := traderrepository.NewTraderRepository(pg)
	customers := customerrepository.NewCustomerRepository(pg)
	kyc := customerrepository.NewKYCProfileRepository(pg)
	riskFlags := riskrepository.NewRiskFlagRepository(pg)
	transactions := transactionrepository.NewTransactionRepository(pg)
	for _, init := range []func() error{traders.Init, customers.Init, kyc.Init, riskFlags.Init, transactions.Init} {
		if err := init(); err != nil {
			t.Fatalf("initialize repositories: %v", err)
		}
	}
	if err := storage.InitTenantSchema(pg.DB()); err != nil {
		t.Fatalf("tenant migration: %v", err)
	}

	storeIDs := make([]int, 4)
	traderIDs := make([]int, 3)
	for i := range storeIDs {
		if err := pg.DB().QueryRow(`INSERT INTO stores(name,bcc_license_number) VALUES($1,$2) RETURNING id`, fmt.Sprintf("Store %d", i), fmt.Sprintf("TEST-LIC-%d", i)).Scan(&storeIDs[i]); err != nil {
			t.Fatal(err)
		}
		if i == 3 {
			continue
		}
		err := pg.DB().QueryRow(`INSERT INTO traders(first_name,last_name,email,status,role,roles,password_hash,store_id,is_active) VALUES('Test','Trader',$1,'ACTIVE','TRADER',ARRAY['TRADER']::TEXT[],'hash',$2,TRUE) RETURNING id`, fmt.Sprintf("tenant-%d@example.test", i), storeIDs[i]).Scan(&traderIDs[i])
		if err != nil {
			t.Fatal(err)
		}
	}

	service := customerservice.NewCustomerServiceWithKYC(customers, kyc)
	customerIDs := make([]int, 3)
	txIDs := make([]int, 3)
	for i := range customerIDs {
		personName := "Shared Person"
		if i > 0 {
			personName = fmt.Sprintf("Different submitted name %d", i)
		}
		dob := time.Date(1988, time.January, 12, 0, 0, 0, 0, time.UTC)
		if i > 0 {
			dob = time.Date(2000, time.March, 4, 0, 0, 0, 0, time.UTC)
		}
		person := &customerentity.Customer{StoreID: storeIDs[i], FullName: personName, IDType: "PASSPORT", IDNumber: "P-TEST-001", Phone: "+243810000001", DateOfBirth: &dob}
		if i == 2 {
			transaction := &transactionentity.Transaction{Amount: 25, Currency: "USD", TargetCurrency: "CDF", Type: "BUY", Rate: 2800, ConvertedAmount: 70000, Status: "COMPLETED", TraderID: traderIDs[i], StoreID: storeIDs[i]}
			dbTx, err := transactions.CreateTransactionWithNewCustomer(transaction, person)
			if err != nil {
				t.Fatalf("create customer and transaction in store C: %v", err)
			}
			if err := dbTx.Commit(); err != nil {
				t.Fatal(err)
			}
			customerIDs[i], txIDs[i] = person.ID, transaction.ID
			if person.FullName != "Shared Person" {
				t.Fatalf("transaction identity was not prefilled: %q", person.FullName)
			}
			continue
		}
		profile := &customerservice.CustomerKYCProfile{LegalNature: customerservice.Individual, ActivityProfile: customerservice.Personal, KYCStatus: customerservice.Unverified}
		saved, created, err := service.CreateCustomerWithKYC(person, profile)
		if err != nil || !created {
			t.Fatalf("create store-local customer %d: created=%v err=%v", i, created, err)
		}
		if saved.FullName != "Shared Person" {
			t.Fatalf("store %d identity was not prefilled: %q", i, saved.FullName)
		}
		if saved.DateOfBirth == nil || !saved.DateOfBirth.Equal(time.Date(1988, time.January, 12, 0, 0, 0, 0, time.UTC)) {
			t.Fatalf("store %d date of birth was not prefilled: %v", i, saved.DateOfBirth)
		}
		customerIDs[i] = saved.ID
	}
	var personCount, dossierCount int
	if err := pg.DB().QueryRow(`SELECT COUNT(*) FROM persons WHERE id_type='PASSPORT' AND id_number='P-TEST-001'`).Scan(&personCount); err != nil {
		t.Fatal(err)
	}
	if err := pg.DB().QueryRow(`SELECT COUNT(*) FROM customers WHERE id_type='PASSPORT' AND id_number='P-TEST-001' AND store_id IN ($1,$2,$3)`, storeIDs[0], storeIDs[1], storeIDs[2]).Scan(&dossierCount); err != nil {
		t.Fatal(err)
	}
	if personCount != 1 || dossierCount != 3 {
		t.Fatalf("persons=%d customers=%d; want 1 and 3", personCount, dossierCount)
	}

	if _, err := pg.DB().Exec(`UPDATE customer_kyc_profiles SET kyc_status='VERIFIED',source_of_funds='PRIVATE',expected_volume=900 WHERE customer_id=$1 AND store_id=$2`, customerIDs[0], storeIDs[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.DB().Exec(`UPDATE customers SET risk_score=99,risk_level='CRITICAL' WHERE id=$1 AND store_id=$2`, customerIDs[0], storeIDs[0]); err != nil {
		t.Fatal(err)
	}
	for i := range customerIDs {
		listed, count, err := customers.ListCustomers(storage.CustomerFilter{StoreID: storeIDs[i]})
		if err != nil || count != 1 || len(listed) != 1 || listed[0].ID != customerIDs[i] {
			t.Fatalf("store %d customer list=%v total=%d err=%v", i, listed, count, err)
		}
		profile, err := service.GetKYCProfileInStoreWithTx(nil, customerIDs[i], storeIDs[i])
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if profile.KYCStatus != customerservice.Verified {
				t.Fatalf("store A KYC status=%s", profile.KYCStatus)
			}
		} else if profile.KYCStatus != customerservice.Unverified || profile.SourceOfFunds != "" || profile.ExpectedVolume != 0 {
			t.Fatalf("store %d inherited KYC: %+v", i, profile)
		}
		customer, err := customers.(storage.ScopedCustomerStorage).GetCustomerInStore(customerIDs[i], storeIDs[i])
		if err != nil {
			t.Fatal(err)
		}
		if i > 0 && (customer.RiskScore != 0 || customer.RiskLevel == "CRITICAL") {
			t.Fatalf("store %d inherited risk: %+v", i, customer)
		}
	}
	if _, err := service.GetKYCProfileInStoreWithTx(nil, customerIDs[0], storeIDs[1]); !errors.Is(err, customerservice.ErrKYCProfileNotFound) {
		t.Fatalf("foreign KYC read error=%v", err)
	}
	foreignProfile, err := service.GetKYCProfileInStoreWithTx(nil, customerIDs[0], storeIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	foreignProfile.StoreID = storeIDs[1]
	if err := kyc.(interface {
		UpdateKYCProfileWithTx(*sql.Tx, *customerservice.CustomerKYCProfile) error
	}).UpdateKYCProfileWithTx(nil, foreignProfile); !errors.Is(err, customerservice.ErrKYCProfileNotFound) {
		t.Fatalf("foreign KYC update error=%v", err)
	}

	for i := 0; i < 2; i++ {
		tx := &transactionentity.Transaction{Amount: 25, Currency: "USD", TargetCurrency: "CDF", Type: "BUY", Rate: 2800, ConvertedAmount: 70000, Status: "COMPLETED", TraderID: traderIDs[i], CustomerID: customerIDs[i], StoreID: storeIDs[i]}
		if err := transactions.CreateTransactionWithTx(nil, tx); err != nil {
			t.Fatalf("create transaction %d: %v", i, err)
		}
		txIDs[i] = tx.ID
	}
	flag := &riskentity.RiskFlag{StoreID: storeIDs[0], Flag: "TEST_FLAG", Reason: "integration", Level: "HIGH", Score: 70, TransactionId: &txIDs[0], TraderId: traderIDs[0], CustomerId: customerIDs[0]}
	if err := riskFlags.CreateRiskFlag(flag); err != nil {
		t.Fatal(err)
	}
	assigned := traderIDs[0]
	if _, err := riskFlags.(storage.ScopedRiskFlagStorage).UpdateRiskFlagStatusInStore(nil, flag.ID, storeIDs[0], string(riskentity.RiskFlagStatusInReview), traderIDs[0], "", &assigned); err != nil {
		t.Fatal(err)
	}
	if _, err := transactions.(storage.ScopedTransactionStorage).GetTransactionInStore(txIDs[0], storeIDs[1]); !errors.Is(err, storage.ErrTransactionNotFound) {
		t.Fatalf("foreign transaction read error=%v", err)
	}
	if _, err := customers.(storage.ScopedCustomerStorage).GetCustomerInStore(customerIDs[0], storeIDs[1]); !errors.Is(err, storage.ErrCustomerIDNotFound) {
		t.Fatalf("foreign customer read error=%v", err)
	}
	if _, err := riskFlags.(storage.ScopedRiskFlagStorage).GetRiskFlagInStore(flag.ID, storeIDs[1]); !errors.Is(err, storage.ErrRiskFlagNotFound) {
		t.Fatalf("foreign alert read error=%v", err)
	}
	if err := riskFlags.(storage.ScopedRiskFlagStorage).DeleteRiskFlagInStore(flag.ID, storeIDs[1]); !errors.Is(err, storage.ErrRiskFlagNotFound) {
		t.Fatalf("foreign alert delete error=%v", err)
	}
	if _, err := riskFlags.(storage.ScopedRiskFlagStorage).UpdateRiskFlagStatusInStore(nil, flag.ID, storeIDs[1], string(riskentity.RiskFlagStatusResolved), traderIDs[1], "not mine", &assigned); !errors.Is(err, storage.ErrRiskFlagNotFound) {
		t.Fatalf("foreign alert status update error=%v", err)
	}
	foreignCustomer := &customerentity.Customer{ID: customerIDs[0], StoreID: storeIDs[1], FullName: "Tampered", IDType: "PASSPORT", IDNumber: "P-TEST-001", Phone: "+243810000001"}
	if err := customers.UpdateCustomer(foreignCustomer); !errors.Is(err, storage.ErrCustomerIDNotFound) {
		t.Fatalf("foreign customer update error=%v", err)
	}
	foreignTx := &transactionentity.Transaction{ID: txIDs[0], StoreID: storeIDs[1], CustomerID: customerIDs[1], TraderID: traderIDs[1], Amount: 30, Currency: "USD", TargetCurrency: "CDF", Type: "BUY", Rate: 2800, ConvertedAmount: 84000, Status: "COMPLETED"}
	if err := transactions.UpdateTransaction(foreignTx); !errors.Is(err, storage.ErrTransactionNotFound) {
		t.Fatalf("foreign transaction update error=%v", err)
	}
	history, err := riskFlags.(storage.ScopedRiskFlagStorage).ListRiskFlagStatusHistoryInStore(flag.ID, storeIDs[1])
	if err != nil || len(history) != 0 {
		t.Fatalf("foreign alert history=%v err=%v", history, err)
	}
	for i := 1; i < 3; i++ {
		items, total, err := riskFlags.ListRiskFlagsWithDetails(storage.RiskFlagFilter{StoreID: storeIDs[i]})
		if err != nil || total != 0 || len(items) != 0 {
			t.Fatalf("store %d saw another store's alerts: total=%d err=%v", i, total, err)
		}
		listed, count, err := transactions.SearchTransactions(storage.TransactionFilter{StoreID: storeIDs[i]})
		if err != nil || count != 1 || len(listed) != 1 || listed[0].ID != txIDs[i] {
			t.Fatalf("store %d transactions=%v total=%d err=%v", i, listed, count, err)
		}
	}

	conflictTx, err := pg.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	conflict := &customerentity.Customer{StoreID: storeIDs[3], FullName: "Different Person", IDType: "PASSPORT", IDNumber: "P-OTHER", Phone: "+243810000001"}
	_, conflictErr := customers.(storage.ScopedCustomerStorage).FindOrCreateCustomerWithTx(conflictTx, conflict)
	_ = conflictTx.Rollback()
	if !errors.Is(conflictErr, storage.ErrIdentityConflict) {
		t.Fatalf("phone/document conflict=%v, want identity_conflict", conflictErr)
	}

	var personID int
	if err := pg.DB().QueryRow(`SELECT id FROM persons WHERE id_type='PASSPORT' AND id_number='P-TEST-001'`).Scan(&personID); err != nil {
		t.Fatal(err)
	}
	_, err = pg.DB().Exec(`INSERT INTO customers(person_id,full_name,id_number,id_type,phone) VALUES($1,'No Store','NO-STORE','PASSPORT','+243899999999')`, personID)
	if err == nil {
		t.Fatal("customer insertion without store_id unexpectedly succeeded")
	}
	if err := transactions.CreateTransactionWithTx(nil, &transactionentity.Transaction{CustomerID: customerIDs[0], TraderID: traderIDs[0]}); err == nil {
		t.Fatal("transaction insertion without store_id unexpectedly succeeded")
	}
	if err := kyc.CreateKYCProfile(&customerservice.CustomerKYCProfile{CustomerID: customerIDs[0], LegalNature: customerservice.Individual, ActivityProfile: customerservice.Personal, KYCStatus: customerservice.Unverified}); err == nil {
		t.Fatal("KYC insertion without store_id unexpectedly succeeded")
	}
	if err := riskFlags.CreateRiskFlag(&riskentity.RiskFlag{Flag: "NO_STORE", CustomerId: customerIDs[0]}); err == nil {
		t.Fatal("risk flag insertion without store_id unexpectedly succeeded")
	}
	for _, table := range []string{"customers", "transactions", "risk_flags", "risk_flag_status_history", "customer_kyc_profiles"} {
		var defaultValue sql.NullString
		if err := pg.DB().QueryRow(`SELECT column_default FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_name='store_id'`, table).Scan(&defaultValue); err != nil {
			t.Fatal(err)
		}
		if defaultValue.Valid {
			t.Fatalf("%s.store_id has forbidden default %q", table, defaultValue.String)
		}
	}
	if err := storage.InitTenantSchema(pg.DB()); err != nil {
		t.Fatalf("second tenant migration run: %v", err)
	}
	if err := pg.DB().QueryRow(`SELECT COUNT(*) FROM persons WHERE id_type='PASSPORT' AND id_number='P-TEST-001'`).Scan(&personCount); err != nil {
		t.Fatal(err)
	}
	if personCount != 1 {
		t.Fatalf("idempotent migration produced %d persons", personCount)
	}

}
