package service

import (
	customerentity "fx-app-api/internal/domain/customer/entity"
	customerservice "fx-app-api/internal/domain/customer/service"
	transactionentity "fx-app-api/internal/domain/transaction/entity"
	"os"
	"path/filepath"
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

func TestLoadRulesFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "risk_rules.json")
	config := []byte(`{
		"transaction_rules": [
			{
				"type": "high_amount",
				"name": "CUSTOM_HIGH_AMOUNT",
				"score": 12,
				"enabled": true,
				"threshold_amount": 5000
			},
			{
				"type": "transaction_frequency",
				"name": "DISABLED_FREQUENCY",
				"score": 99,
				"enabled": false,
				"threshold_count": 1,
				"window": "1h"
			}
		],
		"customer_rules": [
			{
				"type": "business_activity",
				"name": "CUSTOM_BUSINESS_ACTIVITY",
				"score": 7,
				"enabled": true
			}
		]
	}`)
	if err := os.WriteFile(path, config, 0o600); err != nil {
		t.Fatalf("failed to write risk rules config: %v", err)
	}

	transactionRules, customerRules, err := loadRulesFromFile(path)
	if err != nil {
		t.Fatalf("expected rules to load: %v", err)
	}
	if len(transactionRules) != 1 {
		t.Fatalf("expected 1 enabled transaction rule, got %d", len(transactionRules))
	}
	if transactionRules[0].Name() != "CUSTOM_HIGH_AMOUNT" {
		t.Fatalf("expected custom transaction rule name, got %s", transactionRules[0].Name())
	}
	if len(customerRules) != 1 {
		t.Fatalf("expected 1 customer rule, got %d", len(customerRules))
	}
	if customerRules[0].Name() != "CUSTOM_BUSINESS_ACTIVITY" {
		t.Fatalf("expected custom customer rule name, got %s", customerRules[0].Name())
	}
}

func TestDefaultRiskRulesMatchExpectedCatalog(t *testing.T) {
	defaults := defaultRiskRulesFile()

	transactionRules := buildTransactionRules(defaults.TransactionRules)
	customerRules := buildCustomerRules(defaults.CustomerRules)

	if len(transactionRules) != 4 {
		t.Fatalf("expected 4 default transaction rules, got %d", len(transactionRules))
	}
	if len(customerRules) != 4 {
		t.Fatalf("expected 4 default customer rules, got %d", len(customerRules))
	}
}
