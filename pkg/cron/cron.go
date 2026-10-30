// SPDX-License-Identifier: MIT

// Package cron evaluates standard five-field cron expressions. The §25
// lenny-ops backup scheduler, the platform upgrade-check cron, and the
// verification schedules are all configured as cron strings; this
// package parses one and computes the next time it fires.
//
// A field is minute, hour, day-of-month, month, or day-of-week. Each
// accepts `*`, a value, a range `a-b`, a step `*/s` or `a-b/s`, and a
// comma-separated list of those. Day-of-week is 0-7 with both 0 and 7
// meaning Sunday.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// nextSearchLimit bounds the Next scan so an expression that can never
// match (for example 30 February) returns an error instead of looping.
const nextSearchLimit = 4 * 365 * 24 * time.Hour

// Schedule is a parsed cron expression. Each field is a bitmask of the
// values it permits.
type Schedule struct {
	minute, hour, dom, month, dow uint64
	// domRestricted and dowRestricted record whether the day-of-month
	// and day-of-week fields were narrowed from `*`. When both are,
	// standard cron matches a day that satisfies either one.
	domRestricted, dowRestricted bool
}

// Parse parses a five-field cron expression.
func Parse(expr string) (Schedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return Schedule{}, fmt.Errorf("cron: expected 5 fields, got %d in %q", len(fields), expr)
	}
	minute, err := parseField(fields[0], 0, 59)
	if err != nil {
		return Schedule{}, fmt.Errorf("cron: minute field: %w", err)
	}
	hour, err := parseField(fields[1], 0, 23)
	if err != nil {
		return Schedule{}, fmt.Errorf("cron: hour field: %w", err)
	}
	dom, err := parseField(fields[2], 1, 31)
	if err != nil {
		return Schedule{}, fmt.Errorf("cron: day-of-month field: %w", err)
	}
	month, err := parseField(fields[3], 1, 12)
	if err != nil {
		return Schedule{}, fmt.Errorf("cron: month field: %w", err)
	}
	dow, err := parseField(fields[4], 0, 7)
	if err != nil {
		return Schedule{}, fmt.Errorf("cron: day-of-week field: %w", err)
	}
	// Cron day-of-week 7 and 0 both mean Sunday; fold 7 onto 0 so
	// matching can use time.Weekday's 0-6 range directly.
	if dow&(1<<7) != 0 {
		dow |= 1 << 0
	}
	return Schedule{
		minute: minute, hour: hour, dom: dom, month: month, dow: dow,
		domRestricted: fields[2] != "*", dowRestricted: fields[4] != "*",
	}, nil
}

// Next returns the earliest time strictly after t at which the
// schedule fires, evaluated in t's location. It returns an error when
// no match exists within four years.
func (s Schedule) Next(t time.Time) (time.Time, error) {
	next := t.Truncate(time.Minute).Add(time.Minute)
	limit := next.Add(nextSearchLimit)
	for next.Before(limit) {
		if s.matches(next) {
			return next, nil
		}
		next = next.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("cron: no matching time within %s", nextSearchLimit)
}

// matches reports whether the schedule fires at minute-resolution t.
func (s Schedule) matches(t time.Time) bool {
	if s.minute&(1<<uint(t.Minute())) == 0 {
		return false
	}
	if s.hour&(1<<uint(t.Hour())) == 0 {
		return false
	}
	if s.month&(1<<uint(int(t.Month()))) == 0 {
		return false
	}
	domMatch := s.dom&(1<<uint(t.Day())) != 0
	dowMatch := s.dow&(1<<uint(int(t.Weekday()))) != 0
	// Standard cron: when both day fields are restricted, either match
	// satisfies the day; otherwise both must (the unrestricted one is
	// always satisfied).
	if s.domRestricted && s.dowRestricted {
		return domMatch || dowMatch
	}
	return domMatch && dowMatch
}

// parseField parses one comma-separated cron field into a bitmask of
// the values in [min, max] it permits.
func parseField(spec string, min, max int) (uint64, error) {
	var mask uint64
	for _, term := range strings.Split(spec, ",") {
		lo, hi, step, err := parseTerm(term, min, max)
		if err != nil {
			return 0, err
		}
		for v := lo; v <= hi; v += step {
			mask |= 1 << uint(v)
		}
	}
	return mask, nil
}

// parseTerm parses one cron term — `*`, `n`, `a-b`, `*/s`, or `a-b/s`
// — into an inclusive low/high bound and a step.
func parseTerm(term string, min, max int) (lo, hi, step int, err error) {
	step = 1
	base := term
	if slash := strings.IndexByte(term, '/'); slash >= 0 {
		base = term[:slash]
		step, err = strconv.Atoi(term[slash+1:])
		if err != nil || step < 1 {
			return 0, 0, 0, fmt.Errorf("invalid step in %q", term)
		}
	}
	switch {
	case base == "*":
		lo, hi = min, max
	case strings.IndexByte(base, '-') > 0:
		dash := strings.IndexByte(base, '-')
		lo, err = strconv.Atoi(base[:dash])
		if err != nil {
			return 0, 0, 0, fmt.Errorf("invalid range start in %q", term)
		}
		hi, err = strconv.Atoi(base[dash+1:])
		if err != nil {
			return 0, 0, 0, fmt.Errorf("invalid range end in %q", term)
		}
	default:
		lo, err = strconv.Atoi(base)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("invalid value %q", term)
		}
		hi = lo
	}
	if lo < min || hi > max || lo > hi {
		return 0, 0, 0, fmt.Errorf("term %q out of range [%d,%d]", term, min, max)
	}
	return lo, hi, step, nil
}
