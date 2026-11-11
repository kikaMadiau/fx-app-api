package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strings"
	"time"
)

// PersistedRule is the engine-facing projection of one aml_rules row.
type PersistedRule struct {
	Code       string          `json:"code"`
	Version    string          `json:"version"`
	Category   Category        `json:"category"`
	Mode       Mode            `json:"mode"`
	Enabled    bool            `json:"enabled"`
	Score      int             `json:"score"`
	Severity   string          `json:"severity"`
	Action     string          `json:"action"`
	Parameters json.RawMessage `json:"parameters"`
}

type parameterizedRule struct {
	code     string
	category Category
	evaluate func(context.Context, EvalContext) (Result, error)
}

func (r *parameterizedRule) Code() string       { return r.code }
func (r *parameterizedRule) Category() Category { return r.category }

func (r *parameterizedRule) Evaluate(ctx context.Context, input EvalContext) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("AML rule evaluation requires a context")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := input.Validate(); err != nil {
		return Result{}, err
	}
	return r.evaluate(ctx, input)
}

// RegisterShadowRules validates active database records and registers the new rules in SHADOW.
// A newly introduced rule cannot be activated in ENFORCE mode through this loader.
func RegisterShadowRules(registry *Registry, records []PersistedRule) error {
	return registerConfiguredRules(registry, records, true)
}

// RegisterDecisionRules registers every enabled, effective mode for the transaction pipeline.
func RegisterDecisionRules(registry *Registry, records []PersistedRule, legacyRules ...[]Rule) error {
	var legacy []Rule
	if len(legacyRules) > 0 {
		legacy = legacyRules[0]
	}
	if registry == nil {
		return errors.New("AML registry must not be nil")
	}
	legacyByCode := make(map[string]Rule, len(legacy))
	for _, rule := range legacy {
		legacyByCode[rule.Code()] = rule
	}
	for _, record := range records {
		var rule Rule
		if !record.Enabled || record.Mode == ModeOff {
			rule = &inactiveRule{code: record.Code, category: record.Category}
		} else if isParameterizedRule(record.Code) {
			parsed, err := newParameterizedRule(record)
			if err != nil {
				return err
			}
			rule = parsed
		} else if legacyRule, ok := legacyByCode[record.Code]; ok {
			if legacyRule.Category() != record.Category {
				return fmt.Errorf("legacy AML rule %q category mismatch", record.Code)
			}
			rule = legacyRule
		} else {
			return fmt.Errorf("enabled AML rule %q has no registered implementation", record.Code)
		}
		mode := record.Mode
		if err := registry.Register(rule, record.Version, mode, RuleMetadata{Severity: record.Severity, Action: record.Action, Enabled: record.Enabled, Parameters: record.Parameters}); err != nil {
			return err
		}
	}
	return nil
}

type inactiveRule struct {
	code     string
	category Category
}

func (r *inactiveRule) Code() string       { return r.code }
func (r *inactiveRule) Category() Category { return r.category }
func (r *inactiveRule) Evaluate(context.Context, EvalContext) (Result, error) {
	return passResult(), nil
}

func isParameterizedRule(code string) bool {
	switch code {
	case "CAP_JOUR", "CUMUL_ALERTE", "FRACTIONNEMENT", "ALLER_RETOUR", "FT_SCHEMA", "SANCTIONS", "IDENT_COMPLETE", "AGREMENT_ACTIF", "OPERATION_INTERDITE", "ECART_TAUX":
		return true
	default:
		return false
	}
}

func IsParameterizedRule(code string) bool { return isParameterizedRule(code) }

func registerConfiguredRules(registry *Registry, records []PersistedRule, shadowOnly bool) error {
	if registry == nil {
		return errors.New("AML registry must not be nil")
	}
	for _, record := range records {
		if shadowOnly && (!record.Enabled || record.Mode == ModeOff) {
			continue
		}
		if record.Mode != ModeOff && record.Mode != ModeShadow && (shadowOnly || record.Mode != ModeEnforce) {
			return fmt.Errorf("AML rule %q has unsupported mode %q", record.Code, record.Mode)
		}
		rule, err := newParameterizedRule(record)
		if err != nil {
			return err
		}
		mode := record.Mode
		if !record.Enabled {
			mode = ModeOff
		}
		if err := registry.Register(rule, record.Version, mode, RuleMetadata{Severity: record.Severity, Action: record.Action, Enabled: record.Enabled, Parameters: record.Parameters}); err != nil {
			return err
		}
	}
	return nil
}

