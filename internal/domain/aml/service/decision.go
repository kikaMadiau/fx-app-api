package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	amlengine "fx-app-api/internal/domain/aml/engine"
	amlrepository "fx-app-api/internal/domain/aml/repository"
	customerentity "fx-app-api/internal/domain/customer/entity"
	"fx-app-api/internal/domain/customer/kycrisk"
	customerservice "fx-app-api/internal/domain/customer/service"
	riskflagentity "fx-app-api/internal/domain/riskflag/entity"
	transactionentity "fx-app-api/internal/domain/transaction/entity"
	"fx-app-api/internal/storage"
)

const amlEngineVersion = "aml-engine-v1"

var ErrAMLIdempotencyReplay = errors.New("AML idempotency key already has a decision")
var ErrInvalidAMLAmount = errors.New("AML operation amount must be represented in minor units")

type AMLPreparedDecision struct {
	PersonID          int64
	Key               string
	Decision          amlengine.Decision
	Input             amlengine.EvalContext
	ReplayOperationID int64
	ReplayOutcome     amlengine.DecisionOutcome
}

type DecisionService struct {
	customerRepo    storage.CustomerStorage
	customerService *customerservice.CustomerService
	transactionRepo storage.TransactionStorage
	riskFlagRepo    storage.RiskFlagStorage
	signalProvider  interface {
		AMLSignals(*sql.Tx, int, int) *kycrisk.Signals
	}
	clock       func() time.Time
	legacyOnce  sync.Once
	legacyRules []amlengine.Rule
	legacyErr   error
}

func NewDecisionService(customers storage.CustomerStorage, transactions storage.TransactionStorage, flags storage.RiskFlagStorage, customerSvc *customerservice.CustomerService, signals interface {
	AMLSignals(*sql.Tx, int, int) *kycrisk.Signals
}) *DecisionService {
	return &DecisionService{customerRepo: customers, transactionRepo: transactions, riskFlagRepo: flags, customerService: customerSvc, signalProvider: signals, clock: time.Now}
}

func (s *DecisionService) SetClock(clock func() time.Time) {
	if clock != nil {
		s.clock = clock
	}
}

type postgresStoreProvider interface{ Store() *storage.PostgresStore }

// DecideAMLWithTx evaluates the current effective rule catalogue before the transaction INSERT.
// Lock and idempotency lookup use the caller's transaction so the lock remains held through commit.
func (s *DecisionService) DecideAMLWithTx(ctx context.Context, dbTx *sql.Tx, tx *transactionentity.Transaction, key string) (*AMLPreparedDecision, error) {
	if ctx == nil || dbTx == nil || tx == nil || strings.TrimSpace(key) == "" {
		return nil, errors.New("AML decision requires context, SQL transaction, operation, and idempotency key")
	}
	provider, ok := s.transactionRepo.(postgresStoreProvider)
	if !ok || provider.Store() == nil {
		return nil, errors.New("AML decision requires a PostgreSQL transaction repository")
	}
	db := provider.Store().DB()
	var personID int64
	if err := dbTx.QueryRowContext(ctx, `SELECT person_id FROM customers WHERE id=$1`, tx.CustomerID).Scan(&personID); err != nil {
		return nil, fmt.Errorf("resolve AML person for customer %d: %w", tx.CustomerID, err)
	}
	decisionRepo := amlrepository.NewDecisionRepository(db)
	if err := decisionRepo.LockIdempotencyKey(ctx, dbTx, key); err != nil {
		return nil, err
	}
	if err := decisionRepo.LockPerson(ctx, dbTx, personID); err != nil {
		return nil, err
	}
	if _, operationID, storedPersonID, outcome, snapshot, found, err := decisionRepo.FindSubmission(ctx, dbTx, key); err != nil {
		return nil, err
	} else if found {
		same, compareErr := sameIdempotentOperation(snapshot, tx, personID)
		if compareErr != nil {
			return nil, compareErr
		}
		if storedPersonID != personID || !same {
			return nil, ErrAMLIdempotencyReplay
		}
		return &AMLPreparedDecision{PersonID: personID, Key: key, ReplayOperationID: operationID, ReplayOutcome: outcome}, ErrAMLIdempotencyReplay
	}

	var storeID int64
	if err := dbTx.QueryRowContext(ctx, `SELECT store_id FROM customers WHERE id=$1`, tx.CustomerID).Scan(&storeID); err != nil {
		return nil, fmt.Errorf("resolve AML store: %w", err)
	}
	rules, err := amlrepository.NewRuleRepository(db).LoadDecisionRulesWithTx(ctx, dbTx, &storeID)
	if err != nil {
		return nil, err
	}
	registry := amlengine.NewRegistry()
	legacyRules, err := s.loadLegacyRules()
	if err != nil {
		return nil, err
	}
	if err := amlengine.RegisterDecisionRules(registry, rules, legacyRules); err != nil {
		return nil, err
	}
	input, err := s.buildAMLEvalContext(ctx, dbTx, tx, personID, key)
	if err != nil {
		return nil, err
	}
	decision, err := amlengine.New(registry).Decide(ctx, input)
	if err != nil {
		return nil, err
	}
	return &AMLPreparedDecision{PersonID: personID, Key: key, Decision: decision, Input: input}, nil
}

