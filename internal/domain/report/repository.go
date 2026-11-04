// Package report calcule les rapports de la page « Rapports » sur une période choisie :
// journal et synthèse des opérations, et suivi conformité / KYC.
// Les jours sont comptés dans le fuseau du bureau (APP_TIMEZONE, Kinshasa par défaut).
package report

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"fx-app-api/internal/storage"
)

const maxReportRows = 5000

type Period struct {
	From     string `json:"from"` // AAAA-MM-JJ inclus
	To       string `json:"to"`   // AAAA-MM-JJ inclus
	Timezone string `json:"timezone"`
}

type CurrencyLine struct {
	Currency    string  `json:"currency"`
	BuyCount    int     `json:"buy_count"`
	BuyAmount   float64 `json:"buy_amount"`
	BuyCDF      float64 `json:"buy_cdf"`
	BuyAvgRate  float64 `json:"buy_avg_rate"` // moyenne pondérée par les montants
	SellCount   int     `json:"sell_count"`
	SellAmount  float64 `json:"sell_amount"`
	SellCDF     float64 `json:"sell_cdf"`
	SellAvgRate float64 `json:"sell_avg_rate"`
	NetAmount   float64 `json:"net_amount"` // devise achetée - devise vendue
}

type DayLine struct {
	Date    string  `json:"date"`
	Count   int     `json:"count"`
	BuyCDF  float64 `json:"buy_cdf"`
	SellCDF float64 `json:"sell_cdf"`
}

type TraderLine struct {
	TraderID     int     `json:"trader_id"`
	Name         string  `json:"name"`
	Count        int     `json:"count"`
	CounterValue float64 `json:"counter_value"`
}

