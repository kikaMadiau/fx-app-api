package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	customerentity "fx-app-api/internal/domain/customer/entity"
	"fx-app-api/internal/domain/customer/kycrisk"
	customerservice "fx-app-api/internal/domain/customer/service"
	riskflagservice "fx-app-api/internal/domain/riskflag/service"
	transactionentity "fx-app-api/internal/domain/transaction/entity"
)

func TestBuildLegacyRulesLoadsCurrentCatalog(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "config", "riskRules.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read legacy catalog %q: %v", path, err)
	}
	rules, err := BuildLegacyRules(data)
	if err != nil {
		t.Fatalf("BuildLegacyRules() error = %v", err)
	}
	if len(rules) != 22 {
		t.Fatalf("BuildLegacyRules() returned %d rules, want 22", len(rules))
	}
	byCode := make(map[string]Rule, len(rules))
	for _, rule := range rules {
		byCode[rule.Code()] = rule
	}
	if got := byCode["HIGH_TRANSACTION_AMOUNT"]; got == nil || got.Category() != CategoryDetection {
		t.Fatalf("high amount registration = %#v, want DETECTION", got)
	}
	if got := byCode["INSUFFICIENT_KYC_STATUS"]; got == nil || got.Category() != CategoryKYCLevel {
		t.Fatalf("KYC registration = %#v, want KYC_LEVEL", got)
	}
}