func sameIdempotentOperation(snapshot []byte, tx *transactionentity.Transaction, personID int64) (bool, error) {
	var stored struct {
		Operation amlengine.Operation `json:"operation"`
	}
	if err := json.Unmarshal(snapshot, &stored); err != nil {
		return false, fmt.Errorf("decode prior AML idempotency snapshot: %w", err)
	}
	amount, err := exactMinor(tx.Amount)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrInvalidAMLAmount, err)
	}
	op := stored.Operation
	return op.PersonID == personID && op.OperatorID == int64(tx.TraderID) && op.StoreID == int64(tx.StoreID) && op.AmountMinor == amount && op.Currency == strings.ToUpper(tx.Currency) && op.TargetCurrency == strings.ToUpper(tx.TargetCurrency) && op.Type == tx.Type, nil
}

func (s *DecisionService) loadLegacyRules() ([]amlengine.Rule, error) {
	s.legacyOnce.Do(func() {
		paths := []string{}
		if path := strings.TrimSpace(os.Getenv("RISK_RULES_CONFIG_PATH")); path != "" {
			paths = append(paths, path)
		} else {
			paths = []string{"config/risk_rules.json", "config/riskRules.json"}
		}
		var data []byte
		var readErr error
		for _, path := range paths {
			data, readErr = os.ReadFile(path)
			if readErr == nil {
				break
			}
		}
		if readErr != nil {
			s.legacyErr = fmt.Errorf("read AML legacy rule definitions: %w", readErr)
			return
		}
		s.legacyRules, s.legacyErr = amlengine.BuildLegacyRules(data)
	})
	if s.legacyErr != nil {
		return nil, s.legacyErr
	}
	return append([]amlengine.Rule(nil), s.legacyRules...), nil
}

func (s *DecisionService) PersistAMLDecisionWithTx(ctx context.Context, dbTx *sql.Tx, tx *transactionentity.Transaction, prepared *AMLPreparedDecision) error {
	if prepared == nil || dbTx == nil || tx == nil {
		return errors.New("persist AML decision requires prepared decision, transaction, and SQL transaction")
	}
	var operationID *int64
	if tx.ID > 0 {
		id := int64(tx.ID)
		operationID = &id
		prepared.Input.Operation.ID = id
	}
	if prepared.Decision.Outcome == amlengine.DecisionAcceptWithApproval {
		tx.Status = transactionentity.StatusPending
		prepared.Input.Operation.Status = tx.Status
	}
	repo := amlrepository.NewDecisionRepository(nil)
	if provider, ok := s.transactionRepo.(postgresStoreProvider); ok && provider.Store() != nil {
		repo = amlrepository.NewDecisionRepository(provider.Store().DB())
	}
	decisionID, err := repo.CreateWithTx(ctx, dbTx, prepared.PersonID, operationID, prepared.Key, amlEngineVersion, prepared.Input, prepared.Decision)
	if err != nil {
		return err
	}
	if operationID != nil {
		if err := amlrepository.NewOutboxRepository().CreateWithTx(ctx, dbTx, "operation.created", "transaction", *operationID, map[string]any{"transaction_id": *operationID, "decision_id": decisionID, "decision": prepared.Decision.Outcome}); err != nil {
			return err
		}
	}
	if operationID != nil && prepared.Input.Operation.GrossUSDMinor > 0 {
		day := prepared.Input.Operation.OccurredAt.In(prepared.Input.ReferenceTimezone).Format("2006-01-02")
		if _, err := dbTx.ExecContext(ctx, `INSERT INTO aml_person_daily(person_id,reference_day,total_usd_minor,operation_count,last_operation_at) VALUES($1,$2,$3,1,$4) ON CONFLICT(person_id,reference_day) DO UPDATE SET total_usd_minor=aml_person_daily.total_usd_minor+EXCLUDED.total_usd_minor, operation_count=aml_person_daily.operation_count+1, last_operation_at=GREATEST(aml_person_daily.last_operation_at,EXCLUDED.last_operation_at), updated_at=CURRENT_TIMESTAMP`, prepared.PersonID, day, prepared.Input.Operation.GrossUSDMinor, prepared.Input.Operation.OccurredAt); err != nil {
			return fmt.Errorf("update AML person daily aggregate: %w", err)
		}
	}
	for _, result := range prepared.Decision.Results {
		if !amlengine.IsParameterizedRule(result.Code) || !result.Evaluated || result.Action == "SCORE_ONLY" || result.Result.Outcome == amlengine.OutcomePass {
			continue
		}
		reason := strings.Join(result.Result.Reasons, "; ")
		if reason == "" {
			reason = "AML rule produced " + string(result.Result.Outcome)
		}
		level := result.Severity
		if level == "" || strings.EqualFold(level, "UNSPECIFIED") {
			level = "MEDIUM"
		}
		var transactionID *int
		if tx.ID > 0 {
			id := tx.ID
			transactionID = &id
		}
		flag := &riskflagentity.RiskFlag{Flag: result.Code, Reason: reason, Score: float64(result.Result.Score), Level: level, TransactionId: transactionID, TraderId: tx.TraderID, CustomerId: tx.CustomerID, StoreID: tx.StoreID, Status: string(riskflagentity.RiskFlagStatusOpen)}
		if err := s.riskFlagRepo.CreateRiskFlagWithTx(dbTx, flag); err != nil {
			return fmt.Errorf("persist AML risk flag %q: %w", result.Code, err)
		}
		if err := amlrepository.NewOutboxRepository().CreateWithTx(ctx, dbTx, "alert.created", "risk_flag", int64(flag.ID), map[string]any{"risk_flag": flag, "decision_id": decisionID}); err != nil {
			return err
		}
	}
	return nil
}

