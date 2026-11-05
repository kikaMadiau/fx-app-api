package service

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// ExchangeRate is the server-configured quote used by both the UI analysis endpoint
// and transaction creation.
type ExchangeRate struct {
	Pair       string  `json:"pair"`
	BuyRate    float64 `json:"buy_rate"`
	SellRate   float64 `json:"sell_rate"`
	ChangeRate float64 `json:"change_rate"`
}

// ConfiguredExchangeRates returns the supported CDF quotes. Invalid or absent environment
// values use the same defaults previously exposed by GET /analysais.
func ConfiguredExchangeRates() []ExchangeRate {
	return []ExchangeRate{
		{
			Pair:       "USD/CDF",
			BuyRate:    positiveEnvRate("EXCHANGE_RATE_USD_CDF_BUY", 2770),
			SellRate:   positiveEnvRate("EXCHANGE_RATE_USD_CDF_SELL", 2795),
			ChangeRate: envRate("EXCHANGE_RATE_USD_CDF_CHANGE", 0.6),
		},
		{
			Pair:       "EUR/CDF",
			BuyRate:    positiveEnvRate("EXCHANGE_RATE_EUR_CDF_BUY", 3000),
			SellRate:   positiveEnvRate("EXCHANGE_RATE_EUR_CDF_SELL", 3035),
			ChangeRate: envRate("EXCHANGE_RATE_EUR_CDF_CHANGE", 0.3),
		},
		{
			Pair:       "GBP/CDF",
			BuyRate:    positiveEnvRate("EXCHANGE_RATE_GBP_CDF_BUY", 3480),
			SellRate:   positiveEnvRate("EXCHANGE_RATE_GBP_CDF_SELL", 3525),
			ChangeRate: envRate("EXCHANGE_RATE_GBP_CDF_CHANGE", -0.2),
		},
	}
}

// ResolveTransactionRate selects the server-side configured quote for the supported pair
// and transaction direction. A client-provided rate is never used for settlement.
func ResolveTransactionRate(currency, targetCurrency, transactionType string) (float64, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	targetCurrency = strings.ToUpper(strings.TrimSpace(targetCurrency))
	transactionType = strings.ToUpper(strings.TrimSpace(transactionType))
	if targetCurrency != "CDF" {
		return 0, fmt.Errorf("unsupported currency pair %s/%s", currency, targetCurrency)
	}
	for _, quote := range ConfiguredExchangeRates() {
		if quote.Pair != currency+"/"+targetCurrency {
			continue
		}
		switch transactionType {
		case "BUY":
			return quote.BuyRate, nil
		case "SELL":
			return quote.SellRate, nil
		default:
			return 0, fmt.Errorf("unsupported transaction type %q", transactionType)
		}
	}
	return 0, fmt.Errorf("unsupported currency pair %s/%s", currency, targetCurrency)
}

func positiveEnvRate(key string, fallback float64) float64 {
	rate := envRate(key, fallback)
	if rate <= 0 {
		return fallback
	}
	return rate
}

func envRate(key string, fallback float64) float64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	// Accepte aussi les valeurs saisies au format français (ex. « 2 263,33 »).
	value = strings.NewReplacer(" ", "", "\u00a0", "", "\u202f", "", ",", ".").Replace(value)
	rate, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return fallback
	}
	return rate
}
