// SPDX-License-Identifier: MIT

package cron_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/cron"
)

var ref = time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)

func TestNextDailyBackup(t *testing.T) {
	s, err := cron.Parse("0 2 * * *")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got, err := s.Next(ref)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	want := time.Date(2026, 5, 18, 2, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Next = %v, want %v", got, want)
	}
}

func TestNextEverySixHours(t *testing.T) {
	s, err := cron.Parse("0 */6 * * *")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got, err := s.Next(ref)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	want := time.Date(2026, 5, 17, 18, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Next = %v, want %v (the next 6-hour boundary)", got, want)
	}
}

func TestNextWeekly(t *testing.T) {
	s, err := cron.Parse("0 3 * * 0")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got, err := s.Next(ref)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got.Weekday() != time.Sunday || got.Hour() != 3 || got.Minute() != 0 {
		t.Errorf("Next = %v, want a Sunday at 03:00", got)
	}
}

func TestNextMonthly(t *testing.T) {
	s, err := cron.Parse("0 4 1 * *")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got, err := s.Next(ref)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got.Day() != 1 || got.Hour() != 4 || got.Minute() != 0 {
		t.Errorf("Next = %v, want the 1st of a month at 04:00", got)
	}
}

func TestNextStrictlyAfterCurrentMatch(t *testing.T) {
	s, err := cron.Parse("0 2 * * *")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Evaluating from a time that itself matches yields the next
	// occurrence, not the same instant.
	got, err := s.Next(time.Date(2026, 5, 17, 2, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	want := time.Date(2026, 5, 18, 2, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Next = %v, want %v", got, want)
	}
}

func TestNextRangeAndList(t *testing.T) {
	weekdays, err := cron.Parse("0 0 * * 1-5")
	if err != nil {
		t.Fatalf("Parse weekdays: %v", err)
	}
	got, err := weekdays.Next(ref)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got.Weekday() == time.Saturday || got.Weekday() == time.Sunday {
		t.Errorf("Next = %v (%s), want a weekday", got, got.Weekday())
	}

	twice, err := cron.Parse("0 0,12 * * *")
	if err != nil {
		t.Fatalf("Parse list: %v", err)
	}
	got, err = twice.Next(ref)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got.Hour() != 0 && got.Hour() != 12 {
		t.Errorf("Next hour = %d, want 0 or 12", got.Hour())
	}
}

func TestNextDayOfMonthOrDayOfWeek(t *testing.T) {
	// When both day fields are restricted, cron matches either.
	s, err := cron.Parse("0 0 13 * 5")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got, err := s.Next(ref)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got.Day() != 13 && got.Weekday() != time.Friday {
		t.Errorf("Next = %v, want the 13th or a Friday", got)
	}
}

func TestNextImpossibleExpression(t *testing.T) {
	s, err := cron.Parse("0 0 30 2 *") // 30 February never occurs
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := s.Next(ref); err == nil {
		t.Error("Next for an unsatisfiable expression returned no error")
	}
}

func TestParseRejectsBadExpressions(t *testing.T) {
	for _, bad := range []string{
		"0 2 * *",     // too few fields
		"0 2 * * * *", // too many fields
		"60 2 * * *",  // minute out of range
		"0 24 * * *",  // hour out of range
		"0 2 0 * *",   // day-of-month below 1
		"0 2 * 13 *",  // month out of range
		"0 2 * * abc", // non-numeric
		"*/0 * * * *", // zero step
		"0 2 5-1 * *", // inverted range
	} {
		if _, err := cron.Parse(bad); err == nil {
			t.Errorf("Parse(%q) = nil error, want a rejection", bad)
		}
	}
}
