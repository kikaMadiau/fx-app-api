package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type DecisionOutcome string

const (
	DecisionAccept             DecisionOutcome = "ACCEPT"
	DecisionAcceptWithApproval DecisionOutcome = "ACCEPT_WITH_APPROVAL"
	DecisionBlock              DecisionOutcome = "BLOCK"
)

// RuleDecision is persisted for every registered rule, including rules that PASS.
type RuleDecision struct {
	Code       string          `json:"code"`
	Version    string          `json:"version"`
	Category   Category        `json:"category"`
	Mode       Mode            `json:"mode"`
	Severity   string          `json:"severity,omitempty"`
	Action     string          `json:"action,omitempty"`
	Parameters json.RawMessage `json:"parameters,omitempty"`
	Enabled    bool            `json:"enabled"`
	Evaluated  bool            `json:"evaluated"`
	Result     Result          `json:"result"`
}

type Decision struct {
	Outcome DecisionOutcome `json:"outcome"`
	Score   int             `json:"score"`
	Results []RuleDecision  `json:"results"`
}

// Engine runs rules in the required category order. SHADOW results are recorded but do not
// affect the decision. A hard block stops evaluation and leaves the remaining journal rows
// marked unevaluated so the decision still captures the complete effective catalogue.
type Engine struct{ Registry *Registry }

func New(registry *Registry) *Engine { return &Engine{Registry: registry} }

func (e *Engine) Decide(ctx context.Context, input EvalContext) (Decision, error) {
	if e == nil || e.Registry == nil {
		return Decision{}, errors.New("AML engine requires a registry")
	}
	if ctx == nil {
		return Decision{}, errors.New("AML decision requires a context")
	}
	if err := input.Validate(); err != nil {
		return Decision{}, err
	}
	decision := Decision{Outcome: DecisionAccept, Results: make([]RuleDecision, 0)}
	categories := []Category{CategoryHardBlock, CategoryScreening, CategoryKYCLevel, CategoryDetection}
	for categoryIndex, category := range categories {
		entries := e.Registry.List(category)
		for ruleIndex, registered := range entries {
			if err := ctx.Err(); err != nil {
				return Decision{}, err
			}
			if registered.Mode == ModeOff || !registered.Enabled {
				decision.Results = append(decision.Results, RuleDecision{Code: registered.Rule.Code(), Version: registered.Version, Category: registered.Category, Mode: registered.Mode, Severity: registered.Severity, Action: registered.Action, Parameters: registered.Parameters, Enabled: registered.Enabled, Evaluated: false})
				continue
			}
			result, err := registered.Rule.Evaluate(ctx, input)
			if err != nil {
				if registered.Mode != ModeShadow {
					return Decision{}, fmt.Errorf("evaluate AML rule %s: %w", registered.Rule.Code(), err)
				}
				result = Result{Outcome: OutcomePass, Evidence: map[string]any{"evaluation_skipped": true, "evaluation_error": err.Error()}}
			}
			if !result.Outcome.Valid() {
				return Decision{}, fmt.Errorf("AML rule %s returned invalid outcome %q", registered.Rule.Code(), result.Outcome)
			}
			decision.Results = append(decision.Results, RuleDecision{Code: registered.Rule.Code(), Version: registered.Version, Category: registered.Category, Mode: registered.Mode, Severity: registered.Severity, Action: registered.Action, Parameters: registered.Parameters, Enabled: registered.Enabled, Evaluated: true, Result: result})
			if registered.Mode != ModeEnforce {
				continue
			}
			if category == CategoryDetection {
				decision.Score += result.Score
			}
			switch result.Outcome {
			case OutcomeBlock:
				decision.Outcome = DecisionBlock
			case OutcomeReview, OutcomeRequireApproval:
				if decision.Outcome != DecisionBlock {
					decision.Outcome = DecisionAcceptWithApproval
				}
			}
			if category == CategoryHardBlock && registered.Mode == ModeEnforce && result.Outcome == OutcomeBlock {
				for _, skipped := range entries[ruleIndex+1:] {
					decision.Results = append(decision.Results, RuleDecision{Code: skipped.Rule.Code(), Version: skipped.Version, Category: skipped.Category, Mode: skipped.Mode, Severity: skipped.Severity, Action: skipped.Action, Parameters: skipped.Parameters, Enabled: skipped.Enabled})
				}
				for _, laterCategory := range categories[categoryIndex+1:] {
					for _, skipped := range e.Registry.List(laterCategory) {
						decision.Results = append(decision.Results, RuleDecision{Code: skipped.Rule.Code(), Version: skipped.Version, Category: skipped.Category, Mode: skipped.Mode, Severity: skipped.Severity, Action: skipped.Action, Parameters: skipped.Parameters, Enabled: skipped.Enabled})
					}
				}
				return decision, nil
			}
		}
	}
	return decision, nil
}
