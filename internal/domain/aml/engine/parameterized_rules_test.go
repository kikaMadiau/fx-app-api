package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	customerentity "fx-app-api/internal/domain/customer/entity"
)

func TestRegisterShadowRulesFromSeedCatalog(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "config", "riskRules.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read AML catalog %q: %v", path, err)
	}
	var catalogue struct {
		Rules []PersistedRule `json:"new_rules"`
	}
	if err := json.Unmarshal(data, &catalogue); err != nil {
		t.Fatalf("decode AML catalog: %v", err)
	}
	registry := NewRegistry()
	if err := RegisterShadowRules(registry, catalogue.Rules); err != nil {
		t.Fatalf("RegisterShadowRules() error = %v", err)
	}
	registered := registry.List("")
	if len(registered) != 8 {
		t.Fatalf("registered %d enabled shadow rules, want 8", len(registered))
	}
	for _, entry := range registered {
		if entry.Mode != ModeShadow {
			t.Errorf("rule %q mode = %q, want SHADOW", entry.Rule.Code(), entry.Mode)
		}
	}
}

func TestParameterizedRuleOutcomes(t *testing.T) {
	now := time.Date(2026, time.January, 10, 12, 0, 0, 0, time.UTC)
	baseContext := func() EvalContext {
		return EvalContext{
			Operation: Operation{
				ID: 100, PersonID: 1, OperatorID: 2, StoreID: 20,
				AmountMinor: 10_000, Currency: "USD", Type: "SELL", TargetCurrency: "CDF",
				Status: "COMPLETED", GrossUSDMinor: 400_000, NetUSDMinor: 390_000,
				OccurredAt: now,
			},
			Customer:          &customerentity.Customer{Phone: "+243810000001"},
			Clock:             ClockFunc(func() time.Time { return now }),
			ReferenceTimezone: time.UTC,
			BCCRatePPM:        1_000_000,
			BCCRateAt:         now,
		}
	}

	tests := []struct {
		name   string
		record PersistedRule
		input  func() EvalContext
		want   Outcome
		score  int
	}{
		{
			name:   "daily cap cross-operator default alerts critically",
			record: persistedRule("CAP_JOUR", CategoryHardBlock, `{"limit_usd_minor":1000000,"scope":"ALL_OPERATORS","cross_operator_action":"ALERT_CRITICAL","cross_operator_severity":"CRITICAL","amount_basis":"GROSS","cancelled_policy":"COUNT"}`),
			input: func() EvalContext {
				input := baseContext()
				input.History = []HistoricalOperation{{ID: 99, OperatorID: 1, AmountMinor: 1, Currency: "USD", GrossUSDMinor: 600_000, Status: "COMPLETED", OccurredAt: now.Add(-time.Hour)}}
				return input
			},
			want: OutcomeAlert,
		},
		{
			name:   "cumulative alert threshold",
			record: persistedRule("CUMUL_ALERTE", CategoryDetection, `{"alert_threshold_usd_minor":900000,"block_threshold_usd_minor":1000000,"scope":"ALL_OPERATORS","amount_basis":"GROSS","cancelled_policy":"COUNT","alert_action":"ALERT","block_action":"BLOCK","block_severity":"CRITICAL"}`),
			input: func() EvalContext {
				input := baseContext()
				input.Operation.GrossUSDMinor = 900_000
				return input
			},
			want: OutcomeAlert,
		},
		{
			name:   "cumulative block threshold",
			record: persistedRule("CUMUL_ALERTE", CategoryDetection, `{"alert_threshold_usd_minor":900000,"block_threshold_usd_minor":1000000,"scope":"ALL_OPERATORS","amount_basis":"GROSS","cancelled_policy":"COUNT","alert_action":"ALERT","block_action":"BLOCK","block_severity":"CRITICAL"}`),
			input: func() EvalContext {
				input := baseContext()
				input.Operation.GrossUSDMinor = 1_000_000
				return input
			},
			want: OutcomeBlock,
		},
		{
			name:   "fractioning count and multi-operator signals",
			record: persistedRule("FRACTIONNEMENT", CategoryDetection, `{"window":"24h","minimum_count":3,"minimum_total_usd_minor":500000,"minimum_distinct_operators":2,"amount_basis":"GROSS","cancelled_policy":"COUNT"}`),
			input: func() EvalContext {
				input := baseContext()
				input.History = []HistoricalOperation{
					{ID: 98, OperatorID: 1, AmountMinor: 1, Currency: "USD", GrossUSDMinor: 160_000, OccurredAt: now.Add(-time.Hour)},
					{ID: 99, OperatorID: 1, AmountMinor: 1, Currency: "USD", GrossUSDMinor: 140_000, OccurredAt: now.Add(-2 * time.Hour)},
				}
				return input
			},
			want: OutcomeAlert,
		},
		{
			name:   "repeated opposite currency pair",
			record: persistedRule("ALLER_RETOUR", CategoryDetection, `{"pair_window":"72h","cycle_window":"168h","maximum_amount_gap_bps":1000,"minimum_amount_usd_minor":100000,"minimum_cycles":2,"amount_basis":"GROSS","cancelled_policy":"COUNT"}`),
			input: func() EvalContext {
				input := baseContext()
				input.Operation.GrossUSDMinor = 100_000
				input.History = []HistoricalOperation{
					{ID: 98, OperatorID: 1, AmountMinor: 1, Currency: "USD", TargetCurrency: "CDF", Type: "BUY", GrossUSDMinor: 100_000, OccurredAt: now.Add(-time.Hour)},
					{ID: 99, OperatorID: 3, AmountMinor: 1, Currency: "USD", TargetCurrency: "CDF", Type: "BUY", GrossUSDMinor: 100_000, OccurredAt: now.Add(-2 * time.Hour)},
				}
				return input
			},
			want: OutcomeAlert,
		},
		{
			name:   "FT repeated small transactions with risk-zone score factor",
			record: persistedRuleWithScore("FT_SCHEMA", CategoryDetection, 10, `{"window":"168h","minimum_operations":5,"maximum_single_amount_usd_minor":100000,"linked_by":["BENEFICIARY","PHONE"],"risk_zones":["ZONE_A"],"risk_zone_score":5,"amount_basis":"GROSS","cancelled_policy":"COUNT"}`),
			input: func() EvalContext {
				input := baseContext()
				input.Operation.GrossUSDMinor = 90_000
				input.Operation.BeneficiaryKey = "beneficiary-1"
				input.Operation.RiskZone = "ZONE_A"
				input.History = make([]HistoricalOperation, 4)
				for i := range input.History {
					input.History[i] = HistoricalOperation{
						ID: int64(i + 1), OperatorID: int64(i + 1), AmountMinor: 1,
						Currency: "USD", GrossUSDMinor: 90_000, BeneficiaryKey: "beneficiary-1",
						Phone: "+243810000001", OccurredAt: now.Add(-time.Duration(i+1) * time.Hour),
					}
				}
				return input
			},
			want: OutcomeAlert, score: 15,
		},
		{
			name:   "sanctions match blocks",
			record: persistedRule("SANCTIONS", CategoryScreening, `{"source_ids":["LIST_A"]}`, "BLOCK"),
			input: func() EvalContext {
				input := baseContext()
				input.SanctionsScreeningAvailable = true
				input.SanctionsMatches = []SanctionsMatch{{SourceID: "LIST_A", PartyType: "CUSTOMER", Reference: "match-1", MatchScore: 10000}}
				return input
			},
			want: OutcomeBlock,
		},
		{
			name:   "identity completeness blocks missing required address",
			record: persistedRule("IDENT_COMPLETE", CategoryHardBlock, `{"require_customer":true,"require_beneficial_owner":true,"require_document":true,"require_valid_document":true,"require_address":true,"require_photo":true}`, "BLOCK"),
			input: func() EvalContext {
				input := baseContext()
				input.Identity = IdentitySnapshot{CustomerIdentified: true, BeneficialOwnerIdentified: true, IdentityDocumentPresented: true, IdentityDocumentValid: true, PhotoPresent: true}
				return input
			},
			want: OutcomeBlock,
		},
		{
			name:   "inactive office blocks",
			record: persistedRule("AGREMENT_ACTIF", CategoryHardBlock, `{"active_statuses":["ACTIVE"]}`, "BLOCK"),
			input: func() EvalContext {
				input := baseContext()
				input.License = LicenseSnapshot{OperatorStatus: "ACTIVE", StoreStatus: "SUSPENDED"}
				return input
			},
			want: OutcomeBlock,
		},
		{
			name:   "prohibited operation blocks",
			record: persistedRule("OPERATION_INTERDITE", CategoryHardBlock, `{"prohibited_types":["CREDIT"]}`, "BLOCK"),
			input: func() EvalContext {
				input := baseContext()
				input.Operation.OperationClass = "CREDIT"
				return input
			},
			want: OutcomeBlock,
		},
		{
			name:   "rate deviation alerts",
			record: persistedRule("ECART_TAUX", CategoryDetection, `{"maximum_deviation_bps":500}`),
			input: func() EvalContext {
				input := baseContext()
				input.Operation.RatePPM = 1_100_000
				return input
			},
			want: OutcomeAlert,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := NewRegistry()
			if err := RegisterShadowRules(registry, []PersistedRule{test.record}); err != nil {
				t.Fatalf("RegisterShadowRules() error = %v", err)
			}
			registered, ok := registry.Get(test.record.Code)
			if !ok {
				t.Fatalf("rule %q was not registered", test.record.Code)
			}
			result, err := registered.Rule.Evaluate(context.Background(), test.input())
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if result.Outcome != test.want {
				t.Fatalf("Outcome = %q, want %q; result: %+v", result.Outcome, test.want, result)
			}
			if test.score != 0 && result.Score != test.score {
				t.Fatalf("Score = %d, want %d", result.Score, test.score)
			}
		})
	}
}