func newParameterizedRule(record PersistedRule) (Rule, error) {
	if strings.TrimSpace(record.Code) == "" || strings.TrimSpace(record.Version) == "" {
		return nil, errors.New("configured AML rule requires code and version")
	}
	if record.Score < 0 {
		return nil, fmt.Errorf("AML rule %q score cannot be negative", record.Code)
	}
	if record.Severity == "" {
		return nil, fmt.Errorf("AML rule %q severity is required", record.Code)
	}
	if !validAction(record.Action) {
		return nil, fmt.Errorf("AML rule %q has invalid action %q", record.Code, record.Action)
	}
	base := func(category Category, evaluate func(context.Context, EvalContext) (Result, error)) (Rule, error) {
		if record.Category != category {
			return nil, fmt.Errorf("AML rule %q category is %q, want %q", record.Code, record.Category, category)
		}
		return &parameterizedRule{code: record.Code, category: category, evaluate: evaluate}, nil
	}

	switch record.Code {
	case "CAP_JOUR":
		var p capDailyParams
		if err := decodeRuleParameters(record.Parameters, &p); err != nil {
			return nil, ruleParameterError(record.Code, err)
		}
		if err := p.validate(); err != nil {
			return nil, ruleParameterError(record.Code, err)
		}
		return base(CategoryHardBlock, func(_ context.Context, input EvalContext) (Result, error) {
			total, err := dailyTotal(input, p.Scope, p.AmountBasis, p.CancelledPolicy)
			if err != nil {
				return Result{}, err
			}
			if total < p.LimitUSDMinor {
				return passResult(), nil
			}
			outcome, severity := actionOutcome(record.Action), record.Severity
			if p.Scope == "ALL_OPERATORS" && p.CrossOperatorAction != "BLOCK" {
				outcome, severity = actionOutcome(p.CrossOperatorAction), p.CrossOperatorSeverity
			}
			return parameterResult(record, outcome, fmt.Sprintf("Daily amount %s reached configured cap %s USD", formatMinor(total), formatMinor(p.LimitUSDMinor)), map[string]any{
				"total_usd_minor": total, "limit_usd_minor": p.LimitUSDMinor, "scope": p.Scope, "severity": severity,
			}), nil
		})
	case "CUMUL_ALERTE":
		var p cumulativeAlertParams
		if err := decodeRuleParameters(record.Parameters, &p); err != nil {
			return nil, ruleParameterError(record.Code, err)
		}
		if err := p.validate(); err != nil {
			return nil, ruleParameterError(record.Code, err)
		}
		return base(CategoryDetection, func(_ context.Context, input EvalContext) (Result, error) {
			total, err := dailyTotal(input, p.Scope, p.AmountBasis, p.CancelledPolicy)
			if err != nil {
				return Result{}, err
			}
			if total >= p.BlockThresholdUSDMinor {
				return parameterResult(record, actionOutcome(p.BlockAction), "Daily cumulative amount reached configured block threshold", map[string]any{
					"total_usd_minor": total, "threshold_usd_minor": p.BlockThresholdUSDMinor, "scope": p.Scope, "severity": p.BlockSeverity,
				}), nil
			}
			if total >= p.AlertThresholdUSDMinor {
				return parameterResult(record, actionOutcome(p.AlertAction), "Daily cumulative amount reached configured alert threshold", map[string]any{
					"total_usd_minor": total, "threshold_usd_minor": p.AlertThresholdUSDMinor, "scope": p.Scope,
				}), nil
			}
			return passResult(), nil
		})
	case "FRACTIONNEMENT":
		var p fractioningParams
		if err := decodeRuleParameters(record.Parameters, &p); err != nil {
			return nil, ruleParameterError(record.Code, err)
		}
		if err := p.validate(); err != nil {
			return nil, ruleParameterError(record.Code, err)
		}
		return base(CategoryDetection, func(_ context.Context, input EvalContext) (Result, error) {
			ops, err := inWindow(input, p.Window, p.AmountBasis, p.CancelledPolicy)
			if err != nil {
				return Result{}, err
			}
			var total int64
			operators := map[int64]bool{}
			dayOperators := map[int64]bool{}
			currentDay := input.Operation.OccurredAt.In(input.ReferenceTimezone).Format("2006-01-02")
			for _, op := range ops {
				total, err = checkedAdd(total, op.amount)
				if err != nil {
					return Result{}, err
				}
				operators[op.operatorID] = true
				if op.time.In(input.ReferenceTimezone).Format("2006-01-02") == currentDay {
					dayOperators[op.operatorID] = true
				}
			}
			reasons := make([]string, 0, 3)
			if len(ops) >= p.MinimumCount && total >= p.MinimumTotalUSDMinor {
				reasons = append(reasons, "configured operation count and cumulative amount thresholds reached")
			}
			if p.MinimumDistinctOperators != nil && distinctPositiveOperators(dayOperators) >= *p.MinimumDistinctOperators {
				reasons = append(reasons, "configured multi-operator threshold reached")
			}
			if p.SharedIdentityMinimumPersonCount != nil && sharedIdentityCount(input.IdentityLinks) >= *p.SharedIdentityMinimumPersonCount {
				reasons = append(reasons, "configured shared-identity threshold reached")
			}
			if p.NearThreshold != nil {
				count := countNearThreshold(ops, p.NearThreshold)
				if count >= p.NearThreshold.MinimumCount {
					reasons = append(reasons, "configured repeated near-threshold amount band reached")
				}
			}
			if p.Cumulative7DUSDMinor != nil {
				sevenDay, err := sumWindow(input, p.Cumulative7DWindow, p.AmountBasis, p.CancelledPolicy)
				if err != nil {
					return Result{}, err
				}
				if sevenDay >= *p.Cumulative7DUSDMinor {
					reasons = append(reasons, "configured seven-day cumulative threshold reached")
				}
			}
			if p.Cumulative30DUSDMinor != nil {
				thirtyDay, err := sumWindow(input, p.Cumulative30DWindow, p.AmountBasis, p.CancelledPolicy)
				if err != nil {
					return Result{}, err
				}
				if thirtyDay >= *p.Cumulative30DUSDMinor {
					reasons = append(reasons, "configured thirty-day cumulative threshold reached")
				}
			}
			if len(reasons) == 0 {
				return passResult(), nil
			}
			return parameterResult(record, OutcomeAlert, strings.Join(reasons, "; "), map[string]any{
				"operation_count": len(ops), "total_usd_minor": total, "distinct_operator_count": distinctPositiveOperators(operators),
				"shared_identity_person_count": sharedIdentityCount(input.IdentityLinks),
			}), nil
		})
	case "ALLER_RETOUR":
		var p roundTripParams
		if err := decodeRuleParameters(record.Parameters, &p); err != nil {
			return nil, ruleParameterError(record.Code, err)
		}
		if err := p.validate(); err != nil {
			return nil, ruleParameterError(record.Code, err)
		}
		return base(CategoryDetection, func(_ context.Context, input EvalContext) (Result, error) {
			current := input.Operation
			if p.CancelledPolicy == "EXCLUDE" && strings.EqualFold(current.Status, "CANCELLED") {
				return passResult(), nil
			}
			currentAmount, err := selectedUSD(current.GrossUSDMinor, current.NetUSDMinor, p.AmountBasis)
			if err != nil {
				return Result{}, err
			}
			if currentAmount < p.MinimumAmountUSDMinor {
				return passResult(), nil
			}
			pairSince := input.CurrentTime().Add(-p.PairWindow)
			cycleSince := input.CurrentTime().Add(-p.CycleWindow)
			matches := 0
			for _, previous := range input.History {
				if previous.ID != 0 && previous.ID == current.ID || previous.OccurredAt.Before(pairSince) {
					continue
				}
				if p.CancelledPolicy == "EXCLUDE" && strings.EqualFold(previous.Status, "CANCELLED") {
					continue
				}
				if !sameCurrencyPair(current.Currency, current.TargetCurrency, previous.Currency, previous.TargetCurrency) || strings.EqualFold(current.Type, previous.Type) {
					continue
				}
				previousAmount, err := selectedUSD(previous.GrossUSDMinor, previous.NetUSDMinor, p.AmountBasis)
				if err != nil {
					return Result{}, err
				}
				if previousAmount < p.MinimumAmountUSDMinor || !withinGapBPS(currentAmount, previousAmount, p.MaximumAmountGapBPS) {
					continue
				}
				if !previous.OccurredAt.Before(cycleSince) {
					matches++
				}
			}
			if matches < p.MinimumCycles {
				return passResult(), nil
			}
			return parameterResult(record, OutcomeAlert, "Configured repeated opposite-trade cycle threshold reached", map[string]any{
				"matching_opposite_operations": matches, "minimum_cycles": p.MinimumCycles,
				"currency_pair": []string{current.Currency, current.TargetCurrency},
			}), nil
		})
	case "FT_SCHEMA":
		var p ftSchemaParams
		if err := decodeRuleParameters(record.Parameters, &p); err != nil {
			return nil, ruleParameterError(record.Code, err)
		}
		if err := p.validate(); err != nil {
			return nil, ruleParameterError(record.Code, err)
		}
		return base(CategoryDetection, func(_ context.Context, input EvalContext) (Result, error) {
			ops, err := inWindow(input, p.Window, p.AmountBasis, p.CancelledPolicy)
			if err != nil {
				return Result{}, err
			}
			small := make([]windowOperation, 0, len(ops))
			for _, op := range ops {
				if op.amount < p.MaximumSingleAmountUSDMinor {
					small = append(small, op)
				}
			}
			if len(small) < p.MinimumOperations {
				return passResult(), nil
			}
			linkedBy := matchingFTLinks(input, small, p.LinkedBy)
			if len(linkedBy) == 0 {
				return passResult(), nil
			}
			score := record.Score
			zoneFactor := false
			for _, zone := range p.RiskZones {
				if zone == input.Operation.RiskZone {
					zoneFactor = true
					score += *p.RiskZoneScore
					break
				}
			}
			result := parameterResult(record, OutcomeAlert, "Configured repeated small-operation link threshold reached", map[string]any{
				"small_operation_count": len(small), "linked_by": linkedBy, "risk_zone_factor_applied": zoneFactor,
			})
			result.Score = score
			return result, nil
		})
	case "SANCTIONS":
		var p sanctionsParams
		if err := decodeRuleParameters(record.Parameters, &p); err != nil {
			return nil, ruleParameterError(record.Code, err)
		}
		if err := p.validate(); err != nil {
			return nil, ruleParameterError(record.Code, err)
		}
		return base(CategoryScreening, func(_ context.Context, input EvalContext) (Result, error) {
			if !input.SanctionsScreeningAvailable {
				return Result{}, fmt.Errorf("%w: sanctions screening source is unavailable", ErrMissingRequiredInput)
			}
			matches := make([]SanctionsMatch, 0)
			sources := stringSet(p.SourceIDs)
			for _, match := range input.SanctionsMatches {
				if sources[match.SourceID] {
					matches = append(matches, match)
				}
			}
			if len(matches) == 0 {
				return passResult(), nil
			}
			return parameterResult(record, actionOutcome(record.Action), "Sanctions screening match requires blocking review", map[string]any{
				"matches": matches, "cenaref_draft_required": true,
			}), nil
		})
	case "IDENT_COMPLETE":
		var p identityCompleteParams
		if err := decodeRuleParameters(record.Parameters, &p); err != nil {
			return nil, ruleParameterError(record.Code, err)
		}
		if err := p.validate(); err != nil {
			return nil, ruleParameterError(record.Code, err)
		}
		return base(CategoryHardBlock, func(_ context.Context, input EvalContext) (Result, error) {
			missing := make([]string, 0, 6)
			if *p.RequireCustomer && !input.Identity.CustomerIdentified {
				missing = append(missing, "customer")
			}
			if *p.RequireBeneficialOwner && !input.Identity.BeneficialOwnerIdentified {
				missing = append(missing, "beneficial_owner")
			}
			if *p.RequireDocument && !input.Identity.IdentityDocumentPresented {
				missing = append(missing, "identity_document")
			}
			if *p.RequireValidDocument && !input.Identity.IdentityDocumentValid {
				missing = append(missing, "valid_identity_document")
			}
			if *p.RequireAddress && !input.Identity.AddressPresent {
				missing = append(missing, "address")
			}
			if *p.RequirePhoto && !input.Identity.PhotoPresent {
				missing = append(missing, "photo")
			}
			if len(missing) == 0 {
				return passResult(), nil
			}
			return parameterResult(record, actionOutcome(record.Action), "Required identity information is incomplete", map[string]any{"missing": missing}), nil
		})
	case "AGREMENT_ACTIF":
		var p activeLicenseParams
		if err := decodeRuleParameters(record.Parameters, &p); err != nil {
			return nil, ruleParameterError(record.Code, err)
		}
		if err := p.validate(); err != nil {
			return nil, ruleParameterError(record.Code, err)
		}
		return base(CategoryHardBlock, func(_ context.Context, input EvalContext) (Result, error) {
			allowed := stringSet(p.ActiveStatuses)
			if allowed[input.License.OperatorStatus] && allowed[input.License.StoreStatus] {
				return passResult(), nil
			}
			return parameterResult(record, actionOutcome(record.Action), "Operator or exchange office is not in an allowed active status", map[string]any{
				"operator_status": input.License.OperatorStatus, "store_status": input.License.StoreStatus,
			}), nil
		})
	case "OPERATION_INTERDITE":
		var p prohibitedOperationParams
		if err := decodeRuleParameters(record.Parameters, &p); err != nil {
			return nil, ruleParameterError(record.Code, err)
		}
		if err := p.validate(); err != nil {
			return nil, ruleParameterError(record.Code, err)
		}
		return base(CategoryHardBlock, func(_ context.Context, input EvalContext) (Result, error) {
			if !stringSet(p.ProhibitedTypes)[input.Operation.OperationClass] {
				return passResult(), nil
			}
			return parameterResult(record, actionOutcome(record.Action), "Operation class is configured as prohibited", map[string]any{"operation_class": input.Operation.OperationClass}), nil
		})
	case "ECART_TAUX":
		var p rateGapParams
		if err := decodeRuleParameters(record.Parameters, &p); err != nil {
			return nil, ruleParameterError(record.Code, err)
		}
		if err := p.validate(); err != nil {
			return nil, ruleParameterError(record.Code, err)
		}
		return base(CategoryDetection, func(_ context.Context, input EvalContext) (Result, error) {
			if input.BCCRatePPM <= 0 || input.Operation.RatePPM <= 0 || input.BCCRateAt.IsZero() {
				return Result{}, fmt.Errorf("%w: operation rate and BCC indicative rate are required for ECART_TAUX", ErrMissingRequiredInput)
			}
			if input.BCCRateAt.In(input.ReferenceTimezone).Format("2006-01-02") != input.Operation.OccurredAt.In(input.ReferenceTimezone).Format("2006-01-02") {
				return Result{}, errors.New("BCC indicative rate must be from the operation's reference calendar day")
			}
			gap, err := deviationBPS(input.Operation.RatePPM, input.BCCRatePPM)
			if err != nil {
				return Result{}, err
			}
			if gap <= int64(p.MaximumDeviationBPS) {
				return passResult(), nil
			}
			return parameterResult(record, actionOutcome(record.Action), "Operation rate deviation exceeded configured threshold", map[string]any{
				"deviation_bps": gap, "maximum_deviation_bps": p.MaximumDeviationBPS,
				"operation_rate_ppm": input.Operation.RatePPM, "bcc_rate_ppm": input.BCCRatePPM,
			}), nil
		})
	default:
		return nil, fmt.Errorf("unknown parameterized AML rule code %q", record.Code)
	}
}

