package service

import "testing"

func TestCalculateConvertedAmount(t *testing.T) {
	convertedAmount, err := CalculateConvertedAmount(100, 2850.55)
	if err != nil {
		t.Fatalf("CalculateConvertedAmount returned error: %v", err)
	}

	if convertedAmount != 285055 {
		t.Fatalf("expected converted amount 285055, got %.4f", convertedAmount)
	}
}

func TestCalculateConvertedAmountRoundsToFourDecimals(t *testing.T) {
	convertedAmount, err := CalculateConvertedAmount(10, 1.234567)
	if err != nil {
		t.Fatalf("CalculateConvertedAmount returned error: %v", err)
	}

	if convertedAmount != 12.3457 {
		t.Fatalf("expected converted amount 12.3457, got %.4f", convertedAmount)
	}
}

func TestCalculateConvertedAmountRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		amount float64
		rate   float64
	}{
		{name: "zero amount", amount: 0, rate: 2800},
		{name: "negative amount", amount: -1, rate: 2800},
		{name: "zero rate", amount: 100, rate: 0},
		{name: "negative rate", amount: 100, rate: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := CalculateConvertedAmount(tt.amount, tt.rate); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
