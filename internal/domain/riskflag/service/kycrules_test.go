package service

import (
	"testing"
	"time"

	customerentity "fx-app-api/internal/domain/customer/entity"
	"fx-app-api/internal/domain/customer/kycrisk"
	customerservice "fx-app-api/internal/domain/customer/service"
	transactionentity "fx-app-api/internal/domain/transaction/entity"
)

func cfg(name string) RuleConfig { return RuleConfig{Name: name, Score: 10, Margin: 0.2, Alert: true} }

func TestIdentityRules(t *testing.T) {
	noID := &RuleContext{Customer: &customerentity.Customer{Phone: "+243810000001"}}
	if (&MissingIDDocumentRule{Config: cfg("MISSING_ID_DOCUMENT")}).Evaluate(noID) == nil {
		t.Fatal("expected a flag for a customer without identity document")
	}
	withID := &RuleContext{Customer: &customerentity.Customer{IDType: "VOTER_CARD", Phone: "+243810000001", Address: "Gombe"}}
	if (&MissingIDDocumentRule{Config: cfg("MISSING_ID_DOCUMENT")}).Evaluate(withID) != nil {
		t.Fatal("voter card is a valid identity document")
	}
	if (&MissingPhoneRule{Config: cfg("MISSING_PHONE")}).Evaluate(withID) != nil {
		t.Fatal("unexpected missing phone flag")
	}
	if (&MissingAddressRule{Config: cfg("MISSING_ADDRESS")}).Evaluate(noID) == nil {
		t.Fatal("expected missing address flag")
	}

	photo := &MissingIDPhotoRule{Config: cfg("MISSING_ID_PHOTO")}
	flag := photo.Evaluate(&RuleContext{Signals: &kycrisk.Signals{HasIDFront: true}})
	if flag == nil || flag.Reason != "Identity document photo missing (back)" {
		t.Fatalf("expected missing back photo, got %+v", flag)
	}
	if photo.Evaluate(&RuleContext{Signals: &kycrisk.Signals{HasIDFront: true, HasIDBack: true}}) != nil {
		t.Fatal("complete photos must not be flagged")
	}

	dup := (&DuplicateIDNumberRule{Config: cfg("DUPLICATE_ID_NUMBER")}).Evaluate(&RuleContext{Signals: &kycrisk.Signals{DuplicateIDCount: 2}})
	if dup == nil || dup.Reason != "Identity document number shared with 2 other customer(s)" {
		t.Fatalf("unexpected duplicate flag %+v", dup)
	}
}

func TestProfileRules(t *testing.T) {
	expired := &RuleContext{KYCProfile: &customerservice.CustomerKYCProfile{IDExpiryDate: "2020-01-31"}}
	if flag := (&ExpiredIDDocumentRule{Config: cfg("EXPIRED_ID_DOCUMENT")}).Evaluate(expired); flag == nil || flag.Reason != "Identity document expired on 2020-01-31" {
		t.Fatalf("expected expired document flag, got %+v", flag)
	}
	valid := &RuleContext{KYCProfile: &customerservice.CustomerKYCProfile{IDExpiryDate: time.Now().AddDate(1, 0, 0).Format("2006-01-02")}}
	if (&ExpiredIDDocumentRule{Config: cfg("EXPIRED_ID_DOCUMENT")}).Evaluate(valid) != nil {
		t.Fatal("valid document must not be flagged")
	}

	profile := &customerservice.CustomerKYCProfile{
		IsPEP: true, PEPFunction: "Député provincial",
		ActivitySector:    "MINING",
		ActsForThirdParty: true, ThirdPartyName: "Société X",
		Nationality: "BE", ResidenceCountry: "CD",
	}
	ctx := &RuleContext{KYCProfile: profile}
	if f := (&PoliticallyExposedRule{Config: cfg("PEP")}).Evaluate(ctx); f == nil || f.Reason != "Customer is a politically exposed person (Député provincial)" {
		t.Fatalf("unexpected PEP flag %+v", f)
	}
	if (&SensitiveSectorRule{Config: cfg("SECTOR")}).Evaluate(ctx) == nil {
		t.Fatal("mining is a sensitive sector")
	}
	if (&ThirdPartyRule{Config: cfg("THIRD")}).Evaluate(ctx) == nil {
		t.Fatal("expected third party flag")
	}
	if f := (&ForeignOrNonResidentRule{Config: cfg("FOREIGN")}).Evaluate(ctx); f == nil || f.Reason != "Customer is foreign national (BE)" {
		t.Fatalf("unexpected foreign flag %+v", f)
	}
	if (&ForeignOrNonResidentRule{Config: cfg("FOREIGN")}).Evaluate(&RuleContext{KYCProfile: &customerservice.CustomerKYCProfile{Nationality: "CD", ResidenceCountry: "CD"}}) != nil {
		t.Fatal("Congolese resident must not be flagged")
	}
}