func parameterResult(record PersistedRule, outcome Outcome, reason string, evidence map[string]any) Result {
	if _, exists := evidence["severity"]; !exists {
		evidence["severity"] = record.Severity
	}
	return Result{Outcome: outcome, Score: record.Score, Reasons: []string{reason}, Evidence: evidence}
}

func actionOutcome(action string) Outcome {
	switch action {
	case "BLOCK":
		return OutcomeBlock
	case "REVIEW":
		return OutcomeReview
	default:
		return OutcomeAlert
	}
}

func validAction(action string) bool {
	switch action {
	case "SCORE_ONLY", "ALERT", "REVIEW", "BLOCK":
		return true
	default:
		return false
	}
}

func decodeRuleParameters(raw json.RawMessage, target any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return errors.New("parameters object is required")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("parameters must contain a single JSON object")
		}
		return err
	}
	return nil
}

func ruleParameterError(code string, err error) error {
	return fmt.Errorf("AML rule %q parameters: %w", code, err)
}

func checkedAdd(total, value int64) (int64, error) {
	if value > 0 && total > int64(^uint64(0)>>1)-value {
		return 0, errors.New("AML amount total overflows int64")
	}
	if value < 0 && total < -int64(^uint64(0)>>1)-1-value {
		return 0, errors.New("AML amount total overflows int64")
	}
	return total + value, nil
}