func TestLegacyRulePortMatchesRiskFlags(t *testing.T) {
	now := time.Now()
	location := kycrisk.Location()
	current := &transactionentity.Transaction{
		ID: 30, Amount: 9500, Currency: "USD", TargetCurrency: "CDF", Type: "SELL", CreatedAt: now,
	}

	transactionCases := []struct {
		name       string
		ruleType   string
		config     LegacyRuleConfig
		legacyRule riskflagservice.Rule
		current    *transactionentity.Transaction
		recent     []*transactionentity.Transaction
		signals    *kycrisk.Signals
	}{
		{
			name: "high amount", ruleType: "high_amount", config: LegacyRuleConfig{Code: "HIGH_AMOUNT", Score: 30, ThresholdMinor: 1_000_000},
			legacyRule: &riskflagservice.HighAmountRule{Config: riskflagservice.RuleConfig{Name: "HIGH_AMOUNT", Score: 30, ThresholdAmount: 10000}},
			current:    current,
		},
		{
			name: "cumulative volume 24h", ruleType: "cumulative_volume", config: LegacyRuleConfig{Code: "CUMULATIVE_24H", Score: 35, ThresholdMinor: 2_000_000, Window: 24 * time.Hour},
			legacyRule: &riskflagservice.CumulativeVolumeRule{Config: riskflagservice.RuleConfig{Name: "CUMULATIVE_24H", Score: 35, ThresholdAmount: 20000, Window: 24 * time.Hour}},
			current:    &transactionentity.Transaction{ID: 31, Amount: 12000, Currency: "USD", Type: "BUY", CreatedAt: now.Add(-3 * time.Hour)},
			recent: []*transactionentity.Transaction{
				{ID: 31, Amount: 12000, Currency: "USD", Type: "BUY", CreatedAt: now.Add(-3 * time.Hour)},
				{ID: 32, Amount: 9000, Currency: "USD", Type: "SELL", CreatedAt: now.Add(-2 * time.Hour)},
				{ID: 33, Amount: 50000, Currency: "USD", Type: "BUY", CreatedAt: now.Add(-48 * time.Hour)},
			},
		},
		{
			name: "cumulative volume 30d", ruleType: "cumulative_volume", config: LegacyRuleConfig{Code: "CUMULATIVE_30D", Score: 40, ThresholdMinor: 10_000_000, Window: 720 * time.Hour},
			legacyRule: &riskflagservice.CumulativeVolumeRule{Config: riskflagservice.RuleConfig{Name: "CUMULATIVE_30D", Score: 40, ThresholdAmount: 100000, Window: 720 * time.Hour}},
			current:    &transactionentity.Transaction{ID: 34, Amount: 55000, Currency: "USD", Type: "BUY", CreatedAt: now},
			recent: []*transactionentity.Transaction{
				{ID: 34, Amount: 55000, Currency: "USD", Type: "BUY", CreatedAt: now},
				{ID: 35, Amount: 50000, Currency: "USD", Type: "SELL", CreatedAt: now.Add(-48 * time.Hour)},
			},
		},
		{
			name: "transaction frequency", ruleType: "transaction_frequency", config: LegacyRuleConfig{Code: "FREQUENCY", Score: 25, ThresholdCount: 10, Window: 24 * time.Hour},
			legacyRule: &riskflagservice.TransactionFrequencyRule{Config: riskflagservice.RuleConfig{Name: "FREQUENCY", Score: 25, ThresholdCount: 10, Window: 24 * time.Hour}},
			current:    &transactionentity.Transaction{ID: 40, Amount: 100, Currency: "USD", Type: "BUY", CreatedAt: now},
			recent: func() []*transactionentity.Transaction {
				transactions := make([]*transactionentity.Transaction, 0, 11)
				for i := 0; i < 11; i++ {
					transactions = append(transactions, &transactionentity.Transaction{ID: i + 40, Amount: 100, Currency: "USD", Type: "BUY", CreatedAt: now.Add(-time.Duration(i) * time.Hour)})
				}
				return transactions
			}(),
		},
		{
			name: "structuring", ruleType: "structuring", config: LegacyRuleConfig{Code: "STRUCTURING", Score: 40, ThresholdMinor: 1_000_000, ThresholdCount: 3, MarginBPS: 2000, Window: 24 * time.Hour},
			legacyRule: &riskflagservice.StructuringRule{Config: riskflagservice.RuleConfig{Name: "STRUCTURING", Score: 40, ThresholdAmount: 10000, ThresholdCount: 3, Margin: 0.2, Window: 24 * time.Hour}},
			current:    current,
			recent: []*transactionentity.Transaction{
				current,
				{ID: 2, Amount: 9000, Currency: "USD", Type: "BUY", CreatedAt: now.Add(-3 * time.Hour)},
				{ID: 1, Amount: 8800, Currency: "USD", Type: "BUY", CreatedAt: now.Add(-5 * time.Hour)},
			},
		},
		{
			name: "round trip", ruleType: "round_trip", config: LegacyRuleConfig{Code: "ROUND_TRIP", Score: 30, Window: 48 * time.Hour},
			legacyRule: &riskflagservice.RoundTripRule{Config: riskflagservice.RuleConfig{Name: "ROUND_TRIP", Score: 30, Window: 48 * time.Hour}},
			current:    current,
			recent: []*transactionentity.Transaction{
				current,
				{ID: 2, Amount: 9000, Currency: "USD", Type: "BUY", CreatedAt: now.Add(-3 * time.Hour)},
				{ID: 1, Amount: 8800, Currency: "USD", Type: "BUY", CreatedAt: now.Add(-5 * time.Hour)},
			},
		},
		{
			name: "dormant reactivation", ruleType: "dormant_reactivation", config: LegacyRuleConfig{Code: "DORMANT", Score: 20, ThresholdMinor: 500_000, Window: 180 * 24 * time.Hour},
			legacyRule: &riskflagservice.DormantReactivationRule{Config: riskflagservice.RuleConfig{Name: "DORMANT", Score: 20, ThresholdAmount: 5000, Window: 180 * 24 * time.Hour}},
			current:    current,
			signals:    &kycrisk.Signals{PreviousTransactionAt: timePointer(now.AddDate(0, -8, 0))},
		},
	}

	for _, tc := range transactionCases {
		t.Run(tc.name, func(t *testing.T) {
			legacyContext, engineContext := pairedContexts(now, location, tc.current, tc.recent, nil, nil, tc.signals)
			compareLegacyResult(t, tc.legacyRule, legacyContext, tc.ruleType, CategoryDetection, tc.config, engineContext)
		})
	}

	profile := &customerservice.CustomerKYCProfile{
		LegalNature: customerservice.LegalEntity, ActivityProfile: customerservice.Business,
		KYCStatus: customerservice.PartiallyVerified, SourceOfFunds: "Business revenue",
		PurposeOfOperations: "Exchange", ExpectedVolume: 1000,
		IDExpiryDate: "2020-01-31", IsPEP: true, PEPFunction: "Député provincial",
		ActivitySector: "MINING", ActsForThirdParty: true, ThirdPartyName: "Société X",
		Nationality: "BE", ResidenceCountry: "US",
	}
	kycCases := []struct {
		name       string
		ruleType   string
		config     LegacyRuleConfig
		legacyRule riskflagservice.Rule
		customer   *customerentity.Customer
		profile    *customerservice.CustomerKYCProfile
		signals    *kycrisk.Signals
		current    *transactionentity.Transaction
		recent     []*transactionentity.Transaction
	}{
		{
			name: "insufficient KYC", ruleType: "insufficient_kyc_status", config: LegacyRuleConfig{Code: "INSUFFICIENT", Score: 45},
			legacyRule: &riskflagservice.InsufficientKYCStatusRule{Config: riskflagservice.RuleConfig{Name: "INSUFFICIENT", Score: 45}}, profile: profile,
		},
		{
			name: "inconsistent profile", ruleType: "inconsistent_profile", config: LegacyRuleConfig{Code: "INCONSISTENT", Score: 35},
			legacyRule: &riskflagservice.InconsistentProfileRule{Config: riskflagservice.RuleConfig{Name: "INCONSISTENT", Score: 35}},
			profile:    &customerservice.CustomerKYCProfile{LegalNature: customerservice.LegalEntity, ActivityProfile: customerservice.Personal},
		},
		{
			name: "business activity", ruleType: "business_activity", config: LegacyRuleConfig{Code: "BUSINESS", Score: 15},
			legacyRule: &riskflagservice.BusinessActivityRule{Config: riskflagservice.RuleConfig{Name: "BUSINESS", Score: 15}}, profile: profile,
		},
		{
			name: "expected volume exceeded", ruleType: "expected_volume_exceeded", config: LegacyRuleConfig{Code: "EXPECTED_VOLUME", Score: 30},
			legacyRule: &riskflagservice.ExpectedVolumeExceededRule{Config: riskflagservice.RuleConfig{Name: "EXPECTED_VOLUME", Score: 30}},
			profile:    profile, current: &transactionentity.Transaction{ID: 50, Amount: 1200, Currency: "USD", Type: "BUY", CreatedAt: now},
			recent: []*transactionentity.Transaction{{ID: 50, Amount: 1200, Currency: "USD", Type: "BUY", CreatedAt: now}},
		},
		{
			name: "missing ID document", ruleType: "missing_id_document", config: LegacyRuleConfig{Code: "MISSING_ID", Score: 40},
			legacyRule: &riskflagservice.MissingIDDocumentRule{Config: riskflagservice.RuleConfig{Name: "MISSING_ID", Score: 40}}, customer: &customerentity.Customer{IDType: " "},
		},
		{
			name: "missing ID photo", ruleType: "missing_id_photo", config: LegacyRuleConfig{Code: "MISSING_PHOTO", Score: 15, Alert: false},
			legacyRule: &riskflagservice.MissingIDPhotoRule{Config: riskflagservice.RuleConfig{Name: "MISSING_PHOTO", Score: 15}}, signals: &kycrisk.Signals{HasIDFront: true},
		},
		{
			name: "missing phone", ruleType: "missing_phone", config: LegacyRuleConfig{Code: "MISSING_PHONE", Score: 20},
			legacyRule: &riskflagservice.MissingPhoneRule{Config: riskflagservice.RuleConfig{Name: "MISSING_PHONE", Score: 20}}, customer: &customerentity.Customer{Phone: " "},
		},
		{
			name: "missing address", ruleType: "missing_address", config: LegacyRuleConfig{Code: "MISSING_ADDRESS", Score: 5, Alert: false},
			legacyRule: &riskflagservice.MissingAddressRule{Config: riskflagservice.RuleConfig{Name: "MISSING_ADDRESS", Score: 5}}, customer: &customerentity.Customer{Address: " "},
		},
		{
			name: "duplicate ID", ruleType: "duplicate_id_number", config: LegacyRuleConfig{Code: "DUPLICATE_ID", Score: 35},
			legacyRule: &riskflagservice.DuplicateIDNumberRule{Config: riskflagservice.RuleConfig{Name: "DUPLICATE_ID", Score: 35}}, signals: &kycrisk.Signals{DuplicateIDCount: 2},
		},
		{
			name: "expired ID", ruleType: "expired_id_document", config: LegacyRuleConfig{Code: "EXPIRED_ID", Score: 45},
			legacyRule: &riskflagservice.ExpiredIDDocumentRule{Config: riskflagservice.RuleConfig{Name: "EXPIRED_ID", Score: 45}}, profile: profile,
		},
		{
			name: "politically exposed", ruleType: "politically_exposed", config: LegacyRuleConfig{Code: "PEP", Score: 35},
			legacyRule: &riskflagservice.PoliticallyExposedRule{Config: riskflagservice.RuleConfig{Name: "PEP", Score: 35}}, profile: profile,
		},
		{
			name: "sensitive sector", ruleType: "sensitive_sector", config: LegacyRuleConfig{Code: "SECTOR", Score: 25},
			legacyRule: &riskflagservice.SensitiveSectorRule{Config: riskflagservice.RuleConfig{Name: "SECTOR", Score: 25}}, profile: profile,
		},
		{
			name: "third party", ruleType: "third_party", config: LegacyRuleConfig{Code: "THIRD_PARTY", Score: 30},
			legacyRule: &riskflagservice.ThirdPartyRule{Config: riskflagservice.RuleConfig{Name: "THIRD_PARTY", Score: 30}}, profile: profile,
		},
		{
			name: "foreign or non-resident", ruleType: "foreign_or_non_resident", config: LegacyRuleConfig{Code: "FOREIGN", Score: 15, Alert: false},
			legacyRule: &riskflagservice.ForeignOrNonResidentRule{Config: riskflagservice.RuleConfig{Name: "FOREIGN", Score: 15}}, profile: profile,
		},
		{
			name: "KYC review due", ruleType: "kyc_review_due", config: LegacyRuleConfig{Code: "REVIEW_DUE", Score: 20, Alert: false},
			legacyRule: &riskflagservice.KYCReviewDueRule{Config: riskflagservice.RuleConfig{Name: "REVIEW_DUE", Score: 20}},
			customer:   &customerentity.Customer{RiskLevel: "HIGH"},
			profile:    &customerservice.CustomerKYCProfile{KYCStatus: customerservice.Verified, LastVerificationDate: now.AddDate(0, -13, 0)},
		},
	}
	for _, tc := range kycCases {
		t.Run(tc.name, func(t *testing.T) {
			legacyContext, engineContext := pairedContexts(now, location, tc.current, tc.recent, tc.customer, tc.profile, tc.signals)
			compareLegacyResult(t, tc.legacyRule, legacyContext, tc.ruleType, CategoryKYCLevel, tc.config, engineContext)
		})
	}
}

