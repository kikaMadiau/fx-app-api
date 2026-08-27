package handlers

import "net/http"

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
			BuyRate:    2770,
			SellRate:   2795,
			ChangeRate: 0.6,
		},
		{
			Pair:       "EUR/CDF",
			BuyRate:    3000,
			SellRate:   3035,
			ChangeRate: 0.3,
		},
		{
			Pair:       "GBP/CDF",
			BuyRate:    3480,
			SellRate:   3525,
			ChangeRate: -0.2,
		},
	}

	return WriteJson(w, http.StatusOK, analyses)
}
