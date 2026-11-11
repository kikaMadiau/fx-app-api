package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadAMLRuleSeed(t *testing.T) {
	seedPath := filepath.Join(t.TempDir(), "rules.json")
	seedJSON := `{
		"transaction_rules": [{"name":"TEST_TX","type":"high_amount","enabled":true,"threshold_amount":1000}],
		"customer_rules": [{"name":"TEST_KYC","type":"missing_address","enabled":false}],
		"new_rules": [{"code":"TEST_CAP","type":"cap_jour","version":"1","category":"HARD_BLOCK","mode":"SHADOW","enabled":true,"score":0,"severity":"CRITICAL","action":"BLOCK","parameters":{"limit_usd_minor":10000}}]
	}`
	if err := os.WriteFile(seedPath, []byte(seedJSON), 0600); err != nil {
		t.Fatal(err)
	}

	seed, err := readAMLRuleSeed(seedPath)
	if err != nil {
		t.Fatalf("readAMLRuleSeed() error = %v", err)
	}
	if len(seed.TransactionRules) != 1 || len(seed.CustomerRules) != 1 || len(seed.NewRules) != 1 {
		t.Fatalf("seed rule counts = transaction:%d customer:%d new:%d, want 1 each", len(seed.TransactionRules), len(seed.CustomerRules), len(seed.NewRules))
	}
	if string(seed.TransactionRules[0]["name"]) != `"TEST_TX"` {
		t.Fatalf("transaction rule name = %s, want TEST_TX", seed.TransactionRules[0]["name"])
	}
	if string(seed.TransactionRules[0]["threshold_amount"]) != "1000" {
		t.Fatalf("transaction threshold = %s, want 1000", seed.TransactionRules[0]["threshold_amount"])
	}
}

func TestReadAMLRuleSeedRejectsEmptyCatalogue(t *testing.T) {
	seedPath := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(seedPath, []byte(`{"transaction_rules":[],"customer_rules":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAMLRuleSeed(seedPath); err == nil {
		t.Fatal("readAMLRuleSeed() expected an error for an empty catalogue")
	}
}
