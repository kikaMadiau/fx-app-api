package repository

import (
	"fmt"
	"os"
	"time"

	"fx-app-api/internal/storage"
)

type Volume struct {
	Currency        string  `json:"currency"`
	Amount          float64 `json:"amount"`
	ConvertedAmount float64 `json:"converted_amount"`
	TargetCurrency  string  `json:"target_currency"`
}
type Summary struct {
	TransactionsToday     int      `json:"transactions_today"`
	TransactionsYesterday int      `json:"transactions_yesterday"`
	VolumeToday           []Volume `json:"volume_today"`
	ActiveCustomers       int      `json:"active_customers"`
	NewCustomersThisMonth int      `json:"new_customers_this_month"`
	OpenAlerts            int      `json:"open_alerts"`
	UrgentAlerts          int      `json:"urgent_alerts"`
}
type DashboardRepository struct{ store *storage.PostgresStore }

func NewDashboardRepository(store *storage.PostgresStore) *DashboardRepository {
	return &DashboardRepository{store: store}
}

// DayBounds calcule les limites locales du jour courant et précédent.
func DayBounds(now time.Time, location *time.Location) (todayStart, yesterdayStart, tomorrowStart time.Time) {
	local := now.In(location)
	todayStart = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	return todayStart, todayStart.AddDate(0, 0, -1), todayStart.AddDate(0, 0, 1)
}
func MonthStart(now time.Time, location *time.Location) time.Time {
	local := now.In(location)
	return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, location)
}
func appLocation() (*time.Location, error) {
	name := os.Getenv("APP_TIMEZONE")
	if name == "" {
		name = "Africa/Kinshasa"
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("invalid APP_TIMEZONE: %w", err)
	}
	return loc, nil
}
func (r *DashboardRepository) Summary(now time.Time, traderIDs ...int) (*Summary, error) {
	loc, err := appLocation()
	if err != nil {
		return nil, err
	}
	today, yesterday, tomorrow := DayBounds(now, loc)
	monthStart := MonthStart(now, loc)
	result := &Summary{VolumeToday: []Volume{}}
	db := r.store.DB()
	const active = "deleted_at IS NULL AND status IS DISTINCT FROM 'CANCELLED'"
	traderID := 0
	if len(traderIDs) > 0 {
		traderID = traderIDs[0]
	}
	scope, todayArgs, sinceArgs := "", []any{today, tomorrow}, []any{now.AddDate(0, 0, -30)}
	if traderID > 0 {
		scope = " AND trader_id = $3"
		todayArgs = append(todayArgs, traderID)
		sinceArgs = append(sinceArgs, traderID)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM transactions WHERE `+active+` AND created_at >= $1 AND created_at < $2`+scope, todayArgs...).Scan(&result.TransactionsToday); err != nil {
		return nil, fmt.Errorf("count today's transactions: %w", err)
	}
	yesterdayArgs := []any{yesterday, today}
	if traderID > 0 {
		yesterdayArgs = append(yesterdayArgs, traderID)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM transactions WHERE `+active+` AND created_at >= $1 AND created_at < $2`+scope, yesterdayArgs...).Scan(&result.TransactionsYesterday); err != nil {
		return nil, fmt.Errorf("count yesterday's transactions: %w", err)
	}
	rows, err := db.Query(`SELECT currency, COALESCE(SUM(amount),0), CASE WHEN COUNT(DISTINCT target_currency)=1 THEN COALESCE(SUM(converted_amount),0) ELSE 0 END, CASE WHEN COUNT(DISTINCT target_currency)=1 THEN MIN(target_currency) ELSE '' END FROM transactions WHERE `+active+` AND created_at >= $1 AND created_at < $2`+scope+` GROUP BY currency ORDER BY currency`, todayArgs...)
	if err != nil {
		return nil, fmt.Errorf("sum today's volume: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v Volume
		if err := rows.Scan(&v.Currency, &v.Amount, &v.ConvertedAmount, &v.TargetCurrency); err != nil {
			return nil, err
		}
		result.VolumeToday = append(result.VolumeToday, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	activeScope := ""
	if traderID > 0 {
		activeScope = " AND trader_id = $2"
	}
	if err := db.QueryRow(`SELECT COUNT(DISTINCT customer_id) FROM transactions WHERE deleted_at IS NULL AND status IS DISTINCT FROM 'CANCELLED' AND created_at >= $1`+activeScope, sinceArgs...).Scan(&result.ActiveCustomers); err != nil {
		return nil, fmt.Errorf("count active customers: %w", err)
	}
	if traderID > 0 {
		if err := db.QueryRow(`SELECT COUNT(DISTINCT c.id) FROM customers c JOIN transactions t ON t.customer_id = c.id WHERE c.deleted_at IS NULL AND t.deleted_at IS NULL AND t.status IS DISTINCT FROM 'CANCELLED' AND t.trader_id = $1 AND c.created_at >= $2 AND c.created_at <= $3`, traderID, monthStart, now).Scan(&result.NewCustomersThisMonth); err != nil {
			return nil, fmt.Errorf("count new customers: %w", err)
		}
	} else if err := db.QueryRow(`SELECT COUNT(*) FROM customers WHERE deleted_at IS NULL AND created_at >= $1 AND created_at <= $2`, monthStart, now).Scan(&result.NewCustomersThisMonth); err != nil {
		return nil, fmt.Errorf("count new customers: %w", err)
	}
	alertScope := ""
	alertArgs := []any{}
	if traderID > 0 {
		alertScope = " AND trader_id = $1"
		alertArgs = append(alertArgs, traderID)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FILTER (WHERE status IN ('OPEN','IN_REVIEW')), COUNT(*) FILTER (WHERE status IN ('OPEN','IN_REVIEW') AND level IN ('HIGH','CRITICAL')) FROM risk_flags WHERE deleted_at IS NULL`+alertScope, alertArgs...).Scan(&result.OpenAlerts, &result.UrgentAlerts); err != nil {
		return nil, fmt.Errorf("count open alerts: %w", err)
	}
	return result, nil
}
