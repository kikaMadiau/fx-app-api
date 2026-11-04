package kycrisk

import (
	"strings"
	"testing"
	"time"
)

func codes(blocks []Block) string {
	var out []string
	for _, b := range blocks {
		out = append(out, b.Code)
	}
	return strings.Join(out, ",")
}

func TestBlocks(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		in   GateInput
		want string
	}{
		{"no ID under threshold", GateInput{AmountUSD: 1000}, ""},
		{"no ID above threshold", GateInput{AmountUSD: 1000.01}, BlockIDRequired},
		{"expired ID, any amount", GateInput{IDType: "PASSPORT", IDExpiryDate: "2026-11-02", AmountUSD: 10}, BlockIDExpired},
		{"ID valid on its expiry day", GateInput{IDType: "PASSPORT", IDExpiryDate: "2026-11-03", AmountUSD: 10}, ""},
		{"high risk above threshold", GateInput{IDType: "VOTER_CARD", RiskLevel: "HIGH", AmountUSD: 1500}, BlockValidationRequired},
		{"high risk under threshold", GateInput{IDType: "VOTER_CARD", RiskLevel: "CRITICAL", AmountUSD: 900}, ""},
		{"medium risk above threshold", GateInput{IDType: "VOTER_CARD", RiskLevel: "MEDIUM", AmountUSD: 5000}, ""},
		{"several reasons", GateInput{RiskLevel: "HIGH", IDExpiryDate: "2025-01-01", AmountUSD: 2000}, "KYC_ID_REQUIRED,KYC_ID_EXPIRED,KYC_VALIDATION_REQUIRED"},
	}
	for _, c := range cases {
		if got := codes(Blocks(c.in, now)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	msg := BlockMessage(Blocks(GateInput{AmountUSD: 2500}, now))
	if !strings.Contains(msg, "1 000 USD") || !strings.HasPrefix(msg, "Opération refusée.") {
		t.Fatalf("unexpected message %q", msg)
	}
}

func TestUSDEquivalent(t *testing.T) {
	t.Setenv("EXCHANGE_RATE_USD_CDF_BUY", "2800")
	if got := USDEquivalent(1500, "USD", 4200000, "CDF"); got != 1500 {
		t.Fatalf("USD amount: got %v", got)
	}
	if got := USDEquivalent(1000, "EUR", 3080000, "CDF"); got != 1100 {
		t.Fatalf("EUR through CDF counter value: got %v", got)
	}
}

func TestReviewDueDate(t *testing.T) {
	last := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	for level, want := range map[string]string{"HIGH": "2026-01-15", "CRITICAL": "2026-01-15", "MEDIUM": "2027-01-15", "LOW": "2028-01-15", "": "2028-01-15"} {
		if got := ReviewDueDate(last, level).Format("2006-01-02"); got != want {
			t.Errorf("%s: got %s want %s", level, got, want)
		}
	}
	if !ReviewDueDate(time.Time{}, "HIGH").IsZero() {
		t.Fatal("never verified: no due date")
	}
}

func TestDailyLimitBlock(t *testing.T) {
	t.Setenv("DAILY_LIMIT_USD", "5000")
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	base := GateInput{IDType: "PASSPORT", IDExpiryDate: "2030-01-01", RiskLevel: "LOW"}

	base.DayTotalUSD, base.AmountUSD = 4000, 1000
	if blocks := Blocks(base, now); HasBlock(blocks, BlockDailyLimit) {
		t.Fatalf("reaching exactly the limit is allowed: %+v", blocks)
	}
	base.AmountUSD = 1000.5
	blocks := Blocks(base, now)
	if !HasBlock(blocks, BlockDailyLimit) {
		t.Fatalf("above the limit must be refused: %+v", blocks)
	}
	if msg := BlockMessage(blocks); !strings.Contains(msg, "Limite journalière de 5 000 USD") || !strings.Contains(msg, "déjà changé 4 000 USD") {
		t.Fatalf("message: %s", msg)
	}

	t.Setenv("DAILY_LIMIT_USD", "0")
	base.DayTotalUSD = 1e6
	if HasBlock(Blocks(base, now), BlockDailyLimit) {
		t.Fatal("0 disables the daily limit")
	}
}
