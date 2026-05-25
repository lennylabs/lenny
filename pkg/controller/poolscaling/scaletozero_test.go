// SPDX-License-Identifier: MIT

package poolscaling

import (
	"testing"
	"time"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
)

// spec: §4.6.1 line 400 — scaleToZero sets minWarm:0 while the cron
// window between schedule and resumeAt is open; both expressions are
// UTC by default with an optional IANA timezone override.
func TestScaleToZeroActive(t *testing.T) {
	utc := func(h int) time.Time {
		return time.Date(2026, 5, 24, h, 0, 0, 0, time.UTC)
	}
	// Window opens at 22:00 and resumes at 06:00 (UTC).
	policy := &lennyv1.ScaleToZeroPolicy{Schedule: "0 22 * * *", ResumeAt: "0 6 * * *"}

	cases := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"midday outside window", utc(12).Add(30 * time.Minute), false},
		{"late evening inside window", utc(23), true},
		{"after midnight inside window", utc(3), true},
		{"just after resume outside window", utc(6).Add(30 * time.Minute), false},
		{"just after open inside window", utc(22).Add(30 * time.Minute), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := scaleToZeroActive(policy, tc.now)
			if err != nil {
				t.Fatalf("scaleToZeroActive: %v", err)
			}
			if got != tc.want {
				t.Errorf("at %s: active=%v, want %v", tc.now.Format(time.Kitchen), got, tc.want)
			}
		})
	}
}

func TestScaleToZeroActiveNilPolicy(t *testing.T) {
	got, err := scaleToZeroActive(nil, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Error("nil policy must never be active")
	}
}

// A timezone override shifts the wall-clock window. With a Berlin
// (UTC+2 in May) zone, the 22:00 local open is 20:00 UTC, so 21:00 UTC
// already falls inside the window.
func TestScaleToZeroActiveTimezoneOverride(t *testing.T) {
	policy := &lennyv1.ScaleToZeroPolicy{
		Schedule: "0 22 * * *",
		ResumeAt: "0 6 * * *",
		Timezone: "Europe/Berlin",
	}
	now := time.Date(2026, 5, 24, 21, 0, 0, 0, time.UTC)
	got, err := scaleToZeroActive(policy, now)
	if err != nil {
		t.Fatalf("scaleToZeroActive: %v", err)
	}
	if !got {
		t.Error("21:00 UTC is 23:00 Berlin time, inside the window; want active")
	}
}

func TestScaleToZeroActiveRejectsBadInput(t *testing.T) {
	cases := []struct {
		name   string
		policy *lennyv1.ScaleToZeroPolicy
	}{
		{"bad timezone", &lennyv1.ScaleToZeroPolicy{Schedule: "0 22 * * *", ResumeAt: "0 6 * * *", Timezone: "Mars/Olympus"}},
		{"bad schedule", &lennyv1.ScaleToZeroPolicy{Schedule: "not a cron", ResumeAt: "0 6 * * *"}},
		{"bad resumeAt", &lennyv1.ScaleToZeroPolicy{Schedule: "0 22 * * *", ResumeAt: "99 99 * * *"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := scaleToZeroActive(tc.policy, time.Now()); err == nil {
				t.Error("expected an error for invalid input")
			}
		})
	}
}