type windowOperation struct {
	amount      int64
	operatorID  int64
	storeID     int64
	beneficiary string
	phone       string
	status      string
	time        time.Time
}

func dailyTotal(input EvalContext, scope, amountBasis, cancelledPolicy string) (int64, error) {
	current := input.Operation.OccurredAt.In(input.ReferenceTimezone)
	day := current.Format("2006-01-02")
	var total int64
	addOperation := func(amount int64, operatorID int64, status string, at time.Time) error {
		if scope == "PER_OPERATOR" && operatorID != input.Operation.OperatorID {
			return nil
		}
		if cancelledPolicy == "EXCLUDE" && strings.EqualFold(status, "CANCELLED") {
			return nil
		}
		if at.In(input.ReferenceTimezone).Format("2006-01-02") != day {
			return nil
		}
		var err error
		total, err = checkedAdd(total, amount)
		return err
	}
	for _, historical := range input.History {
		if historical.ID != 0 && historical.ID == input.Operation.ID {
			continue
		}
		amount, err := selectedUSD(historical.GrossUSDMinor, historical.NetUSDMinor, amountBasis)
		if err != nil {
			return 0, err
		}
		if err := addOperation(amount, historical.OperatorID, historical.Status, historical.OccurredAt); err != nil {
			return 0, err
		}
	}
	currentAmount, err := selectedUSD(input.Operation.GrossUSDMinor, input.Operation.NetUSDMinor, amountBasis)
	if err != nil {
		return 0, err
	}
	if err := addOperation(currentAmount, input.Operation.OperatorID, input.Operation.Status, input.Operation.OccurredAt); err != nil {
		return 0, err
	}
	return total, nil
}

