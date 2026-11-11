// Package engine defines the versioned, configurable AML rule contract.
package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	customerentity "fx-app-api/internal/domain/customer/entity"
	"fx-app-api/internal/domain/customer/kycrisk"
	customerservice "fx-app-api/internal/domain/customer/service"
)

// Category identifies the stage and purpose of a rule in the decision pipeline.
type Category string

const (
	CategoryHardBlock Category = "HARD_BLOCK"
	CategoryKYCLevel  Category = "KYC_LEVEL"
	CategoryScreening Category = "SCREENING"
	CategoryDetection Category = "DETECTION"
)

func (c Category) Valid() bool {
	switch c {
	case CategoryHardBlock, CategoryKYCLevel, CategoryScreening, CategoryDetection:
		return true
	default:
		return false
	}
}

// Outcome is the result returned by a rule. REVIEW means a human review is required.
type Outcome string

const (
	OutcomePass   Outcome = "PASS"
	OutcomeAlert  Outcome = "ALERT"
	OutcomeReview Outcome = "REVIEW"
	// OutcomeRequireApproval is the public terminology for a required human approval.
	// OutcomeReview remains accepted for compatibility with rules ported from the legacy engine.
	OutcomeRequireApproval Outcome = "REQUIRE_APPROVAL"
	OutcomeBlock           Outcome = "BLOCK"
)

func (o Outcome) Valid() bool {
	switch o {
	case OutcomePass, OutcomeAlert, OutcomeReview, OutcomeRequireApproval, OutcomeBlock:
		return true
	default:
		return false
	}
}

// Mode controls the effect of a registered rule. SHADOW records results without enforcement.
type Mode string

const (
	ModeOff     Mode = "OFF"
	ModeShadow  Mode = "SHADOW"
	ModeEnforce Mode = "ENFORCE"
)

func (m Mode) Valid() bool {
	switch m {
	case ModeOff, ModeShadow, ModeEnforce:
		return true
	default:
		return false
	}
}

// Rule is implemented independently by each AML rule.
type Rule interface {
	Code() string
	Category() Category
	Evaluate(ctx context.Context, input EvalContext) (Result, error)
}

// Result contains the rule outcome and explainable evidence. Evidence values should be
// JSON-serializable so the decision journal can preserve a stable snapshot.
type Result struct {
	Outcome  Outcome        `json:"outcome"`
	Score    int            `json:"score"`
	Reasons  []string       `json:"reasons,omitempty"`
	Evidence map[string]any `json:"evidence,omitempty"`
}

// Operation contains the values the engine evaluates. AmountMinor is an integer in the
// smallest unit of Currency (for example cents for USD). Currency must be an ISO 4217 code.
// OccurredAt is an absolute timestamp and should be stored in UTC.
type Operation struct {
	ID             int64     `json:"id,omitempty"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"`
	PersonID       int64     `json:"person_id"`
	OperatorID     int64     `json:"operator_id"`
	StoreID        int64     `json:"store_id,omitempty"`
	AmountMinor    int64     `json:"amount_minor"`
	Currency       string    `json:"currency"`
	Type           string    `json:"type,omitempty"`
	TargetCurrency string    `json:"target_currency,omitempty"`
	Status         string    `json:"status,omitempty"`
	OperationClass string    `json:"operation_class,omitempty"`
	GrossUSDMinor  int64     `json:"gross_usd_minor,omitempty"`
	NetUSDMinor    int64     `json:"net_usd_minor,omitempty"`
	RatePPM        int64     `json:"rate_ppm,omitempty"`
	BeneficiaryKey string    `json:"beneficiary_key,omitempty"`
	RiskZone       string    `json:"risk_zone,omitempty"`
	OccurredAt     time.Time `json:"occurred_at"`
}

// HistoricalOperation is an earlier operation for the same person, including activity at
// other operators. It deliberately uses integer minor units as well.
type HistoricalOperation struct {
	ID             int64     `json:"id,omitempty"`
	OperatorID     int64     `json:"operator_id"`
	StoreID        int64     `json:"store_id,omitempty"`
	AmountMinor    int64     `json:"amount_minor"`
	Currency       string    `json:"currency"`
	Type           string    `json:"type,omitempty"`
	TargetCurrency string    `json:"target_currency,omitempty"`
	Status         string    `json:"status,omitempty"`
	GrossUSDMinor  int64     `json:"gross_usd_minor,omitempty"`
	NetUSDMinor    int64     `json:"net_usd_minor,omitempty"`
	BeneficiaryKey string    `json:"beneficiary_key,omitempty"`
	Phone          string    `json:"phone,omitempty"`
	OccurredAt     time.Time `json:"occurred_at"`
}

