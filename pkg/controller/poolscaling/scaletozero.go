// SPDX-License-Identifier: MIT

package poolscaling

import (
	"fmt"
	"time"

	cron "github.com/robfig/cron/v3"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// scaleToZeroParser parses the standard five-field cron expressions the
// §4.6.1 scaleToZero window uses, plus the @-descriptor forms. It is the
// PoolScalingController's embedded cron scheduler the spec names at
// §4.6.1 line 400.
var scaleToZeroParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// scaleToZeroActive reports whether now falls inside the pool's
// scale-to-zero window. The window opens when the Schedule cron fires
// and closes when the ResumeAt cron fires; while it is open the pool's
// warm floor is overridden to zero (spec/04_system-components.md §4.6.1
// line 400).
//
// Both cron expressions are interpreted in the policy's Timezone,
// defaulting to UTC when unset. Membership is decided by comparing the
// next firing of each expression after now: when the next resume fires
// strictly before the next schedule, the pool is currently inside the
// window and waiting to resume. A nil policy is never active.
func scaleToZeroActive(p *lennyv1.ScaleToZeroPolicy, now time.Time) (bool, error) {
	if p == nil {
		return false, nil
	}
	loc, err := scaleToZeroLocation(p.Timezone)
	if err != nil {
		return false, err
	}
	down, err := parseScaleToZeroCron(p.Schedule, loc)
	if err != nil {
		return false, fmt.Errorf("scaleToZero.schedule: %w", err)
	}
	up, err := parseScaleToZeroCron(p.ResumeAt, loc)
	if err != nil {
		return false, fmt.Errorf("scaleToZero.resumeAt: %w", err)
	}
	nextDown := down.Next(now)
	nextUp := up.Next(now)
	return nextUp.Before(nextDown), nil
}

// scaleToZeroLocation resolves the policy's timezone, defaulting to UTC
// when the field is empty. Any IANA timezone string is accepted per
// §4.6.1 line 400.
func scaleToZeroLocation(tz string) (*time.Location, error) {
	if tz == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", tz, err)
	}
	return loc, nil
}

// parseScaleToZeroCron parses one cron expression in the supplied
// location. The location is threaded through the CRON_TZ prefix the
// robfig parser honors so the schedule fires at the wall-clock time in
// that zone.
func parseScaleToZeroCron(expr string, loc *time.Location) (cron.Schedule, error) {
	spec := "CRON_TZ=" + loc.String() + " " + expr
	sched, err := scaleToZeroParser.Parse(spec)
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression %q: %w", expr, err)
	}
	return sched, nil
}