type Row struct {
	ID           int       `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	Type         string    `json:"type"`
	Currency     string    `json:"currency"`
	Amount       float64   `json:"amount"`
	Rate         float64   `json:"rate"`
	CounterValue float64   `json:"counter_value"`
	Status       string    `json:"status"`
	RiskLevel    string    `json:"risk_level"`
	CustomerID   int       `json:"customer_id"`
	CustomerName string    `json:"customer_name"`
	IDType       string    `json:"id_type"`
	IDNumber     string    `json:"id_number"`
	TraderName   string    `json:"trader_name"`
}

type Activity struct {
	Period         Period         `json:"period"`
	Count          int            `json:"count"`
	CancelledCount int            `json:"cancelled_count"`
	PendingCount   int            `json:"pending_count"`
	Customers      int            `json:"customers"`
	CounterValue   float64        `json:"counter_value"`
	ByCurrency     []CurrencyLine `json:"by_currency"`
	ByDay          []DayLine      `json:"by_day"`
	ByTrader       []TraderLine   `json:"by_trader"`
	Transactions   []Row          `json:"transactions"`
	Truncated      bool           `json:"truncated"`
}

type AlertLine struct {
	ID           int        `json:"id"`
	Flag         string     `json:"flag"`
	Reason       string     `json:"reason"`
	Level        string     `json:"level"`
	Status       string     `json:"status"`
	CustomerName string     `json:"customer_name"`
	CreatedAt    time.Time  `json:"created_at"`
	ResolvedAt   *time.Time `json:"resolved_at"`
}

type Compliance struct {
	Period            Period         `json:"period"`
	CustomersTotal    int            `json:"customers_total"`
	NewCustomers      int            `json:"new_customers"`
	KYC               map[string]int `json:"kyc"` // UNVERIFIED, …, NONE (sans dossier)
	MissingIDPhoto    int            `json:"missing_id_photo"`
	AlertsCreated     int            `json:"alerts_created"`
	AlertsClosed      int            `json:"alerts_closed"`
	AlertsByStatus    map[string]int `json:"alerts_by_status"`
	AlertsByLevel     map[string]int `json:"alerts_by_level"`
	LargeThresholdUSD float64        `json:"large_threshold_usd"`
	LargeThresholdCDF float64        `json:"large_threshold_cdf"`
	LargeTransactions []Row          `json:"large_transactions"`
	Alerts            []AlertLine    `json:"alerts"`
}

type Repository struct{ store *storage.PostgresStore }

func NewRepository(store *storage.PostgresStore) *Repository { return &Repository{store: store} }

func Location() (*time.Location, error) {
	name := strings.TrimSpace(os.Getenv("APP_TIMEZONE"))
	if name == "" {
		name = "Africa/Kinshasa"
	}
	return time.LoadLocation(name)
}

// Bounds convertit deux dates AAAA-MM-JJ (incluses) en bornes horaires [début, fin[.
func Bounds(from, to string, loc *time.Location) (time.Time, time.Time, error) {
	start, err := time.ParseInLocation("2006-01-02", from, loc)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("from must be a date YYYY-MM-DD")
	}
	end, err := time.ParseInLocation("2006-01-02", to, loc)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("to must be a date YYYY-MM-DD")
	}
	if end.Before(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("to must not be before from")
	}
	if end.Sub(start) > 366*24*time.Hour {
		return time.Time{}, time.Time{}, fmt.Errorf("the period must not exceed one year")
	}
	return start, end.AddDate(0, 0, 1), nil
}

// storeScope limite au bureau de change (storeID 0 : tous les bureaux). storeID est un entier :
// il peut être écrit tel quel dans la requête.
func storeScope(alias string, storeID int) string {
	if storeID == 0 {
		return ""
	}
	return fmt.Sprintf(" AND %sstore_id = %d", alias, storeID)
}

// scope ajoute le filtre « ses propres opérations » pour un trader (traderID > 0).
func scope(column string, traderID int, args *[]any) string {
	if traderID <= 0 {
		return ""
	}
	*args = append(*args, traderID)
	return fmt.Sprintf(" AND %s = $%d", column, len(*args))
}

const rowSelect = `SELECT t.id, t.created_at, COALESCE(t.type,''), t.currency, t.amount, COALESCE(t.rate,0),
	COALESCE(t.converted_amount, t.amount * COALESCE(t.rate,0)), COALESCE(t.status,''), COALESCE(t.risk_level,''),
	t.customer_id, COALESCE(c.full_name,''), COALESCE(c.id_type,''), COALESCE(c.id_number,''),
	COALESCE(NULLIF(TRIM(COALESCE(tr.first_name,'') || ' ' || COALESCE(tr.last_name,'')), ''), tr.email, '')
	FROM transactions t
	LEFT JOIN customers c ON c.id = t.customer_id
	LEFT JOIN traders tr ON tr.id = t.trader_id`

func scanRows(rows *sql.Rows) ([]Row, error) {
	defer rows.Close()
	out := []Row{}
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.ID, &r.CreatedAt, &r.Type, &r.Currency, &r.Amount, &r.Rate, &r.CounterValue,
			&r.Status, &r.RiskLevel, &r.CustomerID, &r.CustomerName, &r.IDType, &r.IDNumber, &r.TraderName); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (r *Repository) Activity(from, to string, traderID, storeID int) (*Activity, error) {
	loc, err := Location()
	if err != nil {
		return nil, err
	}
	start, end, err := Bounds(from, to, loc)
	if err != nil {
		return nil, err
	}
	db := r.store.DB()
	res := &Activity{Period: Period{From: from, To: to, Timezone: loc.String()}, ByCurrency: []CurrencyLine{}, ByDay: []DayLine{}, ByTrader: []TraderLine{}}
	const active = "t.deleted_at IS NULL AND t.status IS DISTINCT FROM 'CANCELLED'"

	args := []any{start, end}
	sc := scope("t.trader_id", traderID, &args) + storeScope("t.", storeID)
	if err := db.QueryRow(`SELECT COUNT(*) FILTER (WHERE t.status IS DISTINCT FROM 'CANCELLED'),
			COUNT(*) FILTER (WHERE t.status = 'CANCELLED'), COUNT(*) FILTER (WHERE t.status = 'PENDING'),
			COUNT(DISTINCT t.customer_id) FILTER (WHERE t.status IS DISTINCT FROM 'CANCELLED'),
			COALESCE(SUM(COALESCE(t.converted_amount, t.amount * t.rate)) FILTER (WHERE t.status IS DISTINCT FROM 'CANCELLED'), 0)
		FROM transactions t WHERE t.deleted_at IS NULL AND t.created_at >= $1 AND t.created_at < $2`+sc, args...).
		Scan(&res.Count, &res.CancelledCount, &res.PendingCount, &res.Customers, &res.CounterValue); err != nil {
		return nil, fmt.Errorf("report totals: %w", err)
	}

	rows, err := db.Query(`SELECT t.currency,
			COUNT(*) FILTER (WHERE t.type = 'BUY'), COALESCE(SUM(t.amount) FILTER (WHERE t.type = 'BUY'), 0),
			COALESCE(SUM(COALESCE(t.converted_amount, t.amount * t.rate)) FILTER (WHERE t.type = 'BUY'), 0),
			COUNT(*) FILTER (WHERE t.type = 'SELL'), COALESCE(SUM(t.amount) FILTER (WHERE t.type = 'SELL'), 0),
			COALESCE(SUM(COALESCE(t.converted_amount, t.amount * t.rate)) FILTER (WHERE t.type = 'SELL'), 0)
		FROM transactions t WHERE `+active+` AND t.created_at >= $1 AND t.created_at < $2`+sc+`
		GROUP BY t.currency ORDER BY t.currency`, args...)
	if err != nil {
		return nil, fmt.Errorf("report by currency: %w", err)
	}
	for rows.Next() {
		var l CurrencyLine
		if err := rows.Scan(&l.Currency, &l.BuyCount, &l.BuyAmount, &l.BuyCDF, &l.SellCount, &l.SellAmount, &l.SellCDF); err != nil {
			rows.Close()
			return nil, err
		}
		if l.BuyAmount > 0 {
			l.BuyAvgRate = l.BuyCDF / l.BuyAmount
		}
		if l.SellAmount > 0 {
			l.SellAvgRate = l.SellCDF / l.SellAmount
		}
		l.NetAmount = l.BuyAmount - l.SellAmount
		res.ByCurrency = append(res.ByCurrency, l)
	}
	rows.Close()

	dayArgs := append([]any{}, args...)
	dayArgs = append(dayArgs, loc.String())
	tzPos := len(dayArgs)
	rows, err = db.Query(fmt.Sprintf(`SELECT to_char(t.created_at AT TIME ZONE $%d, 'YYYY-MM-DD') AS d, COUNT(*),
			COALESCE(SUM(COALESCE(t.converted_amount, t.amount * t.rate)) FILTER (WHERE t.type = 'BUY'), 0),
			COALESCE(SUM(COALESCE(t.converted_amount, t.amount * t.rate)) FILTER (WHERE t.type = 'SELL'), 0)
		FROM transactions t WHERE `+active+` AND t.created_at >= $1 AND t.created_at < $2`+sc+`
		GROUP BY d ORDER BY d`, tzPos), dayArgs...)
	if err != nil {
		return nil, fmt.Errorf("report by day: %w", err)
	}
	for rows.Next() {
		var l DayLine
		if err := rows.Scan(&l.Date, &l.Count, &l.BuyCDF, &l.SellCDF); err != nil {
			rows.Close()
			return nil, err
		}
		res.ByDay = append(res.ByDay, l)
	}
	rows.Close()

	rows, err = db.Query(`SELECT t.trader_id,
			COALESCE(NULLIF(TRIM(COALESCE(tr.first_name,'') || ' ' || COALESCE(tr.last_name,'')), ''), tr.email, ''),
			COUNT(*), COALESCE(SUM(COALESCE(t.converted_amount, t.amount * t.rate)), 0)
		FROM transactions t LEFT JOIN traders tr ON tr.id = t.trader_id
		WHERE `+active+` AND t.created_at >= $1 AND t.created_at < $2`+sc+`
		GROUP BY t.trader_id, tr.first_name, tr.last_name, tr.email ORDER BY 4 DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("report by trader: %w", err)
	}
	for rows.Next() {
		var l TraderLine
		if err := rows.Scan(&l.TraderID, &l.Name, &l.Count, &l.CounterValue); err != nil {
			rows.Close()
			return nil, err
		}
		res.ByTrader = append(res.ByTrader, l)
	}
	rows.Close()

	listArgs := append([]any{}, args...)
	listArgs = append(listArgs, maxReportRows+1)
	rows, err = db.Query(fmt.Sprintf(rowSelect+` WHERE t.deleted_at IS NULL AND t.created_at >= $1 AND t.created_at < $2`+sc+`
		ORDER BY t.created_at, t.id LIMIT $%d`, len(listArgs)), listArgs...)
	if err != nil {
		return nil, fmt.Errorf("report rows: %w", err)
	}
	if res.Transactions, err = scanRows(rows); err != nil {
		return nil, err
	}
	if len(res.Transactions) > maxReportRows {
		res.Transactions = res.Transactions[:maxReportRows]
		res.Truncated = true
	}
	return res, nil
}

