// SPDX-License-Identifier: MIT

package tokenservice

import (
	"sync"
	"time"
)

// §13.3 line 607 / line 611 limit tier vocabulary. The labels match
// the §16.1 `lenny_oauth_token_rate_limited_total` /
// `lenny_oauth_token_rate_limited_sampled_total` label vocabulary and
// the §13.3 `token.exchange_rate_limited` audit payload's `limit_tier`
// field.
const (
	LimitTierCallerPerSecond = "caller_per_second"
	LimitTierCallerPerMinute = "caller_per_minute"
	LimitTierTenantPerSecond = "tenant_per_second"
)

// RateLimitOptions configures the §13.3 per-(tenant, sub) and global
// per-tenant rate limits on POST /v1/oauth/token. Zero on any field
// disables that tier. The §13.3 line 607 defaults are 10 req/s and
// 300 req/min per (tenant, sub) plus 100 req/s per tenant; the binary
// flag layer fills them in.
type RateLimitOptions struct {
	// CallerPerSecond caps per-(tenant, sub) per-second.
	CallerPerSecond int
	// CallerPerMinute caps per-(tenant, sub) per-minute.
	CallerPerMinute int
	// TenantPerSecond caps per-tenant per-second.
	TenantPerSecond int
	// SampleWindow is the §13.3 line 611 rolling window for audit
	// sampling (first rejection per (tenant, sub, tier) emits the row;
	// subsequent rejections in the window increment the sampled
	// counter only). Zero means 10 seconds, the §13.3 default.
	SampleWindow time.Duration
}

// IsZero reports whether every limit and the sample window are unset.
func (o RateLimitOptions) IsZero() bool {
	return o.CallerPerSecond <= 0 && o.CallerPerMinute <= 0 &&
		o.TenantPerSecond <= 0 && o.SampleWindow <= 0
}

// RateLimitDecision captures the per-call rate-limit outcome the
// handler maps to an HTTP response and to the §13.3 sampled audit
// path.
type RateLimitDecision struct {
	Allowed      bool
	LimitTier    string
	RetryAfter   time.Duration
	AuditSampled bool
}

// RateLimiter enforces the §13.3 token-endpoint rate-limit budgets in
// per-replica memory. It backs both the audit-sampling decision and
// the 429 response.
//
// spec: §13.3 line 607 (limits), line 609 (sampled audit), line 611
// (per-replica local state).
type RateLimiter struct {
	opts   RateLimitOptions
	now    func() time.Time
	window time.Duration

	mu sync.Mutex
	// bucketSec tracks per-key per-second counters. Key shapes:
	//   "c1:"+tenant+":"+sub      caller per-second
	//   "t1:"+tenant              tenant per-second
	bucketSec map[string]secBucket
	// bucketMin tracks per-(tenant, sub) per-minute counters.
	bucketMin map[string]minBucket
	// sampleLast tracks the last sample emit time per
	// (tenant, sub, tier).
	sampleLast map[string]time.Time
}

type secBucket struct {
	sec   int64
	count int
}

type minBucket struct {
	min   int64
	count int
}

// NewRateLimiter constructs a §13.3 rate limiter. now overrides
// time.Now for tests; production passes nil.
func NewRateLimiter(opts RateLimitOptions, now func() time.Time) *RateLimiter {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	window := opts.SampleWindow
	if window <= 0 {
		window = 10 * time.Second
	}
	return &RateLimiter{
		opts:       opts,
		now:        now,
		window:     window,
		bucketSec:  map[string]secBucket{},
		bucketMin:  map[string]minBucket{},
		sampleLast: map[string]time.Time{},
	}
}

// Now returns the limiter's current wall-clock instant.
func (l *RateLimiter) Now() time.Time { return l.now().UTC() }

// Allow returns the decision for one request. The limiter increments
// every applicable per-second / per-minute bucket on Allow; a denied
// request still consumes a slot in the bucket that denied it so
// repeated probes inside the window stay denied.
func (l *RateLimiter) Allow(at time.Time, tenantID, sub string) RateLimitDecision {
	at = at.UTC()
	l.mu.Lock()
	defer l.mu.Unlock()

	// Tenant-per-second bucket (one bucket per tenant).
	if l.opts.TenantPerSecond > 0 && tenantID != "" {
		key := "t1:" + tenantID
		next, ok := l.incSec(key, at, l.opts.TenantPerSecond)
		if !ok {
			return l.deny(tenantID, sub, LimitTierTenantPerSecond, at, next)
		}
	}
	// Caller per-second bucket.
	if l.opts.CallerPerSecond > 0 && tenantID != "" && sub != "" {
		key := "c1:" + tenantID + ":" + sub
		next, ok := l.incSec(key, at, l.opts.CallerPerSecond)
		if !ok {
			return l.deny(tenantID, sub, LimitTierCallerPerSecond, at, next)
		}
	}
	// Caller per-minute bucket.
	if l.opts.CallerPerMinute > 0 && tenantID != "" && sub != "" {
		key := "c60:" + tenantID + ":" + sub
		next, ok := l.incMin(key, at, l.opts.CallerPerMinute)
		if !ok {
			return l.deny(tenantID, sub, LimitTierCallerPerMinute, at, next)
		}
	}
	return RateLimitDecision{Allowed: true}
}

// incSec increments and reports whether the bucket is still under
// limit. It returns the wall-clock instant the bucket resets.
func (l *RateLimiter) incSec(key string, at time.Time, limit int) (time.Time, bool) {
	sec := at.Unix()
	b := l.bucketSec[key]
	if b.sec != sec {
		b = secBucket{sec: sec, count: 0}
	}
	b.count++
	l.bucketSec[key] = b
	next := time.Unix(sec+1, 0).UTC()
	if b.count > limit {
		return next, false
	}
	return next, true
}

// incMin increments the per-minute counter.
func (l *RateLimiter) incMin(key string, at time.Time, limit int) (time.Time, bool) {
	min := at.Unix() / 60
	b := l.bucketMin[key]
	if b.min != min {
		b = minBucket{min: min, count: 0}
	}
	b.count++
	l.bucketMin[key] = b
	next := time.Unix((min+1)*60, 0).UTC()
	if b.count > limit {
		return next, false
	}
	return next, true
}

// deny builds the deny decision and computes whether this rejection
// should emit a sampled audit row (first rejection per
// (tenant, sub, tier) in the window per §13.3 line 609).
func (l *RateLimiter) deny(tenantID, sub, tier string, at, resetAt time.Time) RateLimitDecision {
	retry := time.Until(resetAt)
	if retry <= 0 {
		retry = time.Second
	}
	sampleKey := tier + ":" + tenantID + ":" + sub
	sampled := false
	if last, ok := l.sampleLast[sampleKey]; !ok || at.Sub(last) >= l.window {
		sampled = true
		l.sampleLast[sampleKey] = at
	}
	return RateLimitDecision{
		Allowed:      false,
		LimitTier:    tier,
		RetryAfter:   retry,
		AuditSampled: sampled,
	}
}
