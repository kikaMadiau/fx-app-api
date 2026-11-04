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
	// Champs ajoutés pour la vue d'ensemble du frontend.
	BuyToday      int          `json:"buy_today"`
	SellToday     int          `json:"sell_today"`
	PendingToday  int          `json:"pending_today"`
	DailyActivity []DayCount   `json:"daily_activity"`
	LatestRates   []RateQuote  `json:"latest_rates"`
	RecentAlerts  []AlertBrief `json:"recent_alerts"`
}

// DayCount : nombre d'opérations et contre-valeur d'une journée (heure locale).
type DayCount struct {
	Date            string  `json:"date"` // AAAA-MM-JJ
	Count           int     `json:"count"`
	ConvertedAmount float64 `json:"converted_amount"`
}

// RateQuote : dernier taux réellement appliqué pour une devise et un sens d'opération.
type RateQuote struct {
	Currency  string    `json:"currency"`
	Type      string    `json:"type"` // BUY ou SELL
	Rate      float64   `json:"rate"`
	AppliedAt time.Time `json:"applied_at"`
}

// AlertBrief : alerte ouverte ou en revue, résumée pour la vue d'ensemble.
type AlertBrief struct {
	ID            int       `json:"id"`
	Flag          string    `json:"flag"`
	Reason        string    `json:"reason"`
	Level         string    `json:"level"`
	Status        string    `json:"status"`
	TransactionID int       `json:"transaction_id"`
	CustomerName  string    `json:"customer_name"`
	CreatedAt     time.Time `json:"created_at"`
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
// Summary : storeID limite au bureau de change (0 : tous les bureaux) ; traderIDs, à un trader.
func (r *DashboardRepository) Summary(now time.Time, storeID int, traderIDs ...int) (*Summary, error) {
	loc, err := appLocation()
	if err != nil {
		return nil, err
	}
	today, yesterday, tomorrow := DayBounds(now, loc)
	monthStart := MonthStart(now, loc)
	result := &Summary{VolumeToday: []Volume{}, DailyActivity: []DayCount{}, LatestRates: []RateQuote{}, RecentAlerts: []AlertBrief{}}
	db := r.store.DB()
	store := storeClause("", storeID)
	active := "deleted_at IS NULL AND status IS DISTINCT FROM 'CANCELLED'" + store
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
	if err := db.QueryRow(`SELECT COUNT(DISTINCT customer_id) FROM transactions WHERE `+active+` AND created_at >= $1`+activeScope, sinceArgs...).Scan(&result.ActiveCustomers); err != nil {
		return nil, fmt.Errorf("count active customers: %w", err)
	}
	if traderID > 0 {
		if err := db.QueryRow(`SELECT COUNT(DISTINCT c.id) FROM customers c JOIN transactions t ON t.customer_id = c.id WHERE c.deleted_at IS NULL AND t.deleted_at IS NULL AND t.status IS DISTINCT FROM 'CANCELLED' AND t.trader_id = $1 AND c.created_at >= $2 AND c.created_at <= $3`+storeClause("c.", storeID), traderID, monthStart, now).Scan(&result.NewCustomersThisMonth); err != nil {
			return nil, fmt.Errorf("count new customers: %w", err)
		}
	} else if err := db.QueryRow(`SELECT COUNT(*) FROM customers WHERE deleted_at IS NULL AND created_at >= $1 AND created_at <= $2`+store, monthStart, now).Scan(&result.NewCustomersThisMonth); err != nil {
		return nil, fmt.Errorf("count new customers: %w", err)
	}
	alertScope := ""
	alertArgs := []any{}
	if traderID > 0 {
		alertScope = " AND trader_id = $1"
		alertArgs = append(alertArgs, traderID)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FILTER (WHERE status IN ('OPEN','IN_REVIEW')), COUNT(*) FILTER (WHERE status IN ('OPEN','IN_REVIEW') AND level IN ('HIGH','CRITICAL')) FROM risk_flags WHERE deleted_at IS NULL`+store+alertScope, alertArgs...).Scan(&result.OpenAlerts, &result.UrgentAlerts); err != nil {
		return nil, fmt.Errorf("count open alerts: %w", err)
	}
	if err := r.overviewExtras(result, loc, today, tomorrow, storeID, traderID); err != nil {
		return nil, err
	}
	return result, nil
}

// overviewExtras complète le résumé : achats/ventes du jour, activité des 7 derniers jours,
// derniers taux appliqués et alertes récentes. Un trader ne voit que ses propres opérations ;
// les taux, eux, sont ceux pratiqués par tout le bureau.
func (r *DashboardRepository) overviewExtras(result *Summary, loc *time.Location, today, tomorrow time.Time, storeID, traderID int) error {
	db := r.store.DB()
	active := "deleted_at IS NULL AND status IS DISTINCT FROM 'CANCELLED'" + storeClause("", storeID)
	scope, args := "", []any{today, tomorrow}
	if traderID > 0 {
		scope = " AND trader_id = $3"
		args = append(args, traderID)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FILTER (WHERE type = 'BUY'), COUNT(*) FILTER (WHERE type = 'SELL'), COUNT(*) FILTER (WHERE status = 'PENDING') FROM transactions WHERE `+active+` AND created_at >= $1 AND created_at < $2`+scope, args...).Scan(&result.BuyToday, &result.SellToday, &result.PendingToday); err != nil {
		return fmt.Errorf("count today's buys and sells: %w", err)
	}

	weekStart := today.AddDate(0, 0, -6)
	weekArgs := []any{weekStart, tomorrow, loc.String()}
	weekScope := ""
	if traderID > 0 {
		weekScope = " AND trader_id = $4"
		weekArgs = append(weekArgs, traderID)
	}
	rows, err := db.Query(`SELECT to_char(created_at AT TIME ZONE $3, 'YYYY-MM-DD'), COUNT(*), COALESCE(SUM(converted_amount),0) FROM transactions WHERE `+active+` AND created_at >= $1 AND created_at < $2`+weekScope+` GROUP BY 1`, weekArgs...)
	if err != nil {
		return fmt.Errorf("daily activity: %w", err)
	}
	byDay := map[string]DayCount{}
	for rows.Next() {
		var d DayCount
		if err := rows.Scan(&d.Date, &d.Count, &d.ConvertedAmount); err != nil {
			rows.Close()
			return err
		}
		byDay[d.Date] = d
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for day := weekStart; day.Before(tomorrow); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		d, ok := byDay[key]
		if !ok {
			d = DayCount{Date: key}
		}
		result.DailyActivity = append(result.DailyActivity, d)
	}

	rows, err = db.Query(`SELECT DISTINCT ON (currency, type) currency, type, rate, created_at FROM transactions WHERE ` + active + ` AND rate > 0 ORDER BY currency, type, created_at DESC, id DESC`)
	if err != nil {
		return fmt.Errorf("latest rates: %w", err)
	}
	for rows.Next() {
		var q RateQuote
		if err := rows.Scan(&q.Currency, &q.Type, &q.Rate, &q.AppliedAt); err != nil {
			rows.Close()
			return err
		}
		result.LatestRates = append(result.LatestRates, q)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	alertScope, alertArgs := "", []any{}
	if traderID > 0 {
		alertScope = " AND f.trader_id = $1"
		alertArgs = append(alertArgs, traderID)
	}
	rows, err = db.Query(`SELECT f.id, f.flag, COALESCE(f.reason,''), COALESCE(f.level,''), f.status, f.transaction_id, COALESCE(c.full_name,''), f.created_at
		FROM risk_flags f LEFT JOIN customers c ON c.id = f.customer_id
		WHERE f.deleted_at IS NULL AND f.status IN ('OPEN','IN_REVIEW')`+storeClause("f.", storeID)+alertScope+`
		ORDER BY CASE f.level WHEN 'CRITICAL' THEN 0 WHEN 'HIGH' THEN 1 WHEN 'MEDIUM' THEN 2 ELSE 3 END, f.created_at DESC
		LIMIT 5`, alertArgs...)
	if err != nil {
		return fmt.Errorf("recent alerts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a AlertBrief
		if err := rows.Scan(&a.ID, &a.Flag, &a.Reason, &a.Level, &a.Status, &a.TransactionID, &a.CustomerName, &a.CreatedAt); err != nil {
			return err
		}
		result.RecentAlerts = append(result.RecentAlerts, a)
	}
	return rows.Err()
}

// storeClause limite au bureau de change (storeID 0 : tous les bureaux). storeID est un entier :
// il peut être écrit tel quel dans la requête.
func storeClause(alias string, storeID int) string {
	if storeID == 0 {
		return ""
	}
	return fmt.Sprintf(" AND %sstore_id = %d", alias, storeID)
}
