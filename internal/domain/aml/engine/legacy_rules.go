package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"fx-app-api/internal/domain/customer/kycrisk"
	customerservice "fx-app-api/internal/domain/customer/service"
)

// LegacyRuleConfig carries the old risk-rules.json values after exact conversion to the
// engine's integer-cent amount representation.
type LegacyRuleConfig struct {
	Code           string
	Score          int
	ThresholdMinor int64
	ThresholdCount int
	Window         time.Duration
	MarginBPS      int
	Alert          bool
}

type legacyRule struct {
	code     string
	category Category
	evaluate func(context.Context, EvalContext) (Result, error)
}

func (r *legacyRule) Code() string       { return r.code }
func (r *legacyRule) Category() Category { return r.category }

func (r *legacyRule) Evaluate(ctx context.Context, input EvalContext) (Result, error) {
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

// BuildLegacyRules ports the transaction_rules and customer_rules from the former risk
// catalogue. Monetary values in threshold_amount are interpreted as major currency units
// and converted exactly to cents. Unknown legacy rule types remain ignored, as before.
func BuildLegacyRules(data []byte) ([]Rule, error) {
	var catalogue struct {
		TransactionRules []map[string]json.RawMessage `json:"transaction_rules"`
		CustomerRules    []map[string]json.RawMessage `json:"customer_rules"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&catalogue); err != nil {
		return nil, fmt.Errorf("decode legacy AML rule catalogue: %w", err)
	}

	var rules []Rule
	for _, section := range []struct {
		definitions []map[string]json.RawMessage
		category    Category
	}{
		{catalogue.TransactionRules, CategoryDetection},
		{catalogue.CustomerRules, CategoryKYCLevel},
	} {
		for _, definition := range section.definitions {
			config, ruleType, enabled, err := parseLegacyRuleConfig(definition)
			if err != nil {
				return nil, err
			}
			if !enabled {
				continue
			}
			rule, known := makeLegacyRule(ruleType, section.category, config)
			if known {
				rules = append(rules, rule)
			}
		}
	}
	return rules, nil
}

func parseLegacyRuleConfig(raw map[string]json.RawMessage) (LegacyRuleConfig, string, bool, error) {
	var config LegacyRuleConfig
	if value, ok := raw["enabled"]; ok {
		var enabled bool
		if err := json.Unmarshal(value, &enabled); err != nil {
			return config, "", false, fmt.Errorf("legacy AML rule enabled: %w", err)
		}
		if !enabled {
			return config, "", false, nil
		}
	}
	if err := json.Unmarshal(raw["name"], &config.Code); err != nil || strings.TrimSpace(config.Code) == "" {
		return config, "", false, errors.New("legacy AML rule name is required")
	}
	var ruleType string
	if err := json.Unmarshal(raw["type"], &ruleType); err != nil || strings.TrimSpace(ruleType) == "" {
		return config, "", false, fmt.Errorf("legacy AML rule %q type is required", config.Code)
	}
	config.Alert = true
	if value, ok := raw["alert"]; ok {
		if err := json.Unmarshal(value, &config.Alert); err != nil {
			return config, "", false, fmt.Errorf("legacy AML rule %q alert: %w", config.Code, err)
		}
	}
	if value, ok := raw["score"]; ok {
		score, err := parseInteger(value)
		if err != nil || score < 0 || score > int64(^uint(0)>>1) {
			return config, "", false, fmt.Errorf("legacy AML rule %q score must be a non-negative integer", config.Code)
		}
		config.Score = int(score)
	}
	if value, ok := raw["threshold_amount"]; ok {
		amountMinor, err := parseMajorAmountMinor(value)
		if err != nil {
			return config, "", false, fmt.Errorf("legacy AML rule %q threshold_amount: %w", config.Code, err)
		}
		config.ThresholdMinor = amountMinor
	}
	if value, ok := raw["threshold_count"]; ok {
		count, err := parseInteger(value)
		if err != nil || count < 0 || count > int64(^uint(0)>>1) {
			return config, "", false, fmt.Errorf("legacy AML rule %q threshold_count must be a non-negative integer", config.Code)
		}
		config.ThresholdCount = int(count)
	}
	if value, ok := raw["window"]; ok {
		var window string
		if err := json.Unmarshal(value, &window); err != nil {
			return config, "", false, fmt.Errorf("legacy AML rule %q window: %w", config.Code, err)
		}
		if window != "" {
			parsed, err := time.ParseDuration(window)
			if err != nil {
				return config, "", false, fmt.Errorf("legacy AML rule %q window: %w", config.Code, err)
			}
			config.Window = parsed
		}
	}
	config.MarginBPS = 2000 // Former default margin was 0.2.
	if value, ok := raw["margin"]; ok {
		marginBPS, err := parseBasisPoints(value)
		if err != nil {
			return config, "", false, fmt.Errorf("legacy AML rule %q margin: %w", config.Code, err)
		}
		if marginBPS > 0 && marginBPS < 10000 {
			config.MarginBPS = int(marginBPS)
		}
	}
	return config, ruleType, true, nil
}

func parseInteger(raw json.RawMessage) (int64, error) {
	rational, ok := new(big.Rat).SetString(string(raw))
	if !ok || !rational.IsInt() || !rational.Num().IsInt64() {
		return 0, errors.New("must be an integer")
	}
	return rational.Num().Int64(), nil
}

func parseMajorAmountMinor(raw json.RawMessage) (int64, error) {
	rational, ok := new(big.Rat).SetString(string(raw))
	if !ok {
		return 0, errors.New("must be a decimal amount")
	}
	rational.Mul(rational, big.NewRat(100, 1))
	if !rational.IsInt() || !rational.Num().IsInt64() {
		return 0, errors.New("amount is not exactly representable in cents")
	}
	return rational.Num().Int64(), nil
}

func parseBasisPoints(raw json.RawMessage) (int64, error) {
	rational, ok := new(big.Rat).SetString(string(raw))
	if !ok {
		return 0, errors.New("must be a decimal fraction")
	}
	rational.Mul(rational, big.NewRat(10000, 1))
	if !rational.IsInt() || !rational.Num().IsInt64() {
		return 0, errors.New("fraction is not exactly representable in basis points")
	}
	return rational.Num().Int64(), nil
}

func makeLegacyRule(ruleType string, category Category, config LegacyRuleConfig) (Rule, bool) {
	newRule := func(evaluate func(context.Context, EvalContext) (Result, error)) (Rule, bool) {
		return &legacyRule{code: config.Code, category: category, evaluate: evaluate}, true
	}
	switch ruleType {
	case "high_amount":
		return newRule(func(_ context.Context, input EvalContext) (Result, error) {
			if input.Operation.AmountMinor <= config.ThresholdMinor {
				return passResult(), nil
			}
			return alertResult(config, fmt.Sprintf("Transaction amount %s exceeds threshold %s", formatMinor(input.Operation.AmountMinor), formatMinor(config.ThresholdMinor))), nil
		})
	case "cumulative_volume":
		return newRule(func(_ context.Context, input EvalContext) (Result, error) {
			total, err := sumMinorSince(input, input.CurrentTime().Add(-config.Window))
			if err != nil {
				return Result{}, err
			}
			if total <= config.ThresholdMinor {
				return passResult(), nil
			}
			return alertResult(config, fmt.Sprintf("Cumulative amount %s exceeds threshold %s over %s", formatMinor(total), formatMinor(config.ThresholdMinor), config.Window)), nil
		})
	case "transaction_frequency":
		return newRule(func(_ context.Context, input EvalContext) (Result, error) {
			count := countSince(input, input.CurrentTime().Add(-config.Window))
			if count <= config.ThresholdCount {
				return passResult(), nil
			}
			return alertResult(config, fmt.Sprintf("Transaction count %d exceeds threshold %d over %s", count, config.ThresholdCount, config.Window)), nil
		})
	case "structuring":
		return newRule(func(_ context.Context, input EvalContext) (Result, error) {
			threshold := config.ThresholdMinor
			if threshold <= 0 || config.ThresholdCount <= 0 || !inStructuringBand(input.Operation.AmountMinor, threshold, config.MarginBPS) {
				return passResult(), nil
			}
			since := input.CurrentTime().Add(-config.Window)
			count, countedCurrent := 0, false
			for _, history := range input.History {
				if history.OccurredAt.Before(since) || !inStructuringBand(history.AmountMinor, threshold, config.MarginBPS) {
					continue
				}
				count++
				if history.ID != 0 && history.ID == input.Operation.ID {
					countedCurrent = true
				}
			}
			if !countedCurrent {
				count++
			}
			if count < config.ThresholdCount {
				return passResult(), nil
			}
			return alertResult(config, fmt.Sprintf("%d transactions just below threshold %s over %s", count, formatMinor(threshold), config.Window)), nil
		})
	case "round_trip":
		return newRule(func(_ context.Context, input EvalContext) (Result, error) {
			current := input.Operation
			since := input.CurrentTime().Add(-config.Window)
			for _, history := range input.History {
				if (history.ID != 0 && history.ID == current.ID) || history.OccurredAt.Before(since) {
					continue
				}
				if strings.EqualFold(history.Currency, current.Currency) && !strings.EqualFold(history.Type, current.Type) {
					return alertResult(config, fmt.Sprintf("Opposite %s after %s on %s within %s (transaction %d)", current.Type, history.Type, current.Currency, config.Window, history.ID)), nil
				}
			}
			return passResult(), nil
		})
	case "dormant_reactivation":
		return newRule(func(_ context.Context, input EvalContext) (Result, error) {
			if input.Signals == nil || input.Signals.PreviousTransactionAt == nil {
				return passResult(), nil
			}
			idle := input.CurrentTime().Sub(*input.Signals.PreviousTransactionAt)
			if idle < config.Window || input.Operation.AmountMinor < config.ThresholdMinor {
				return passResult(), nil
			}
			days := int(idle.Hours() / 24)
			return alertResult(config, fmt.Sprintf("Customer inactive for %d days returns with amount %s", days, formatMinor(input.Operation.AmountMinor))), nil
		})
	case "insufficient_kyc_status":
		return newRule(func(_ context.Context, input EvalContext) (Result, error) {
			if input.KYCProfile == nil {
				return alertResult(config, "Customer has no KYC profile"), nil
			}
			status := input.KYCProfile.KYCStatus
			if status != customerservice.Verified && status != customerservice.EnhancedVerification {
				return alertResult(config, fmt.Sprintf("KYC status %s is insufficient for transaction", status)), nil
			}
			return passResult(), nil
		})
	case "inconsistent_profile":
		return newRule(func(_ context.Context, input EvalContext) (Result, error) {
			profile := input.KYCProfile
			if profile == nil {
				return alertResult(config, "Customer has no KYC profile"), nil
			}
			switch {
			case profile.LegalNature == customerservice.LegalEntity && profile.ActivityProfile == customerservice.Personal:
				return alertResult(config, "Legal entity cannot have PERSONAL activity profile"), nil
			case profile.LegalNature == customerservice.LegalEntity && (profile.CompanyName == "" || profile.RegistrationNumber == ""):
				return alertResult(config, "Legal entity KYC is missing company name or registration number"), nil
			case profile.LegalNature == customerservice.Individual && profile.Profession == "" && profile.ActivityProfile != customerservice.Personal:
				return alertResult(config, "Individual professional/business profile is missing profession"), nil
			case profile.SourceOfFunds == "" || profile.PurposeOfOperations == "":
				return alertResult(config, "KYC profile is missing source of funds or purpose of operations"), nil
			default:
				return passResult(), nil
			}
		})
	case "business_activity":
		return newRule(func(_ context.Context, input EvalContext) (Result, error) {
			if input.KYCProfile != nil && input.KYCProfile.ActivityProfile == customerservice.Business {
				return alertResult(config, "Customer activity profile is BUSINESS"), nil
			}
			return passResult(), nil
		})
	case "expected_volume_exceeded":
		return newRule(func(_ context.Context, input EvalContext) (Result, error) {
			if input.KYCProfile == nil || input.ExpectedVolumeMinor <= 0 {
				return passResult(), nil
			}
			expected := input.ExpectedVolumeMinor
			since := input.CurrentTime().AddDate(0, 0, -30)
			total, err := sumMinorSince(input, since)
			if err != nil {
				return Result{}, err
			}
			if total > expected {
				return alertResult(config, fmt.Sprintf("30-day amount %s exceeds expected monthly volume %s", formatMinor(total), formatMinor(expected))), nil
			}
			return passResult(), nil
		})
	case "missing_id_document":
		return newRule(func(_ context.Context, input EvalContext) (Result, error) {
			if input.Customer != nil && strings.TrimSpace(input.Customer.IDType) == "" {
				return alertResult(config, "Customer has no identity document on file"), nil
			}
			return passResult(), nil
		})
	case "missing_id_photo":
		return newRule(func(_ context.Context, input EvalContext) (Result, error) {
			if input.Signals == nil || (input.Signals.HasIDFront && input.Signals.HasIDBack) {
				return passResult(), nil
			}
			missing := "front and back"
			switch {
			case input.Signals.HasIDFront:
				missing = "back"
			case input.Signals.HasIDBack:
				missing = "front"
			}
			return alertResult(config, fmt.Sprintf("Identity document photo missing (%s)", missing)), nil
		})
	case "missing_phone":
		return newRule(func(_ context.Context, input EvalContext) (Result, error) {
			if input.Customer != nil && strings.TrimSpace(input.Customer.Phone) == "" {
				return alertResult(config, "Customer has no phone number"), nil
			}
			return passResult(), nil
		})
	case "missing_address":
		return newRule(func(_ context.Context, input EvalContext) (Result, error) {
			if input.Customer != nil && strings.TrimSpace(input.Customer.Address) == "" {
				return alertResult(config, "Customer has no address"), nil
			}
			return passResult(), nil
		})
	case "duplicate_id_number":
		return newRule(func(_ context.Context, input EvalContext) (Result, error) {
			if input.Signals != nil && input.Signals.DuplicateIDCount > 0 {
				return alertResult(config, fmt.Sprintf("Identity document number shared with %d other customer(s)", input.Signals.DuplicateIDCount)), nil
			}
			return passResult(), nil
		})
	case "expired_id_document":
		return newRule(func(_ context.Context, input EvalContext) (Result, error) {
			if input.KYCProfile != nil && idExpiredAt(input.KYCProfile.IDExpiryDate, input.CurrentTime(), input.ReferenceTimezone) {
				return alertResult(config, fmt.Sprintf("Identity document expired on %s", input.KYCProfile.IDExpiryDate)), nil
			}
			return passResult(), nil
		})
	case "politically_exposed":
		return newRule(func(_ context.Context, input EvalContext) (Result, error) {
			if input.KYCProfile == nil || !input.KYCProfile.IsPEP {
				return passResult(), nil
			}
			reason := "Customer is a politically exposed person"
			if input.KYCProfile.PEPFunction != "" {
				reason += fmt.Sprintf(" (%s)", input.KYCProfile.PEPFunction)
			}
			return alertResult(config, reason), nil
		})
	case "sensitive_sector":
		return newRule(func(_ context.Context, input EvalContext) (Result, error) {
			if input.KYCProfile != nil && kycrisk.SensitiveSector(input.KYCProfile.ActivitySector) {
				return alertResult(config, fmt.Sprintf("Customer works in a sensitive sector: %s", input.KYCProfile.ActivitySector)), nil
			}
			return passResult(), nil
		})
	case "third_party":
		return newRule(func(_ context.Context, input EvalContext) (Result, error) {
			if input.KYCProfile != nil && input.KYCProfile.ActsForThirdParty {
				return alertResult(config, fmt.Sprintf("Customer acts on behalf of a third party: %s", input.KYCProfile.ThirdPartyName)), nil
			}
			return passResult(), nil
		})
	case "foreign_or_non_resident":
		return newRule(func(_ context.Context, input EvalContext) (Result, error) {
			if input.KYCProfile == nil {
				return passResult(), nil
			}
			var parts []string
			if nationality := input.KYCProfile.Nationality; nationality != "" && nationality != kycrisk.HomeCountry {
				parts = append(parts, fmt.Sprintf("foreign national (%s)", nationality))
			}
			if residence := input.KYCProfile.ResidenceCountry; residence != "" && residence != kycrisk.HomeCountry {
				parts = append(parts, fmt.Sprintf("not resident in the DRC (%s)", residence))
			}
			if len(parts) == 0 {
				return passResult(), nil
			}
			return alertResult(config, "Customer is "+strings.Join(parts, " and ")), nil
		})
	case "kyc_review_due":
		return newRule(func(_ context.Context, input EvalContext) (Result, error) {
			profile := input.KYCProfile
			if profile == nil || input.Customer == nil {
				return passResult(), nil
			}
			if profile.KYCStatus != customerservice.Verified && profile.KYCStatus != customerservice.EnhancedVerification {
				return passResult(), nil
			}
			due := kycrisk.ReviewDueDate(profile.LastVerificationDate, input.Customer.RiskLevel)
			if due.IsZero() || input.CurrentTime().Before(due) {
				return passResult(), nil
			}
			return alertResult(config, fmt.Sprintf("KYC review overdue since %s", due.In(input.ReferenceTimezone).Format("2006-01-02"))), nil
		})
	default:
		return nil, false
	}
}

func passResult() Result { return Result{Outcome: OutcomePass} }

func alertResult(config LegacyRuleConfig, reason string) Result {
	return Result{Outcome: OutcomeAlert, Score: config.Score, Reasons: []string{reason}}
}

func sumMinorSince(input EvalContext, since time.Time) (int64, error) {
	var total int64
	add := func(amount int64) error {
		if amount > 0 && total > int64(^uint64(0)>>1)-amount {
			return errors.New("historical amount total overflows int64")
		}
		total += amount
		return nil
	}
	for _, operation := range input.History {
		if operation.ID != 0 && operation.ID == input.Operation.ID {
			continue
		}
		if !operation.OccurredAt.Before(since) {
			if err := add(operation.AmountMinor); err != nil {
				return 0, err
			}
		}
	}
	if !input.Operation.OccurredAt.Before(since) {
		if err := add(input.Operation.AmountMinor); err != nil {
			return 0, err
		}
	}
	return total, nil
}

func countSince(input EvalContext, since time.Time) int {
	count := 0
	for _, operation := range input.History {
		if operation.ID != 0 && operation.ID == input.Operation.ID {
			continue
		}
		if !operation.OccurredAt.Before(since) {
			count++
		}
	}
	if !input.Operation.OccurredAt.Before(since) {
		count++
	}
	return count
}

func inStructuringBand(amount, threshold int64, marginBPS int) bool {
	if threshold <= 0 || marginBPS <= 0 || marginBPS >= 10000 {
		return false
	}
	// Split the multiplication to avoid overflowing threshold*(10000-marginBPS).
	factor := int64(10000 - marginBPS)
	lower := (threshold/10000)*factor + (threshold%10000)*factor/10000
	return amount >= lower && amount < threshold
}

func formatMinor(amount int64) string {
	negative := amount < 0
	if negative {
		amount = -amount
	}
	formatted := fmt.Sprintf("%d.%02d", amount/100, amount%100)
	if negative {
		return "-" + formatted
	}
	return formatted
}

func idExpiredAt(expiry string, now time.Time, location *time.Location) bool {
	if !kycrisk.ValidDate(expiry) {
		return false
	}
	if location == nil {
		location = time.UTC
	}
	return expiry < now.In(location).Format("2006-01-02")
}