// LargeThreshold : seuil des « opérations importantes », en USD (REPORT_LARGE_TRANSACTION_USD,
// 10 000 par défaut), converti en CDF au taux d'achat USD configuré pour le bureau.
func LargeThreshold() (usd, cdf float64) {
	usd = envFloat("REPORT_LARGE_TRANSACTION_USD", 10000)
	return usd, usd * envFloat("EXCHANGE_RATE_USD_CDF_BUY", 2770)
}

func envFloat(key string, fallback float64) float64 {
	var v float64
	if _, err := fmt.Sscan(strings.TrimSpace(os.Getenv(key)), &v); err != nil || v <= 0 {
		return fallback
	}
	return v
}

func (r *Repository) Compliance(from, to string, traderID, storeID int) (*Compliance, error) {
	loc, err := Location()
	if err != nil {
		return nil, err
	}
	start, end, err := Bounds(from, to, loc)
	if err != nil {
		return nil, err
	}
	db := r.store.DB()
	res := &Compliance{
		Period:         Period{From: from, To: to, Timezone: loc.String()},
		KYC:            map[string]int{},
		AlertsByStatus: map[string]int{},
		AlertsByLevel:  map[string]int{},
		Alerts:         []AlertLine{},
	}

	// Les clients sont partagés par tout le bureau : ces chiffres ne dépendent pas du trader.
	if err := db.QueryRow(`SELECT COUNT(*), COUNT(*) FILTER (WHERE created_at >= $1 AND created_at < $2),
			COUNT(*) FILTER (WHERE NOT EXISTS (SELECT 1 FROM customer_id_documents d WHERE d.customer_id = customers.id AND d.side = 'front'))
		FROM customers WHERE deleted_at IS NULL`+storeScope("", storeID), start, end).Scan(&res.CustomersTotal, &res.NewCustomers, &res.MissingIDPhoto); err != nil {
		return nil, fmt.Errorf("report customers: %w", err)
	}
	rows, err := db.Query(`SELECT COALESCE(k.kyc_status, 'NONE'), COUNT(*) FROM customers c
		LEFT JOIN customer_kyc_profiles k ON k.customer_id = c.id
		WHERE c.deleted_at IS NULL`+storeScope("c.", storeID)+` GROUP BY 1`)
	if err != nil {
		return nil, fmt.Errorf("report kyc: %w", err)
	}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			rows.Close()
			return nil, err
		}
		res.KYC[status] = n
	}
	rows.Close()

	args := []any{start, end}
	sc := scope("f.trader_id", traderID, &args) + storeScope("f.", storeID)
	if err := db.QueryRow(`SELECT COUNT(*) FILTER (WHERE f.created_at >= $1 AND f.created_at < $2),
			COUNT(*) FILTER (WHERE f.resolved_at >= $1 AND f.resolved_at < $2)
		FROM risk_flags f WHERE f.deleted_at IS NULL`+sc, args...).Scan(&res.AlertsCreated, &res.AlertsClosed); err != nil {
		return nil, fmt.Errorf("report alerts: %w", err)
	}
	rows, err = db.Query(`SELECT f.status, COALESCE(NULLIF(f.level,''), 'NONE'), COUNT(*) FROM risk_flags f
		WHERE f.deleted_at IS NULL AND f.created_at >= $1 AND f.created_at < $2`+sc+` GROUP BY 1, 2`, args...)
	if err != nil {
		return nil, fmt.Errorf("report alerts by status: %w", err)
	}
	for rows.Next() {
		var status, level string
		var n int
		if err := rows.Scan(&status, &level, &n); err != nil {
			rows.Close()
			return nil, err
		}
		res.AlertsByStatus[status] += n
		res.AlertsByLevel[level] += n
	}
	rows.Close()

	alertArgs := append([]any{}, args...)
	alertArgs = append(alertArgs, maxReportRows)
	rows, err = db.Query(fmt.Sprintf(`SELECT f.id, f.flag, COALESCE(f.reason,''), COALESCE(f.level,''), f.status,
			COALESCE(c.full_name,''), f.created_at, f.resolved_at
		FROM risk_flags f LEFT JOIN customers c ON c.id = f.customer_id
		WHERE f.deleted_at IS NULL AND f.created_at >= $1 AND f.created_at < $2`+sc+`
		ORDER BY f.created_at, f.id LIMIT $%d`, len(alertArgs)), alertArgs...)
	if err != nil {
		return nil, fmt.Errorf("report alert list: %w", err)
	}
	for rows.Next() {
		var a AlertLine
		if err := rows.Scan(&a.ID, &a.Flag, &a.Reason, &a.Level, &a.Status, &a.CustomerName, &a.CreatedAt, &a.ResolvedAt); err != nil {
			rows.Close()
			return nil, err
		}
		res.Alerts = append(res.Alerts, a)
	}
	rows.Close()

	res.LargeThresholdUSD, res.LargeThresholdCDF = LargeThreshold()
	largeArgs := []any{start, end, res.LargeThresholdCDF}
	lsc := scope("t.trader_id", traderID, &largeArgs) + storeScope("t.", storeID)
	rows, err = db.Query(rowSelect+` WHERE t.deleted_at IS NULL AND t.status IS DISTINCT FROM 'CANCELLED'
		AND t.created_at >= $1 AND t.created_at < $2
		AND COALESCE(t.converted_amount, t.amount * COALESCE(t.rate,0)) >= $3`+lsc+`
		ORDER BY t.created_at, t.id LIMIT 1000`, largeArgs...)
	if err != nil {
		return nil, fmt.Errorf("report large transactions: %w", err)
	}
	if res.LargeTransactions, err = scanRows(rows); err != nil {
		return nil, err
	}
	return res, nil
}
