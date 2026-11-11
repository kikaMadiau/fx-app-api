package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrBCCRateNotFound = errors.New("BCC indicative rate not found")

type BCCRateRepository struct{}

func NewBCCRateRepository() *BCCRateRepository { return &BCCRateRepository{} }

func (r *BCCRateRepository) CreateWithTx(ctx context.Context, tx *sql.Tx, currency string, referenceDay time.Time, unitsPerUSDPPM int64, source string, publishedAt time.Time) error {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if tx == nil || len(currency) != 3 || unitsPerUSDPPM <= 0 || strings.TrimSpace(source) == "" || publishedAt.IsZero() || referenceDay.IsZero() {
		return errors.New("BCC rate requires transaction, ISO currency, reference day, positive rate, source, and publication time")
	}
	for _, r := range currency {
		if r < 'A' || r > 'Z' {
			return errors.New("BCC rate currency must be an uppercase ISO code")
		}
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO aml_bcc_rates(currency, reference_day, units_per_usd_ppm, source, published_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(currency,reference_day) DO NOTHING`, currency, referenceDay.Format("2006-01-02"), unitsPerUSDPPM, strings.TrimSpace(source), publishedAt.UTC())
	if err != nil {
		return fmt.Errorf("store BCC indicative rate: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check BCC rate insert: %w", err)
	}
	if changed == 1 {
		return nil
	}
	var existing int64
	if err := tx.QueryRowContext(ctx, `SELECT units_per_usd_ppm FROM aml_bcc_rates WHERE currency=$1 AND reference_day=$2`, currency, referenceDay.Format("2006-01-02")).Scan(&existing); err != nil {
		return fmt.Errorf("read existing BCC rate: %w", err)
	}
	if existing != unitsPerUSDPPM {
		return fmt.Errorf("BCC rate for %s on %s is immutable and already has a different value", currency, referenceDay.Format("2006-01-02"))
	}
	return nil
}

// UnitsPerUSD returns the published units of currency per USD, scaled by 1e6.
// USD is the exact identity quote and does not require a stored rate.
func (r *BCCRateRepository) UnitsPerUSD(ctx context.Context, tx *sql.Tx, currency string, referenceDay time.Time) (int64, error) {
	rate, _, err := r.PublishedRate(ctx, tx, currency, referenceDay)
	return rate, err
}

func (r *BCCRateRepository) PublishedRate(ctx context.Context, tx *sql.Tx, currency string, referenceDay time.Time) (int64, time.Time, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "USD" {
		return 1_000_000, referenceDay.UTC(), nil
	}
	if tx == nil || len(currency) != 3 || referenceDay.IsZero() {
		return 0, time.Time{}, errors.New("BCC rate lookup requires transaction, ISO currency, and reference day")
	}
	var rate int64
	var published time.Time
	err := tx.QueryRowContext(ctx, `SELECT units_per_usd_ppm, published_at FROM aml_bcc_rates WHERE currency=$1 AND reference_day=$2`, currency, referenceDay.Format("2006-01-02")).Scan(&rate, &published)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, time.Time{}, fmt.Errorf("%w: %s on %s", ErrBCCRateNotFound, currency, referenceDay.Format("2006-01-02"))
	}
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("load BCC indicative rate: %w", err)
	}
	if rate <= 0 {
		return 0, time.Time{}, fmt.Errorf("invalid BCC indicative rate for %s on %s", currency, referenceDay.Format("2006-01-02"))
	}
	return rate, published.UTC(), nil
}