func inWindow(input EvalContext, window time.Duration, amountBasis, cancelledPolicy string) ([]windowOperation, error) {
	since := input.CurrentTime().Add(-window)
	operations := make([]windowOperation, 0, len(input.History)+1)
	add := func(amount int64, operatorID, storeID int64, beneficiary, phone, status string, at time.Time) {
		if !at.Before(since) && !(cancelledPolicy == "EXCLUDE" && strings.EqualFold(status, "CANCELLED")) {
			operations = append(operations, windowOperation{amount: amount, operatorID: operatorID, storeID: storeID, beneficiary: beneficiary, phone: phone, status: status, time: at})
		}
	}
	for _, historical := range input.History {
		if historical.ID != 0 && historical.ID == input.Operation.ID {
			continue
		}
		amount, err := selectedUSD(historical.GrossUSDMinor, historical.NetUSDMinor, amountBasis)
		if err != nil {
			return nil, err
		}
		add(amount, historical.OperatorID, historical.StoreID, historical.BeneficiaryKey, historical.Phone, historical.Status, historical.OccurredAt)
	}
	currentAmount, err := selectedUSD(input.Operation.GrossUSDMinor, input.Operation.NetUSDMinor, amountBasis)
	if err != nil {
		return nil, err
	}
	add(currentAmount, input.Operation.OperatorID, input.Operation.StoreID, input.Operation.BeneficiaryKey, input.CustomerPhone(), input.Operation.Status, input.Operation.OccurredAt)
	return operations, nil
}

func (c EvalContext) CustomerPhone() string {
	if c.Customer == nil {
		return ""
	}
	return c.Customer.Phone
}

