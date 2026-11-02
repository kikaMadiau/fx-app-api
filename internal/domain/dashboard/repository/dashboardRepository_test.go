package repository

import (
	"testing"
	"time"
)

func TestDayBoundsAcrossMidnight(t *testing.T) {
	loc, err := time.LoadLocation("Africa/Kinshasa")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 2, 0, 5, 0, 0, loc)
	today, yesterday, tomorrow := DayBounds(now, loc)
	if today.Format(time.RFC3339) != "2026-11-02T00:00:00+01:00" {
		t.Fatalf("today start = %s", today)
	}
	if yesterday.Format("2006-01-02 15:04") != "2026-11-01 00:00" {
		t.Fatalf("yesterday start = %s", yesterday)
	}
	if tomorrow.Format("2006-01-02 15:04") != "2026-11-03 00:00" {
		t.Fatalf("tomorrow start = %s", tomorrow)
	}
}
func TestMonthStartAtMonthBoundary(t *testing.T) {
	loc, err := time.LoadLocation("Africa/Kinshasa")
	if err != nil {
		t.Fatal(err)
	}
	got := MonthStart(time.Date(2026, 11, 1, 0, 1, 0, 0, loc), loc)
	if got.Format("2006-01-02 15:04") != "2026-11-01 00:00" {
		t.Fatalf("month start = %s", got)
	}
}
