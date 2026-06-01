// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"strings"
)

// requiredRedisMaxmemoryPolicy is the only eviction policy §12.4 admits
// for the Redis instance that backs the billing stream and the
// per-tenant counters: those keys MUST NOT be silently evicted under
// memory pressure. Any other policy lets Redis drop a billing entry or a
// quota counter when maxmemory is reached, which corrupts metering and
// quota enforcement.
const requiredRedisMaxmemoryPolicy = "noeviction"

// RedisConfigProber reads a single Redis CONFIG parameter (the live
// value of `CONFIG GET <param>`) from the operator's bring-your-own
// Redis. The lenny-preflight Job constructs a real prober over a
// go-redis client; tests pass a fake. An empty value with a nil error
// means the parameter is unset; a non-nil error means the instance was
// unreachable.
type RedisConfigProber interface {
	ConfigGet(ctx context.Context, param string) (string, error)
}

// RedisConfigProbeFunc adapts a function to RedisConfigProber.
type RedisConfigProbeFunc func(ctx context.Context, param string) (string, error)

// ConfigGet implements RedisConfigProber.
func (f RedisConfigProbeFunc) ConfigGet(ctx context.Context, param string) (string, error) {
	return f(ctx, param)
}

// CheckRedisMaxmemoryPolicy audits a bring-your-own Redis for the §12.4
// `maxmemory-policy: noeviction` invariant. The cloud Terraform sets the
// policy on managed Redis (AWS / Azure / GCP), but the self-managed
// profile points the gateway at an operator-supplied Redis the chart
// does not deploy, so the policy is unverified there and the
// BillingStreamEvictionPolicyDrift alert has no INFO-memory signal to
// fire from on a BYO topology. This preflight check closes that gap: it
// reads the live policy and fails closed when it is anything other than
// noeviction, before the install completes against a Redis that can
// silently drop billing-stream or per-tenant-counter keys.
//
// A read failure is surfaced as a failed check, consistent with the
// fail-closed posture of the preflight Job.
//
// spec: §12.4 — billing stream and per-tenant counters must not evict.
func CheckRedisMaxmemoryPolicy(ctx context.Context, prober RedisConfigProber) Decision {
	policy, err := prober.ConfigGet(ctx, "maxmemory-policy")
	if err != nil {
		return Decision{Reason: "REDIS_UNREACHABLE: CONFIG GET maxmemory-policy: " + err.Error()}
	}
	policy = strings.ToLower(strings.TrimSpace(policy))
	if policy == "" {
		return Decision{Reason: "REDIS_MAXMEMORY_POLICY_UNKNOWN: CONFIG GET maxmemory-policy returned no value; " +
			"§12.4 requires maxmemory-policy=noeviction so the billing stream and per-tenant counters cannot evict."}
	}
	if policy != requiredRedisMaxmemoryPolicy {
		return Decision{Reason: "REDIS_EVICTION_POLICY_DRIFT: maxmemory-policy=" + policy +
			"; §12.4 requires noeviction. Under memory pressure this policy can silently drop billing-stream " +
			"or per-tenant-counter keys, corrupting metering and quota enforcement."}
	}
	return Decision{Passed: true}
}
