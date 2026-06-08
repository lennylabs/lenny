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
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// runtimeUpgradeProbeTimeout bounds the webhook's call to the gateway
// runtime-upgrade-active endpoint when HTTPRuntimeUpgradeProbe has no
// client configured.
const runtimeUpgradeProbeTimeout = 5 * time.Second

// RuntimeUpgradeProbe reports whether the named SandboxWarmPool has an
// active (non-terminal) §10.5 RuntimeUpgrade record. A query failure
// returns an error so the deletion guard fails closed.
//
// spec: §10.5 line 508.
type RuntimeUpgradeProbe interface {
	ActiveForPool(ctx context.Context, pool string) (active bool, err error)
}

// HTTPRuntimeUpgradeProbe queries the gateway
// GET /internal/runtime-upgrade/active endpoint over HTTP. A non-200
// response or a transport failure surfaces as an error so the webhook
// denies the SandboxTemplate deletion fail-closed.
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

// SandboxTemplateDeletionGuard returns the Decider for the
// lenny-sandboxtemplate-deletion-guard ValidatingAdmissionWebhook
// (§10.5 line 508, the "key safety invariant"). It is installed on
// SandboxTemplate DELETE in agent namespaces and blocks deletion of an
// old runtime template while a RuntimeUpgrade referencing its pool is
// still active, so no operator command outside the upgrade state machine
// can delete the template the upgrade may still need to roll back to.
//
// For each delete the decider lists every SandboxWarmPool whose
// templateRef names the template, then queries the gateway for each
// referencing pool's RuntimeUpgrade state. A delete is denied with 409
// when any referencing pool has an active upgrade. The control is
// fail-closed: a failure to list the referencing pools or to reach the
// gateway denies the delete (503) rather than admitting an unverifiable
// deletion.
func SandboxTemplateDeletionGuard(reader client.Reader, probe RuntimeUpgradeProbe) Decider {
	return func(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
		if req.Operation != admissionv1.Delete {
			return Allow()
		}
		template := req.Name
		if template == "" {
			// A collection delete carries no single object name; the
			// guard cannot resolve a referencing pool, so it has nothing
			// to protect here.
			return Allow()
		}
		pools, err := poolsReferencingTemplate(ctx, reader, req.Namespace, template)
		if err != nil {
			return Deny(http.StatusServiceUnavailable, fmt.Sprintf(
				"SandboxTemplate %q deletion blocked: cannot list referencing SandboxWarmPools to verify no active RuntimeUpgrade (§10.5 line 508): %v",
				template, err))
		}
		for _, pool := range pools {
			active, err := probe.ActiveForPool(ctx, pool)
			if err != nil {
				return Deny(http.StatusServiceUnavailable, fmt.Sprintf(
					"SandboxTemplate %q deletion blocked: cannot reach the gateway to check the RuntimeUpgrade state of pool %q (§10.5 line 508): %v",
					template, pool, err))
			}
			if active {
				return Deny(http.StatusConflict, fmt.Sprintf(
					"SandboxTemplate %q deletion blocked: SandboxWarmPool %q has an active RuntimeUpgrade. The old SandboxTemplate is preserved until the upgrade reaches Complete (§10.5 line 508); roll back or complete the upgrade before deleting the template.",
					template, pool))
			}
		}
		return Allow()
	}
}

// poolsReferencingTemplate lists the SandboxWarmPools in namespace whose
// spec.templateRef names template. SandboxTemplate and SandboxWarmPool
// are namespaced and a pool references a template in its own namespace,
// so the lookup is scoped to the deleted template's namespace.
func poolsReferencingTemplate(ctx context.Context, reader client.Reader, namespace, template string) ([]string, error) {
	if reader == nil {
		return nil, fmt.Errorf("cluster reader not configured")
	}
	var pools lennyv1.SandboxWarmPoolList
	if err := reader.List(ctx, &pools, client.InNamespace(namespace)); err != nil {
		return nil, err
	}
	var out []string
	for i := range pools.Items {
		if pools.Items[i].Spec.TemplateRef == template {
			out = append(out, pools.Items[i].Name)
		}
	}
	return out, nil
}