func TestKYCReviewDueRule(t *testing.T) {
	rule := &KYCReviewDueRule{Config: cfg("KYC_REVIEW_DUE")}
	verifiedAgo := func(months int, level string) *RuleContext {
		return &RuleContext{
			Customer: &customerentity.Customer{RiskLevel: level},
			KYCProfile: &customerservice.CustomerKYCProfile{
				KYCStatus:            customerservice.Verified,
				LastVerificationDate: time.Now().AddDate(0, -months, 0),
			},
		}
	}
	if rule.Evaluate(verifiedAgo(13, "HIGH")) == nil {
		t.Fatal("high risk: review due after 12 months")
	}
	if rule.Evaluate(verifiedAgo(13, "MEDIUM")) != nil {
		t.Fatal("medium risk: review due after 24 months only")
	}
	if rule.Evaluate(verifiedAgo(30, "LOW")) != nil {
		t.Fatal("low risk: review due after 36 months only")
	}
	if rule.Evaluate(verifiedAgo(37, "LOW")) == nil {
		t.Fatal("low risk: review due after 36 months")
	}
}

func TestBehaviourRules(t *testing.T) {
	now := time.Now()
	current := &transactionentity.Transaction{ID: 3, Amount: 9500, Currency: "USD", Type: "SELL", CreatedAt: now}
	recent := []*transactionentity.Transaction{
		current,
		{ID: 2, Amount: 9000, Currency: "USD", Type: "BUY", CreatedAt: now.Add(-3 * time.Hour)},
		{ID: 1, Amount: 8800, Currency: "USD", Type: "BUY", CreatedAt: now.Add(-5 * time.Hour)},
	}
	ctx := &RuleContext{Transaction: current, RecentTransactions: recent}

	structuring := &StructuringRule{Config: RuleConfig{Name: "STRUCTURING", Score: 40, ThresholdAmount: 10000, ThresholdCount: 3, Margin: 0.2, Window: 24 * time.Hour}}
	if f := structuring.Evaluate(ctx); f == nil || f.Reason != "3 transactions just below threshold 10000.00 over 24h0m0s" {
		t.Fatalf("expected structuring flag, got %+v", f)
	}
	small := &RuleContext{Transaction: &transactionentity.Transaction{ID: 9, Amount: 500}, RecentTransactions: recent}
	if structuring.Evaluate(small) != nil {
		t.Fatal("an amount far below the threshold is not structuring")
	}

	roundTrip := &RoundTripRule{Config: RuleConfig{Name: "ROUND_TRIP", Score: 30, Window: 48 * time.Hour}}
	if f := roundTrip.Evaluate(ctx); f == nil || f.Reason != "Opposite SELL after BUY on USD within 48h0m0s (transaction 2)" {
		t.Fatalf("expected round trip flag, got %+v", f)
	}

	dormant := &DormantReactivationRule{Config: RuleConfig{Name: "DORMANT", Score: 20, ThresholdAmount: 5000, Window: 180 * 24 * time.Hour}}
	longAgo := now.AddDate(0, -8, 0)
	if dormant.Evaluate(&RuleContext{Transaction: current, Signals: &kycrisk.Signals{PreviousTransactionAt: &longAgo}}) == nil {
		t.Fatal("expected dormant reactivation flag")
	}
	lastWeek := now.AddDate(0, 0, -7)
	if dormant.Evaluate(&RuleContext{Transaction: current, Signals: &kycrisk.Signals{PreviousTransactionAt: &lastWeek}}) != nil {
		t.Fatal("active customer must not be flagged")
	}
}

func TestSilentRulesFromConfig(t *testing.T) {
	silent := silentRuleNames(defaultRiskRulesFile())
	for _, name := range []string{"MISSING_ID_PHOTO", "MISSING_ADDRESS", "FOREIGN_OR_NON_RESIDENT", "KYC_REVIEW_DUE"} {
		if !silent[name] {
			t.Fatalf("%s should count in the score without creating an alert", name)
		}
	}
	if silent["MISSING_ID_DOCUMENT"] || silent["HIGH_TRANSACTION_AMOUNT"] {
		t.Fatal("blocking and amount rules must create alerts")
	}
}