func sumWindow(input EvalContext, window time.Duration, amountBasis, cancelledPolicy string) (int64, error) {
	ops, err := inWindow(input, window, amountBasis, cancelledPolicy)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, op := range ops {
		total, err = checkedAdd(total, op.amount)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

func selectedUSD(gross, net int64, basis string) (int64, error) {
	selected := gross
	if basis == "NET" {
		selected = net
	}
	if selected <= 0 {
		return 0, fmt.Errorf("%w: USD equivalent amount required by AML rule is missing", ErrMissingRequiredInput)
	}
	return selected, nil
}

func sameCurrencyPair(currency, target, otherCurrency, otherTarget string) bool {
	return target != "" && otherTarget != "" && strings.EqualFold(currency, otherCurrency) && strings.EqualFold(target, otherTarget)
}

func withinGapBPS(a, b int64, maximum int) bool {
	maximumAmount := a
	if b > maximumAmount {
		maximumAmount = b
	}
	if maximumAmount <= 0 {
		return false
	}
	difference := a - b
	if difference < 0 {
		difference = -difference
	}
	left := new(big.Int).Mul(big.NewInt(difference), big.NewInt(10000))
	right := new(big.Int).Mul(big.NewInt(int64(maximum)), big.NewInt(maximumAmount))
	return left.Cmp(right) <= 0
}

func deviationBPS(actual, reference int64) (int64, error) {
	if actual <= 0 || reference <= 0 {
		return 0, errors.New("rates must be positive")
	}
	difference := actual - reference
	if difference < 0 {
		difference = -difference
	}
	numerator := new(big.Int).Mul(big.NewInt(difference), big.NewInt(10000))
	quotient := new(big.Int).Quo(numerator, big.NewInt(reference))
	if !quotient.IsInt64() {
		return 0, errors.New("rate deviation overflows int64")
	}
	return quotient.Int64(), nil
}

func distinctPositiveOperators(operators map[int64]bool) int {
	count := 0
	for operatorID := range operators {
		if operatorID > 0 {
			count++
		}
	}
	return count
}

func sharedIdentityCount(signals IdentityLinkSignals) int {
	maximum := signals.PhonePersonCount
	if signals.AddressPersonCount > maximum {
		maximum = signals.AddressPersonCount
	}
	if signals.BeneficialOwnerPersonCount > maximum {
		maximum = signals.BeneficialOwnerPersonCount
	}
	return maximum
}

func countNearThreshold(operations []windowOperation, params *nearThresholdParams) int {
	count := 0
	for _, op := range operations {
		if op.amount >= params.ThresholdUSDMinor && inRatioBand(op.amount, params.ThresholdUSDMinor, params.MinimumRatioBPS, params.MaximumRatioBPS) {
			count++
		}
	}
	return count
}

func inRatioBand(amount, threshold int64, minimumBPS, maximumBPS int) bool {
	if threshold <= 0 || amount < 0 {
		return false
	}
	amountScaled := new(big.Int).Mul(big.NewInt(amount), big.NewInt(10000))
	thresholdScaled := new(big.Int).Mul(big.NewInt(threshold), big.NewInt(int64(minimumBPS)))
	if amountScaled.Cmp(thresholdScaled) < 0 {
		return false
	}
	maximumScaled := new(big.Int).Mul(big.NewInt(threshold), big.NewInt(int64(maximumBPS)))
	return amountScaled.Cmp(maximumScaled) <= 0
}

func matchingFTLinks(input EvalContext, operations []windowOperation, links []string) []string {
	matched := map[string]bool{}
	for _, dimension := range links {
		switch dimension {
		case "BENEFICIARY":
			if input.Operation.BeneficiaryKey == "" {
				continue
			}
			for _, op := range operations {
				if op.beneficiary == input.Operation.BeneficiaryKey {
					matched[dimension] = true
					break
				}
			}
		case "PHONE":
			phone := strings.TrimSpace(input.CustomerPhone())
			if phone == "" {
				continue
			}
			for _, op := range operations {
				if strings.TrimSpace(op.phone) == phone {
					matched[dimension] = true
					break
				}
			}
		case "LISTED_PERSON":
			if len(input.SanctionsMatches) > 0 {
				matched[dimension] = true
			}
		}
	}
	result := make([]string, 0, len(matched))
	for dimension := range matched {
		result = append(result, dimension)
	}
	sort.Strings(result)
	return result
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func uniqueNonEmpty(values []string) bool {
	if len(values) == 0 {
		return false
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

type capDailyParams struct {
	LimitUSDMinor         int64  `json:"limit_usd_minor"`
	Scope                 string `json:"scope"`
	CrossOperatorAction   string `json:"cross_operator_action"`
	CrossOperatorSeverity string `json:"cross_operator_severity"`
	AmountBasis           string `json:"amount_basis"`
	CancelledPolicy       string `json:"cancelled_policy"`
}

func (p capDailyParams) validate() error {
	if p.LimitUSDMinor <= 0 || !validScope(p.Scope) || !validAmountBasis(p.AmountBasis) || !validCancelledPolicy(p.CancelledPolicy) {
		return errors.New("limit, scope, amount_basis, and cancelled_policy are required and must be valid")
	}
	if p.CrossOperatorAction != "ALERT_CRITICAL" && p.CrossOperatorAction != "BLOCK" {
		return errors.New("cross_operator_action must be ALERT_CRITICAL or BLOCK")
	}
	if p.CrossOperatorAction == "ALERT_CRITICAL" && strings.TrimSpace(p.CrossOperatorSeverity) == "" {
		return errors.New("cross_operator_severity is required for ALERT_CRITICAL")
	}
	return nil
}

type cumulativeAlertParams struct {
	AlertThresholdUSDMinor int64  `json:"alert_threshold_usd_minor"`
	BlockThresholdUSDMinor int64  `json:"block_threshold_usd_minor"`
	Scope                  string `json:"scope"`
	AmountBasis            string `json:"amount_basis"`
	CancelledPolicy        string `json:"cancelled_policy"`
	AlertAction            string `json:"alert_action"`
	BlockAction            string `json:"block_action"`
	BlockSeverity          string `json:"block_severity"`
}

func (p cumulativeAlertParams) validate() error {
	if p.AlertThresholdUSDMinor <= 0 || p.BlockThresholdUSDMinor <= p.AlertThresholdUSDMinor || !validScope(p.Scope) || !validAmountBasis(p.AmountBasis) || !validCancelledPolicy(p.CancelledPolicy) || !validAction(p.AlertAction) || !validAction(p.BlockAction) || strings.TrimSpace(p.BlockSeverity) == "" {
		return errors.New("alert/block thresholds, actions, block severity, scope, amount_basis, and cancelled_policy must be valid")
	}
	return nil
}

type nearThresholdParams struct {
	ThresholdUSDMinor int64 `json:"threshold_usd_minor"`
	MinimumRatioBPS   int   `json:"minimum_ratio_bps"`
	MaximumRatioBPS   int   `json:"maximum_ratio_bps"`
	MinimumCount      int   `json:"minimum_count"`
}

type fractioningParams struct {
	Window                           time.Duration        `json:"-"`
	WindowText                       string               `json:"window"`
	MinimumCount                     int                  `json:"minimum_count"`
	MinimumTotalUSDMinor             int64                `json:"minimum_total_usd_minor"`
	MinimumDistinctOperators         *int                 `json:"minimum_distinct_operators,omitempty"`
	SharedIdentityMinimumPersonCount *int                 `json:"shared_identity_minimum_person_count,omitempty"`
	NearThreshold                    *nearThresholdParams `json:"near_threshold,omitempty"`
	Cumulative7DUSDMinor             *int64               `json:"cumulative_7d_usd_minor,omitempty"`
	Cumulative7DWindow               time.Duration        `json:"-"`
	Cumulative7DWindowText           string               `json:"cumulative_7d_window,omitempty"`
	Cumulative30DUSDMinor            *int64               `json:"cumulative_30d_usd_minor,omitempty"`
	Cumulative30DWindow              time.Duration        `json:"-"`
	Cumulative30DWindowText          string               `json:"cumulative_30d_window,omitempty"`
	AmountBasis                      string               `json:"amount_basis"`
	CancelledPolicy                  string               `json:"cancelled_policy"`
}

func (p *fractioningParams) validate() error {
	window, err := time.ParseDuration(p.WindowText)
	if err != nil || window <= 0 || p.MinimumCount <= 0 || p.MinimumTotalUSDMinor <= 0 || !validAmountBasis(p.AmountBasis) || !validCancelledPolicy(p.CancelledPolicy) {
		return errors.New("window, positive count/total, amount_basis, and cancelled_policy are required")
	}
	p.Window = window
	if p.MinimumDistinctOperators != nil && *p.MinimumDistinctOperators < 2 {
		return errors.New("minimum_distinct_operators must be at least two")
	}
	if p.SharedIdentityMinimumPersonCount != nil && *p.SharedIdentityMinimumPersonCount < 2 {
		return errors.New("shared_identity_minimum_person_count must be at least two")
	}
	if p.Cumulative7DUSDMinor != nil && *p.Cumulative7DUSDMinor <= 0 || p.Cumulative30DUSDMinor != nil && *p.Cumulative30DUSDMinor <= 0 {
		return errors.New("configured cumulative thresholds must be positive")
	}
	if p.Cumulative7DUSDMinor != nil {
		p.Cumulative7DWindow, err = time.ParseDuration(p.Cumulative7DWindowText)
		if err != nil || p.Cumulative7DWindow <= 0 {
			return errors.New("cumulative_7d_window is required with cumulative_7d_usd_minor")
		}
	} else if p.Cumulative7DWindowText != "" {
		return errors.New("cumulative_7d_window requires cumulative_7d_usd_minor")
	}
	if p.Cumulative30DUSDMinor != nil {
		p.Cumulative30DWindow, err = time.ParseDuration(p.Cumulative30DWindowText)
		if err != nil || p.Cumulative30DWindow <= 0 {
			return errors.New("cumulative_30d_window is required with cumulative_30d_usd_minor")
		}
	} else if p.Cumulative30DWindowText != "" {
		return errors.New("cumulative_30d_window requires cumulative_30d_usd_minor")
	}
	if p.NearThreshold != nil {
		band := p.NearThreshold
		if band.ThresholdUSDMinor <= 0 || band.MinimumRatioBPS <= 0 || band.MaximumRatioBPS <= band.MinimumRatioBPS || band.MaximumRatioBPS >= 10000 || band.MinimumCount <= 0 {
			return errors.New("near_threshold parameters are incomplete or invalid")
		}
	}
	return nil
}

type roundTripParams struct {
	PairWindowText        string        `json:"pair_window"`
	CycleWindowText       string        `json:"cycle_window"`
	PairWindow            time.Duration `json:"-"`
	CycleWindow           time.Duration `json:"-"`
	MaximumAmountGapBPS   int           `json:"maximum_amount_gap_bps"`
	MinimumAmountUSDMinor int64         `json:"minimum_amount_usd_minor"`
	MinimumCycles         int           `json:"minimum_cycles"`
	AmountBasis           string        `json:"amount_basis"`
	CancelledPolicy       string        `json:"cancelled_policy"`
}

func (p *roundTripParams) validate() error {
	var err error
	p.PairWindow, err = time.ParseDuration(p.PairWindowText)
	if err != nil || p.PairWindow <= 0 {
		return errors.New("pair_window must be a positive duration")
	}
	p.CycleWindow, err = time.ParseDuration(p.CycleWindowText)
	if err != nil || p.CycleWindow <= 0 || p.MaximumAmountGapBPS < 0 || p.MaximumAmountGapBPS >= 10000 || p.MinimumAmountUSDMinor <= 0 || p.MinimumCycles <= 0 || !validAmountBasis(p.AmountBasis) || !validCancelledPolicy(p.CancelledPolicy) {
		return errors.New("cycle window, gap, amount, cycles, amount_basis, and cancelled_policy must be valid")
	}
	return nil
}

type ftSchemaParams struct {
	WindowText                  string        `json:"window"`
	Window                      time.Duration `json:"-"`
	MinimumOperations           int           `json:"minimum_operations"`
	MaximumSingleAmountUSDMinor int64         `json:"maximum_single_amount_usd_minor"`
	LinkedBy                    []string      `json:"linked_by"`
	RiskZones                   []string      `json:"risk_zones"`
	RiskZoneScore               *int          `json:"risk_zone_score"`
	AmountBasis                 string        `json:"amount_basis"`
	CancelledPolicy             string        `json:"cancelled_policy"`
}

func (p *ftSchemaParams) validate() error {
	window, err := time.ParseDuration(p.WindowText)
	if err != nil || window <= 0 || p.MinimumOperations <= 0 || p.MaximumSingleAmountUSDMinor <= 0 || len(p.LinkedBy) == 0 || p.RiskZoneScore == nil || *p.RiskZoneScore < 0 || !validAmountBasis(p.AmountBasis) || !validCancelledPolicy(p.CancelledPolicy) {
		return errors.New("window, operation count, amount, link dimensions, risk score, amount basis, and cancellation policy must be valid")
	}
	p.Window = window
	if !uniqueNonEmpty(p.LinkedBy) {
		return errors.New("linked_by must contain unique non-empty values")
	}
	for _, link := range p.LinkedBy {
		if link != "BENEFICIARY" && link != "PHONE" && link != "LISTED_PERSON" {
			return fmt.Errorf("unsupported linked_by dimension %q", link)
		}
	}
	if len(p.RiskZones) > 0 && !uniqueNonEmpty(p.RiskZones) {
		return errors.New("risk_zones must contain unique non-empty values")
	}
	return nil
}

type sanctionsParams struct {
	SourceIDs []string `json:"source_ids"`
}

func (p sanctionsParams) validate() error {
	if !uniqueNonEmpty(p.SourceIDs) {
		return errors.New("source_ids must contain at least one unique source")
	}
	return nil
}

type identityCompleteParams struct {
	RequireCustomer        *bool `json:"require_customer"`
	RequireBeneficialOwner *bool `json:"require_beneficial_owner"`
	RequireDocument        *bool `json:"require_document"`
	RequireValidDocument   *bool `json:"require_valid_document"`
	RequireAddress         *bool `json:"require_address"`
	RequirePhoto           *bool `json:"require_photo"`
}

func (p identityCompleteParams) validate() error {
	if p.RequireCustomer == nil || p.RequireBeneficialOwner == nil || p.RequireDocument == nil || p.RequireValidDocument == nil || p.RequireAddress == nil || p.RequirePhoto == nil {
		return errors.New("all identity completeness requirements must be explicitly configured")
	}
	return nil
}

type activeLicenseParams struct {
	ActiveStatuses []string `json:"active_statuses"`
}

func (p activeLicenseParams) validate() error {
	if !uniqueNonEmpty(p.ActiveStatuses) {
		return errors.New("active_statuses must contain at least one unique status")
	}
	return nil
}

type prohibitedOperationParams struct {
	ProhibitedTypes []string `json:"prohibited_types"`
}

func (p prohibitedOperationParams) validate() error {
	if !uniqueNonEmpty(p.ProhibitedTypes) {
		return errors.New("prohibited_types must contain at least one unique operation type")
	}
	return nil
}

type rateGapParams struct {
	MaximumDeviationBPS int `json:"maximum_deviation_bps"`
}

func (p rateGapParams) validate() error {
	if p.MaximumDeviationBPS <= 0 {
		return errors.New("maximum_deviation_bps must be configured and positive")
	}
	return nil
}

func validScope(scope string) bool            { return scope == "PER_OPERATOR" || scope == "ALL_OPERATORS" }
func validAmountBasis(basis string) bool      { return basis == "GROSS" || basis == "NET" }
func validCancelledPolicy(policy string) bool { return policy == "COUNT" || policy == "EXCLUDE" }