func TestParameterizedRulesRejectInvalidConfigAndEnforceMode(t *testing.T) {
	tests := []PersistedRule{
		persistedRule("ECART_TAUX", CategoryDetection, `{}`),
		persistedRule("CAP_JOUR", CategoryHardBlock, `{"limit_usd_minor":100,"scope":"ALL_OPERATORS","cross_operator_action":"BLOCK","amount_basis":"GROSS","cancelled_policy":"COUNT","unexpected":1}`),
	}
	for _, record := range tests {
		if err := RegisterShadowRules(NewRegistry(), []PersistedRule{record}); err == nil {
			t.Errorf("RegisterShadowRules() accepted invalid parameters for %s", record.Code)
		}
	}
	active := persistedRule("CAP_JOUR", CategoryHardBlock, `{"limit_usd_minor":100,"scope":"ALL_OPERATORS","cross_operator_action":"BLOCK","amount_basis":"GROSS","cancelled_policy":"COUNT"}`)
	active.Mode = ModeEnforce
	if err := RegisterShadowRules(NewRegistry(), []PersistedRule{active}); err == nil {
		t.Fatal("RegisterShadowRules() accepted an ENFORCE mode rule")
	}
}

func persistedRule(code string, category Category, parameters string, action ...string) PersistedRule {
	return persistedRuleWithScore(code, category, 0, parameters, action...)
}

func persistedRuleWithScore(code string, category Category, score int, parameters string, action ...string) PersistedRule {
	selectedAction := "ALERT"
	if len(action) > 0 {
		selectedAction = action[0]
	}
	return PersistedRule{
		Code: code, Version: "test-1", Category: category, Mode: ModeShadow, Enabled: true,
		Score: score, Severity: "TEST", Action: selectedAction, Parameters: json.RawMessage(parameters),
	}
}