func pairedContexts(
	now time.Time,
	location *time.Location,
	current *transactionentity.Transaction,
	recent []*transactionentity.Transaction,
	customer *customerentity.Customer,
	profile *customerservice.CustomerKYCProfile,
	signals *kycrisk.Signals,
) (*riskflagservice.RuleContext, EvalContext) {
	if current == nil {
		current = &transactionentity.Transaction{ID: 999, Amount: 0, Currency: "USD", Type: "BUY", CreatedAt: now}
	}
	history := make([]HistoricalOperation, 0, len(recent))
	for _, transaction := range recent {
		if transaction.ID == current.ID {
			continue
		}
		minor, err := parseMajorAmountMinor([]byte(strconvFormat(transaction.Amount)))
		if err != nil {
			panic(err)
		}
		history = append(history, HistoricalOperation{
			ID: int64(transaction.ID), OperatorID: int64(transaction.TraderID), StoreID: int64(transaction.StoreID),
			AmountMinor: minor, Currency: transaction.Currency, Type: transaction.Type, OccurredAt: transaction.CreatedAt,
		})
	}
	currentMinor, err := parseMajorAmountMinor([]byte(strconvFormat(current.Amount)))
	if err != nil {
		panic(err)
	}
	var expectedVolumeMinor int64
	if profile != nil && profile.ExpectedVolume > 0 {
		expectedMinor, err := parseMajorAmountMinor([]byte(strconv.FormatFloat(profile.ExpectedVolume, 'f', 2, 64)))
		if err != nil {
			panic(err)
		}
		expectedVolumeMinor = expectedMinor
	}
	return &riskflagservice.RuleContext{
			Transaction: current, Customer: customer, KYCProfile: profile, RecentTransactions: recent, Signals: signals,
		}, EvalContext{
			Operation: Operation{
				ID: int64(current.ID), PersonID: 1, OperatorID: int64(current.TraderID), StoreID: int64(current.StoreID),
				AmountMinor: currentMinor, Currency: current.Currency, Type: current.Type, OccurredAt: current.CreatedAt,
			},
			Customer: customer, KYCProfile: profile, ExpectedVolumeMinor: expectedVolumeMinor, Signals: signals, History: history,
			Clock: ClockFunc(func() time.Time { return now }), ReferenceTimezone: location,
		}
}

