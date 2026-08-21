package service

import (
	customerentity "fx-app-api/internal/domain/customer/entity"
	customerservice "fx-app-api/internal/domain/customer/service"
	transactionentity "fx-app-api/internal/domain/transaction/entity"
	"testing"
	"time"
)

func TestCumulativeVolumeRuleFlags24HThreshold(t *testing.T) {
	now := time.Now()
	ctx := &RuleContext{
		RecentTransactions: []*transactionentity.Transaction{
			{Amount: 9000, CreatedAt: now.Add(-2 * time.Hour)},
			{Amount: 12000, CreatedAt: now.Add(-3 * time.Hour)},
			{Amount: 50000, CreatedAt: now.Add(-48 * time.Hour)},
		},
	}
	rule := &CumulativeVolumeRule{
		Config: RuleConfig{Name: "CUMULATIVE_VOLUME_24H", Score: 35, ThresholdAmount: 20000, Window: 24 * time.Hour},
	}

	flag := rule.Evaluate(ctx)
	if flag == nil {
		t.Fatal("expected cumulative volume flag")
	}
	if flag.Score != 35 {
		t.Fatalf("expected score 35, got %.2f", flag.Score)
	}
}

func TestTransactionFrequencyRuleFlagsHighFrequency(t *testing.T) {
	now := time.Now()
	var transactions []*transactionentity.Transaction
	for i := 0; i < 11; i++ {
		transactions = append(transactions, &transactionentity.Transaction{Amount: 100, CreatedAt: now.Add(-time.Duration(i) * time.Hour)})
	}

	rule := &TransactionFrequencyRule{
		Config: RuleConfig{Name: "UNUSUAL_FREQUENCY_24H", Score: 25, ThresholdCount: 10, Window: 24 * time.Hour},
	}

	flag := rule.Evaluate(&RuleContext{RecentTransactions: transactions})
	if flag == nil {
		t.Fatal("expected transaction frequency flag")
	}
}

func TestKYCAndCustomerRules(t *testing.T) {
	ctx := &RuleContext{
		Customer: &customerentity.Customer{ID: 1},
		KYCProfile: &customerservice.CustomerKYCProfile{
			LegalNature:     customerservice.LegalEntity,
			ActivityProfile: customerservice.Business,
			KYCStatus:       customerservice.PartiallyVerified,
			SourceOfFunds:   "Business revenue",
			ExpectedVolume:  1000,
		},
		RecentTransactions: []*transactionentity.Transaction{
			{Amount: 1200, CreatedAt: time.Now().Add(-23 * time.Hour)},
		},
	}

	tests := []Rule{
		&InsufficientKYCStatusRule{Config: RuleConfig{Name: "INSUFFICIENT_KYC_STATUS", Score: 45}},
		&InconsistentProfileRule{Config: RuleConfig{Name: "INCONSISTENT_KYC_PROFILE", Score: 35}},
		&BusinessActivityRule{Config: RuleConfig{Name: "BUSINESS_ACTIVITY_PROFILE", Score: 15}},
		&ExpectedVolumeExceededRule{Config: RuleConfig{Name: "EXPECTED_VOLUME_EXCEEDED", Score: 30}},
	}

	for _, rule := range tests {
		t.Run(rule.Name(), func(t *testing.T) {
			if flag := rule.Evaluate(ctx); flag == nil {
				t.Fatalf("expected flag for rule %s", rule.Name())
			}
		})
	}
}
