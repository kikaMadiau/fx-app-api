package handlers

import (
	"net/http"
	"os"
	"strconv"
	"strings"
)

type ExchangeAnalysisResponse struct {
	Pair       string  `json:"pair"`
	BuyRate    float64 `json:"buy_rate"`
	SellRate   float64 `json:"sell_rate"`
	ChangeRate float64 `json:"change_rate"`
}

func (h *Handler) HandleListAnalysais(w http.ResponseWriter, r *http.Request) error {
	analyses := []ExchangeAnalysisResponse{
		{
			Pair:       "USD/CDF",
			BuyRate:    exchangeRateFromEnv("EXCHANGE_RATE_USD_CDF_BUY", 2770),
			SellRate:   exchangeRateFromEnv("EXCHANGE_RATE_USD_CDF_SELL", 2795),
			ChangeRate: exchangeRateFromEnv("EXCHANGE_RATE_USD_CDF_CHANGE", 0.6),
		},
		{
			Pair:       "EUR/CDF",
			BuyRate:    exchangeRateFromEnv("EXCHANGE_RATE_EUR_CDF_BUY", 3000),
			SellRate:   exchangeRateFromEnv("EXCHANGE_RATE_EUR_CDF_SELL", 3035),
			ChangeRate: exchangeRateFromEnv("EXCHANGE_RATE_EUR_CDF_CHANGE", 0.3),
		},
		{
			Pair:       "GBP/CDF",
			BuyRate:    exchangeRateFromEnv("EXCHANGE_RATE_GBP_CDF_BUY", 3480),
			SellRate:   exchangeRateFromEnv("EXCHANGE_RATE_GBP_CDF_SELL", 3525),
			ChangeRate: exchangeRateFromEnv("EXCHANGE_RATE_GBP_CDF_CHANGE", -0.2),
		},
	}

	return WriteJson(w, http.StatusOK, analyses)
}

func exchangeRateFromEnv(key string, fallback float64) float64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	rate, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return rate
}
