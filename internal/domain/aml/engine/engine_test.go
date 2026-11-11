package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

type testRule struct {
	code     string
	category Category
	result   Result
	err      error
}

func (r testRule) Code() string       { return r.code }
func (r testRule) Category() Category { return r.category }
func (r testRule) Evaluate(context.Context, EvalContext) (Result, error) {
	if r.err != nil {
		return Result{}, r.err
	}
	if r.result.Outcome != "" {
		return r.result, nil
	}
	return Result{Outcome: OutcomePass}, nil
}

func TestEngineRecordsDisabledAndShadowEvaluationErrors(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(testRule{code: "OFF_RULE", category: CategoryHardBlock}, "3", ModeOff, RuleMetadata{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(testRule{code: "SHADOW_RULE", category: CategoryDetection, err: errors.New("missing BCC rate")}, "2", ModeShadow); err != nil {
		t.Fatal(err)
	}
	input := EvalContext{Operation: Operation{PersonID: 1, AmountMinor: 100, Currency: "USD", OccurredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, Clock: ClockFunc(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }), ReferenceTimezone: time.UTC}
	decision, err := New(registry).Decide(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(decision.Results) != 2 {
		t.Fatalf("got %d journal entries, want 2", len(decision.Results))
	}
	if decision.Results[0].Evaluated || decision.Results[0].Enabled {
		t.Fatalf("OFF rule journal entry = %#v", decision.Results[0])
	}
	if decision.Results[1].Result.Evidence["evaluation_skipped"] != true || decision.Results[1].Result.Evidence["evaluation_error"] != "missing BCC rate" {
		t.Fatalf("shadow error evidence = %#v", decision.Results[1])
	}
}

func TestEngineDecideOrderingAndModes(t *testing.T) {
	registry := NewRegistry()
	entries := []struct {
		rule testRule
		mode Mode
	}{
		{testRule{code: "D_ALERT", category: CategoryDetection, result: Result{Outcome: OutcomeAlert, Score: 8}}, ModeEnforce},
		{testRule{code: "S_BLOCK", category: CategoryScreening, result: Result{Outcome: OutcomeBlock}}, ModeShadow},
		{testRule{code: "H_REVIEW", category: CategoryHardBlock, result: Result{Outcome: OutcomeReview}}, ModeEnforce},
		{testRule{code: "K_PASS", category: CategoryKYCLevel}, ModeEnforce},
	}
	for _, entry := range entries {
		if err := registry.Register(entry.rule, "1", entry.mode); err != nil {
			t.Fatal(err)
		}
	}
	input := EvalContext{Operation: Operation{PersonID: 1, AmountMinor: 100, Currency: "USD", OccurredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, Clock: ClockFunc(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }), ReferenceTimezone: time.UTC}
	got, err := New(registry).Decide(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != DecisionAcceptWithApproval || got.Score != 8 {
		t.Fatalf("decision = %#v", got)
	}
	want := []string{"H_REVIEW", "S_BLOCK", "K_PASS", "D_ALERT"}
	if len(got.Results) != len(want) {
		t.Fatalf("got %d results", len(got.Results))
	}
	for i := range want {
		if got.Results[i].Code != want[i] {
			t.Errorf("result[%d] = %q, want %q", i, got.Results[i].Code, want[i])
		}
	}
}

func TestEngineStopsAfterEnforcedHardBlockButJournalsRemainingRules(t *testing.T) {
	registry := NewRegistry()
	for _, rule := range []testRule{
		{code: "A_BLOCK", category: CategoryHardBlock, result: Result{Outcome: OutcomeBlock}},
		{code: "B_LATER_HARD", category: CategoryHardBlock, result: Result{Outcome: OutcomePass}},
		{code: "C_SCREEN", category: CategoryScreening, result: Result{Outcome: OutcomePass}},
	} {
		if err := registry.Register(rule, "1", ModeEnforce); err != nil {
			t.Fatal(err)
		}
	}
	input := EvalContext{Operation: Operation{PersonID: 1, AmountMinor: 100, Currency: "USD", OccurredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, Clock: ClockFunc(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }), ReferenceTimezone: time.UTC}
	decision, err := New(registry).Decide(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != DecisionBlock || len(decision.Results) != 3 {
		t.Fatalf("decision=%#v", decision)
	}
	if !decision.Results[0].Evaluated || decision.Results[1].Evaluated || decision.Results[2].Evaluated {
		t.Fatalf("evaluation markers=%#v", decision.Results)
	}
}

func TestRegisterDecisionRulesIncludesLegacyAndDisabledRows(t *testing.T) {
	registry := NewRegistry()
	records := []PersistedRule{
		{Code: "LEGACY_RULE", Version: "legacy-1", Category: CategoryDetection, Mode: ModeEnforce, Enabled: true, Severity: "MEDIUM", Action: "ALERT"},
		{Code: "DISABLED_RULE", Version: "1", Category: CategoryKYCLevel, Mode: ModeOff, Enabled: false, Severity: "LOW", Action: "ALERT"},
	}
	legacy := testRule{code: "LEGACY_RULE", category: CategoryDetection}
	if err := RegisterDecisionRules(registry, records, []Rule{legacy}); err != nil {
		t.Fatal(err)
	}
	if entry, ok := registry.Get("LEGACY_RULE"); !ok || entry.Mode != ModeEnforce {
		t.Fatalf("legacy entry=%#v, exists=%v", entry, ok)
	}
	if entry, ok := registry.Get("DISABLED_RULE"); !ok || entry.Enabled {
		t.Fatalf("disabled entry=%#v, exists=%v", entry, ok)
	}
}

func TestEvalContextValidate(t *testing.T) {
	valid := EvalContext{
		Operation: Operation{
			PersonID: 1, AmountMinor: 2500, Currency: "USD",
			OccurredAt: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
		},
		Clock:             ClockFunc(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }),
		ReferenceTimezone: time.UTC,
	}

	tests := []struct {
		name    string
		context EvalContext
		wantErr error
	}{
		{name: "valid context", context: valid},
		{name: "clock required", context: func() EvalContext { c := valid; c.Clock = nil; return c }(), wantErr: ErrMissingClock},
		{name: "reference timezone required", context: func() EvalContext { c := valid; c.ReferenceTimezone = nil; return c }(), wantErr: ErrMissingReferenceTimezone},
		{name: "person required", context: func() EvalContext { c := valid; c.Operation.PersonID = 0; return c }()},
		{name: "integer amount cannot be negative", context: func() EvalContext { c := valid; c.Operation.AmountMinor = -1; return c }()},
		{name: "currency must be canonical code", context: func() EvalContext { c := valid; c.Operation.Currency = "usd"; return c }()},
		{name: "history currency validated", context: func() EvalContext {
			c := valid
			c.History = []HistoricalOperation{{AmountMinor: 10, Currency: "US", OccurredAt: valid.Operation.OccurredAt}}
			return c
		}()},
		{name: "operation time required", context: func() EvalContext { c := valid; c.Operation.OccurredAt = time.Time{}; return c }()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.context.Validate()
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Validate() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if tt.name == "valid context" && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if tt.name != "valid context" && err == nil {
				t.Fatal("Validate() expected an error")
			}
		})
	}
}

func TestRegistryRegisterLookupAndMode(t *testing.T) {
	registry := NewRegistry()
	rule := testRule{code: "CAP_JOUR", category: CategoryHardBlock}
	if err := registry.Register(rule, "1.0.0", ModeShadow); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := registry.Register(rule, "1.0.0", ModeEnforce); !errors.Is(err, ErrDuplicateRule) {
		t.Fatalf("duplicate Register() error = %v, want ErrDuplicateRule", err)
	}
	entry, ok := registry.Get("CAP_JOUR")
	if !ok || entry.Version != "1.0.0" || entry.Mode != ModeShadow || entry.Category != CategoryHardBlock {
		t.Fatalf("Get() = %#v, %v", entry, ok)
	}
	if err := registry.SetMode("CAP_JOUR", ModeEnforce); err != nil {
		t.Fatalf("SetMode() error = %v", err)
	}
	entry, _ = registry.Get("CAP_JOUR")
	if entry.Mode != ModeEnforce {
		t.Fatalf("mode = %q, want %q", entry.Mode, ModeEnforce)
	}
	if got := registry.List(CategoryScreening); len(got) != 0 {
		t.Fatalf("List(screening) = %#v, want empty", got)
	}
	if got := registry.List(CategoryHardBlock); len(got) != 1 || got[0].Rule.Code() != "CAP_JOUR" {
		t.Fatalf("List(hard block) = %#v", got)
	}
}

func TestRegistryRejectsInvalidRegistration(t *testing.T) {
	tests := []struct {
		name    string
		rule    Rule
		version string
		mode    Mode
	}{
		{name: "missing version", rule: testRule{code: "R1", category: CategoryDetection}, mode: ModeOff},
		{name: "invalid mode", rule: testRule{code: "R1", category: CategoryDetection}, version: "1", mode: "ON"},
		{name: "invalid category", rule: testRule{code: "R1", category: "UNKNOWN"}, version: "1", mode: ModeOff},
		{name: "untrimmed code", rule: testRule{code: " R1", category: CategoryDetection}, version: "1", mode: ModeOff},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := NewRegistry().Register(tt.rule, tt.version, tt.mode); err == nil {
				t.Fatal("Register() expected an error")
			}
		})
	}
}
