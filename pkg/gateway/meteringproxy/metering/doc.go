// SPDX-License-Identifier: MIT

// Package metering is the metering proxy's usage accounting: the recorder
// that turns the LLM proxy's authoritative token counts, and the direct-mode
// deltas the gateway pulls from pods, into the usage store, the per-session
// token totals, the quota counters, and the mid-session budget enforcer;
// and the per-session budget state the proxy serves to the session core and
// the platform tools seam as seam.BudgetTracking and seam.BudgetResolutions.
//
// It reaches the session core only through pkg/gateway/seam: the recorder
// stamps session activity through GWSEAM-StampActivity.
//
// spec: GWLLM, USAGE, QUOTA, BUDGET
package metering
