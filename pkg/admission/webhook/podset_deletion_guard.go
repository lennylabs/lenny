// SPDX-License-Identifier: MIT

package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
)

// runtimeUpgradeProbeTimeout bounds the webhook's call to the gateway
// runtime-upgrade-active endpoint when HTTPRuntimeUpgradeProbe has no
// client configured.
const runtimeUpgradeProbeTimeout = 5 * time.Second

// RuntimeUpgradeProbe reports whether the named pool has an
// active (non-terminal) §10.5 RuntimeUpgrade record. A query failure
// returns an error so the deletion guard fails closed.
//
// spec: §10.5.
type RuntimeUpgradeProbe interface {
	ActiveForPool(ctx context.Context, pool string) (active bool, err error)
}

// HTTPRuntimeUpgradeProbe queries the gateway
// GET /internal/runtime-upgrade/active endpoint over HTTP. A non-200
// response or a transport failure surfaces as an error so the webhook
// denies the PodSet deletion fail-closed.
type HTTPRuntimeUpgradeProbe struct {
	// URL is the gateway GET /internal/runtime-upgrade/active endpoint.
	URL string
	// Client is the HTTP client; nil selects a client bounded by
	// runtimeUpgradeProbeTimeout.
	Client *http.Client
}

// ActiveForPool calls the gateway endpoint with ?pool=<pool> and reports
// the `active` flag from the response body.
func (p HTTPRuntimeUpgradeProbe) ActiveForPool(ctx context.Context, pool string) (bool, error) {
	if p.URL == "" {
		return false, fmt.Errorf("runtime-upgrade probe URL not configured")
	}
	u, err := url.Parse(p.URL)
	if err != nil {
		return false, fmt.Errorf("runtime-upgrade probe: parse URL: %w", err)
	}
	q := u.Query()
	q.Set("pool", pool)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return false, fmt.Errorf("runtime-upgrade probe: new request: %w", err)
	}
	c := p.Client
	if c == nil {
		c = &http.Client{Timeout: runtimeUpgradeProbeTimeout}
	}
	resp, err := c.Do(req)
	if err != nil {
		return false, fmt.Errorf("runtime-upgrade probe: do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("runtime-upgrade probe: gateway returned %d", resp.StatusCode)
	}
	var body struct {
		Active bool `json:"active"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, fmt.Errorf("runtime-upgrade probe: decode: %w", err)
	}
	return body.Active, nil
}

// PodSetDeletionGuard returns the Decider for the
// lenny-podset-deletion-guard ValidatingAdmissionWebhook (§10.5, the "key
// safety invariant"). It is installed on PodSet DELETE in agent
// namespaces and blocks deletion of a pool's PodSet while a
// RuntimeUpgrade of that pool is still active, so no operator command
// outside the upgrade state machine can delete the pod set the upgrade may
// still need to roll back to.
//
// A PodSet is named after its pool, so the decider queries the gateway
// for the RuntimeUpgrade state of the pool the deleted object names. A
// delete is denied with 409 when that pool has an active upgrade. The
// control is fail-closed: a failure to reach the gateway denies the delete
// (503) rather than admitting an unverifiable deletion.
func PodSetDeletionGuard(probe RuntimeUpgradeProbe) Decider {
	return func(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
		if req.Operation != admissionv1.Delete {
			return Allow()
		}
		pool := req.Name
		if pool == "" {
			// A collection delete carries no single object name; the
			// guard cannot resolve a pool, so it has nothing to protect
			// here.
			return Allow()
		}
		active, err := probe.ActiveForPool(ctx, pool)
		if err != nil {
			return Deny(http.StatusServiceUnavailable, fmt.Sprintf(
				"PodSet %q deletion blocked: cannot reach the gateway to check the RuntimeUpgrade state of pool %q (§10.5): %v",
				pool, pool, err,
			))
		}
		if active {
			return Deny(http.StatusConflict, fmt.Sprintf(
				"PodSet %q deletion blocked: pool %q has an active RuntimeUpgrade. The PodSet is preserved until the upgrade reaches Complete (§10.5); roll back or complete the upgrade before deleting the PodSet.",
				pool, pool,
			))
		}
		return Allow()
	}
}