// IdentitySnapshot and LicenseSnapshot contain verified, point-in-time inputs supplied by
// identity and operator repositories before rules run.
type IdentitySnapshot struct {
	CustomerIdentified        bool
	BeneficialOwnerIdentified bool
	IdentityDocumentPresented bool
	IdentityDocumentValid     bool
	AddressPresent            bool
	PhotoPresent              bool
}

type LicenseSnapshot struct {
	OperatorStatus string
	StoreStatus    string
}

// IdentityLinkSignals report counts of other person profiles sharing an identity attribute.
type IdentityLinkSignals struct {
	PhonePersonCount           int
	AddressPersonCount         int
	BeneficialOwnerPersonCount int
}

// SanctionsMatch is a match already produced by a configured sanctions screening source.
type SanctionsMatch struct {
	SourceID   string `json:"source_id"`
	PartyType  string `json:"party_type"`
	Reference  string `json:"reference"`
	MatchScore int    `json:"match_score"`
}

// Clock is injected so rule evaluation is deterministic and never reads time.Now directly.
type Clock interface {
	Now() time.Time
}

// ClockFunc adapts a function to Clock, useful for application clocks and deterministic tests.
type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }

// EvalContext provides one immutable evaluation snapshot. ReferenceTimezone controls
// calendar windows such as the person's daily total; all timestamps represent UTC instants.
type EvalContext struct {
	Operation  Operation
	Customer   *customerentity.Customer
	KYCProfile *customerservice.CustomerKYCProfile
	// ExpectedVolumeMinor is the KYC profile's declared monthly volume normalized to cents.
	// It keeps the engine from doing monetary calculations with the legacy float64 model field.
	ExpectedVolumeMinor         int64
	Identity                    IdentitySnapshot
	IdentityLinks               IdentityLinkSignals
	License                     LicenseSnapshot
	SanctionsMatches            []SanctionsMatch
	SanctionsScreeningAvailable bool `json:"sanctions_screening_available"`
	BCCRatePPM                  int64
	BCCRateAt                   time.Time
	Signals                     *kycrisk.Signals
	History                     []HistoricalOperation
	Clock                       Clock
	ReferenceTimezone           *time.Location
}

var (
	ErrMissingClock             = errors.New("evaluation context requires an injected clock")
	ErrMissingReferenceTimezone = errors.New("evaluation context requires a reference timezone")
	ErrMissingRequiredInput     = errors.New("AML rule input is unavailable")
)

// Validate checks required context and the shape of operation currency values. ISO membership
// is validated by the configured currency source; this layer ensures the canonical code form.
func (c EvalContext) Validate() error {
	if c.Clock == nil {
		return ErrMissingClock
	}
	if c.ReferenceTimezone == nil {
		return ErrMissingReferenceTimezone
	}
	if c.Operation.PersonID <= 0 {
		return errors.New("operation person_id must be positive")
	}
	if c.Operation.AmountMinor < 0 {
		return errors.New("operation amount_minor must not be negative")
	}
	if c.Operation.GrossUSDMinor < 0 || c.Operation.NetUSDMinor < 0 {
		return errors.New("operation USD amounts must not be negative")
	}
	if c.ExpectedVolumeMinor < 0 {
		return errors.New("expected volume in cents must not be negative")
	}
	if !validCurrencyCode(c.Operation.Currency) {
		return fmt.Errorf("operation currency %q must be a three-letter uppercase ISO 4217 code", c.Operation.Currency)
	}
	if c.Operation.OccurredAt.IsZero() {
		return errors.New("operation occurred_at is required")
	}
	for i, op := range c.History {
		if op.AmountMinor < 0 {
			return fmt.Errorf("history[%d] amount_minor must not be negative", i)
		}
		if op.GrossUSDMinor < 0 || op.NetUSDMinor < 0 {
			return fmt.Errorf("history[%d] USD amounts must not be negative", i)
		}
		if !validCurrencyCode(op.Currency) {
			return fmt.Errorf("history[%d] currency %q must be a three-letter uppercase ISO 4217 code", i, op.Currency)
		}
		if op.OccurredAt.IsZero() {
			return fmt.Errorf("history[%d] occurred_at is required", i)
		}
	}
	return nil
}

// CurrentTime returns the injected clock value normalized to UTC.
func (c EvalContext) CurrentTime() time.Time {
	if c.Clock == nil {
		return time.Time{}
	}
	return c.Clock.Now().UTC()
}

func validCurrencyCode(code string) bool {
	if len(code) != 3 || strings.ToUpper(code) != code {
		return false
	}
	for _, r := range code {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}