func (s *DecisionService) buildAMLEvalContext(ctx context.Context, tx *sql.Tx, operation *transactionentity.Transaction, personID int64, key string) (amlengine.EvalContext, error) {
	customer, err := s.customerRepo.GetCustomerWithTx(tx, operation.CustomerID)
	if err != nil {
		return amlengine.EvalContext{}, fmt.Errorf("load AML customer: %w", err)
	}
	var profile *customerservice.CustomerKYCProfile
	if s.customerService != nil {
		profile, err = s.customerService.GetKYCProfileWithTx(tx, customer.ID)
		if err != nil && !errors.Is(err, customerservice.ErrKYCProfileNotFound) {
			return amlengine.EvalContext{}, fmt.Errorf("load AML KYC profile: %w", err)
		}
	}
	now := s.clock().UTC()
	if operation.CreatedAt.IsZero() {
		operation.CreatedAt = now
	}
	amountMinor, err := exactMinor(operation.Amount)
	if err != nil {
		return amlengine.EvalContext{}, fmt.Errorf("%w: %v", ErrInvalidAMLAmount, err)
	}
	grossUSD, err := usdEquivalentMinor(ctx, tx, amountMinor, operation.Currency, operation.CreatedAt, kycrisk.Location())
	if err != nil {
		return amlengine.EvalContext{}, err
	}
	ratePPM, err := decimalScaled(operation.Rate, 1_000_000)
	if err != nil {
		return amlengine.EvalContext{}, fmt.Errorf("operation rate: %w", err)
	}
	var profileExpected int64
	if profile != nil {
		profileExpected, err = exactMinor(profile.ExpectedVolume)
		if err != nil {
			return amlengine.EvalContext{}, fmt.Errorf("expected KYC volume: %w", err)
		}
	}
	var operatorStatus, storeStatus string
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(status,''), is_active FROM traders WHERE id=$1 AND deleted_at IS NULL`, operation.TraderID).Scan(&operatorStatus, &active); err != nil {
		return amlengine.EvalContext{}, fmt.Errorf("load AML operator status: %w", err)
	}
	if !active {
		operatorStatus = "INACTIVE"
	} else if strings.TrimSpace(operatorStatus) == "" {
		operatorStatus = "ACTIVE"
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(status,'') FROM stores WHERE id=$1 AND deleted_at IS NULL`, operation.StoreID).Scan(&storeStatus); err != nil {
		return amlengine.EvalContext{}, fmt.Errorf("load AML store status: %w", err)
	}
	var front, back bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM customer_id_documents WHERE customer_id=$1 AND side='front'), EXISTS(SELECT 1 FROM customer_id_documents WHERE customer_id=$1 AND side='back')`, customer.ID).Scan(&front, &back); err != nil {
		return amlengine.EvalContext{}, fmt.Errorf("load AML identity photo signals: %w", err)
	}
	identityValid := strings.TrimSpace(customer.IDNumber) != ""
	if profile != nil && profile.IDExpiryDate != "" && kycrisk.IDExpired(profile.IDExpiryDate, now) {
		identityValid = false
	}
	beneficialOwner := profile == nil || !profile.ActsForThirdParty || (strings.TrimSpace(profile.ThirdPartyName) != "" && strings.TrimSpace(profile.ThirdPartyIDNumber) != "")
	bccRatePPM, bccRateAt, err := bccPairRate(ctx, tx, operation.Currency, operation.TargetCurrency, operation.CreatedAt, kycrisk.Location())
	if err != nil {
		return amlengine.EvalContext{}, err
	}
	history, err := loadAMLHistory(ctx, tx, personID, kycrisk.Location())
	if err != nil {
		return amlengine.EvalContext{}, err
	}
	identityLinks, err := loadIdentityLinks(ctx, tx, customer, personID)
	if err != nil {
		return amlengine.EvalContext{}, err
	}
	beneficiaryKey := ""
	if profile != nil && profile.ActsForThirdParty {
		beneficiaryKey = strings.TrimSpace(profile.ThirdPartyIDNumber)
	}
	input := amlengine.EvalContext{
		Operation: amlengine.Operation{IdempotencyKey: key, PersonID: personID, OperatorID: int64(operation.TraderID), StoreID: int64(operation.StoreID), AmountMinor: amountMinor, Currency: strings.ToUpper(operation.Currency), Type: operation.Type, TargetCurrency: strings.ToUpper(operation.TargetCurrency), Status: operation.Status, OperationClass: operation.Type, GrossUSDMinor: grossUSD, NetUSDMinor: grossUSD, RatePPM: ratePPM, BeneficiaryKey: beneficiaryKey, OccurredAt: operation.CreatedAt.UTC()},
		Customer:  customer, KYCProfile: profile, ExpectedVolumeMinor: profileExpected,
		Identity: amlengine.IdentitySnapshot{CustomerIdentified: identityValid, BeneficialOwnerIdentified: beneficialOwner, IdentityDocumentPresented: strings.TrimSpace(customer.IDType) != "" && identityValid, IdentityDocumentValid: identityValid, AddressPresent: strings.TrimSpace(customer.Address) != "", PhotoPresent: front && back}, IdentityLinks: identityLinks,
		License: amlengine.LicenseSnapshot{OperatorStatus: operatorStatus, StoreStatus: storeStatus}, BCCRatePPM: bccRatePPM, BCCRateAt: bccRateAt,
		Signals: nil, History: history, Clock: amlengine.ClockFunc(func() time.Time { return now }), ReferenceTimezone: kycrisk.Location(),
	}
	if s.signalProvider != nil {
		input.Signals = s.signalProvider.AMLSignals(tx, customer.ID, 0)
	}
	return input, nil
}

func loadIdentityLinks(ctx context.Context, tx *sql.Tx, customer *customerentity.Customer, personID int64) (amlengine.IdentityLinkSignals, error) {
	var links amlengine.IdentityLinkSignals
	if strings.TrimSpace(customer.Phone) != "" {
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(DISTINCT person_id) FROM customers WHERE phone=$1 AND person_id<>$2 AND deleted_at IS NULL`, customer.Phone, personID).Scan(&links.PhonePersonCount); err != nil {
			return links, fmt.Errorf("load AML phone links: %w", err)
		}
	}
	if strings.TrimSpace(customer.Address) != "" {
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(DISTINCT person_id) FROM customers WHERE LOWER(TRIM(address))=LOWER(TRIM($1)) AND person_id<>$2 AND deleted_at IS NULL`, customer.Address, personID).Scan(&links.AddressPersonCount); err != nil {
			return links, fmt.Errorf("load AML address links: %w", err)
		}
	}
	return links, nil
}

func loadAMLHistory(ctx context.Context, tx *sql.Tx, personID int64, location *time.Location) ([]amlengine.HistoricalOperation, error) {
	rows, err := tx.QueryContext(ctx, `SELECT t.id, t.trader_id, COALESCE(t.store_id,0), ROUND(t.amount*100)::BIGINT, t.currency, COALESCE(t.target_currency,''), COALESCE(t.type,''), COALESCE(t.status,''), ROUND(COALESCE(t.converted_amount,0)*100)::BIGINT, t.created_at, COALESCE(c.phone,'') FROM transactions t JOIN customers c ON c.id=t.customer_id WHERE c.person_id=$1 AND t.deleted_at IS NULL AND t.created_at >= CURRENT_TIMESTAMP - INTERVAL '30 days' ORDER BY t.created_at`, personID)
	if err != nil {
		return nil, fmt.Errorf("load AML person history: %w", err)
	}
	defer rows.Close()
	history := make([]amlengine.HistoricalOperation, 0)
	for rows.Next() {
		var item amlengine.HistoricalOperation
		var amount, converted int64
		if err := rows.Scan(&item.ID, &item.OperatorID, &item.StoreID, &amount, &item.Currency, &item.TargetCurrency, &item.Type, &item.Status, &converted, &item.OccurredAt, &item.Phone); err != nil {
			return nil, fmt.Errorf("scan AML person history: %w", err)
		}
		item.AmountMinor = amount
		item.GrossUSDMinor, err = usdEquivalentMinor(ctx, tx, amount, item.Currency, item.OccurredAt, location)
		if err != nil {
			return nil, fmt.Errorf("convert historical operation %d to USD using BCC rate: %w", item.ID, err)
		}
		item.NetUSDMinor = item.GrossUSDMinor
		history = append(history, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read AML person history: %w", err)
	}
	return history, nil
}

func usdEquivalentMinor(ctx context.Context, tx *sql.Tx, amount int64, currency string, at time.Time, location *time.Location) (int64, error) {
	if strings.EqualFold(currency, "USD") {
		return amount, nil
	}
	// Never substitute the negotiated transaction rate for the official BCC quote.
	day := at.In(location)
	units, err := amlrepository.NewBCCRateRepository().UnitsPerUSD(ctx, tx, currency, day)
	if err != nil {
		if errors.Is(err, amlrepository.ErrBCCRateNotFound) {
			return 0, nil
		}
		return 0, fmt.Errorf("USD equivalent unavailable for %s: %w", currency, err)
	}
	return roundedQuotient(new(big.Int).Mul(big.NewInt(amount), big.NewInt(1_000_000)), big.NewInt(units)), nil
}

func bccPairRate(ctx context.Context, tx *sql.Tx, source, target string, at time.Time, location *time.Location) (int64, time.Time, error) {
	day := at.In(location)
	repo := amlrepository.NewBCCRateRepository()
	sourceUnits, sourceAt, err := repo.PublishedRate(ctx, tx, source, day)
	if err != nil {
		if errors.Is(err, amlrepository.ErrBCCRateNotFound) {
			return 0, time.Time{}, nil
		}
		return 0, time.Time{}, fmt.Errorf("load BCC reference for %s/%s: %w", source, target, err)
	}
	targetUnits, targetAt, err := repo.PublishedRate(ctx, tx, target, day)
	if err != nil {
		if errors.Is(err, amlrepository.ErrBCCRateNotFound) {
			return 0, time.Time{}, nil
		}
		return 0, time.Time{}, fmt.Errorf("load BCC reference for %s/%s: %w", source, target, err)
	}
	rate := roundedQuotient(new(big.Int).Mul(big.NewInt(targetUnits), big.NewInt(1_000_000)), big.NewInt(sourceUnits))
	if targetAt.After(sourceAt) {
		sourceAt = targetAt
	}
	return rate, sourceAt, nil
}

func roundedQuotient(numerator, denominator *big.Int) int64 {
	if denominator.Sign() <= 0 {
		return 0
	}
	q, r := new(big.Int).QuoRem(numerator, denominator, new(big.Int))
	if new(big.Int).Mul(r, big.NewInt(2)).Cmp(denominator) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0
	}
	return q.Int64()
}

func exactMinor(value float64) (int64, error) { return decimalScaled(value, 100) }
func decimalScaled(value float64, scale int64) (int64, error) {
	if value < 0 {
		return 0, errors.New("value cannot be negative")
	}
	raw := strconv.FormatFloat(value, 'f', -1, 64)
	rational, ok := new(big.Rat).SetString(raw)
	if !ok {
		return 0, errors.New("invalid decimal")
	}
	rational.Mul(rational, big.NewRat(scale, 1))
	if !rational.IsInt() || !rational.Num().IsInt64() {
		return 0, errors.New("value is not exactly representable at configured scale")
	}
	return rational.Num().Int64(), nil
}