func compareLegacyResult(
	t *testing.T,
	legacy riskflagservice.Rule,
	legacyContext *riskflagservice.RuleContext,
	ruleType string,
	category Category,
	config LegacyRuleConfig,
	input EvalContext,
) {
	t.Helper()
	legacyFlag := legacy.Evaluate(legacyContext)
	newRule, known := makeLegacyRule(ruleType, category, config)
	if !known {
		t.Fatalf("rule type %q was not built", ruleType)
	}
	result, err := newRule.Evaluate(t.Context(), input)
	if err != nil {
		t.Fatalf("new rule Evaluate() error = %v", err)
	}
	if legacyFlag == nil {
		if result.Outcome != OutcomePass || result.Score != 0 || len(result.Reasons) != 0 {
			t.Fatalf("legacy returned no flag; new result = %+v", result)
		}
		return
	}
	if result.Outcome != OutcomeAlert || result.Score != int(legacyFlag.Score) || len(result.Reasons) != 1 || result.Reasons[0] != legacyFlag.Reason {
		t.Fatalf("legacy flag = {name:%q reason:%q score:%v}, new result = %+v", legacyFlag.Flag, legacyFlag.Reason, legacyFlag.Score, result)
	}
	if newRule.Code() != legacyFlag.Flag {
		t.Fatalf("new rule code = %q, legacy flag = %q", newRule.Code(), legacyFlag.Flag)
	}
}

func timePointer(value time.Time) *time.Time { return &value }

func strconvFormat(value float64) string {
	return fmt.Sprintf("%.2f", value)
}
