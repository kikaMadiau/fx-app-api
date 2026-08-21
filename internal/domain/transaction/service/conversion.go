package service

import (
	"fmt"
	"math"
)

const conversionPrecision = 10000

// CalculateConvertedAmount applique le taux fixe du trader au montant source.
func CalculateConvertedAmount(amount, traderRate float64) (float64, error) {
	if amount <= 0 {
		return 0, fmt.Errorf("amount must be greater than 0")
	}
	if traderRate <= 0 {
		return 0, fmt.Errorf("rate must be greater than 0")
	}

	return math.Round(amount*traderRate*conversionPrecision) / conversionPrecision, nil
}
