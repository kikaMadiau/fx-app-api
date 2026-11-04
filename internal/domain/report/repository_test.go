package report

import (
	"testing"
	"time"
)

func TestBoundsUsesLocalDaysAndIncludesEndDate(t *testing.T) {
	loc, _ := time.LoadLocation("Africa/Kinshasa")
	start, end, err := Bounds("2026-11-01", "2026-11-03", loc)
	if err != nil {
		t.Fatal(err)
	}
	if got := start.UTC().Format(time.RFC3339); got != "2026-10-31T23:00:00Z" {
		t.Errorf("start = %s, want 2026-10-31T23:00:00Z (minuit à Kinshasa)", got)
	}
	if got := end.UTC().Format(time.RFC3339); got != "2026-11-03T23:00:00Z" {
		t.Errorf("end = %s, want 2026-11-03T23:00:00Z (lendemain du dernier jour)", got)
	}
}

func TestBoundsRejectsInvalidPeriods(t *testing.T) {
	loc, _ := time.LoadLocation("Africa/Kinshasa")
	for _, c := range [][2]string{{"2026-11-03", "2026-11-01"}, {"03/11/2026", "2026-11-03"}, {"2025-01-01", "2026-06-01"}} {
		if _, _, err := Bounds(c[0], c[1], loc); err == nil {
			t.Errorf("Bounds(%s, %s) should fail", c[0], c[1])
		}
	}
}

func TestLargeThresholdFollowsConfiguredRate(t *testing.T) {
	t.Setenv("REPORT_LARGE_TRANSACTION_USD", "5000")
	t.Setenv("EXCHANGE_RATE_USD_CDF_BUY", "2800")
	if usd, cdf := LargeThreshold(); usd != 5000 || cdf != 14_000_000 {
		t.Errorf("LargeThreshold() = %v, %v", usd, cdf)
	}
}
