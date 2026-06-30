// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/admission/ownership"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/statelessproxy"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/statelessrouting"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/tenantaffinity"
)

// labelPool is the §5.2 pod label the WarmPoolController stamps on every
// pod that belongs to a pool. The stateless EndpointLister discovers a
// pool's pods through it.
const labelPool = "lenny.dev/pool"

// podEndpointLister discovers the pod IPs and readiness behind one
// stateless pool by listing the pool's agent pods. It projects the same
// pod-IP / readiness set a discovery.k8s.io/v1 EndpointSlice for the
// pool Service would carry (the pod's Ready condition is exactly what an
// EndpointSlice mirrors as Conditions.Ready), sourced directly from the
// pods because no per-pool Service fronts a stateless pool in v1.
//
// spec: §5.2 line 500.
type podEndpointLister struct {
	client    client.Client
	namespace string
	pool      string
}

func (l podEndpointLister) ListEndpoints(ctx context.Context) ([]tenantaffinity.Endpoint, error) {
	var pods corev1.PodList
	if err := l.client.List(ctx, &pods,
		client.InNamespace(l.namespace),
		client.MatchingLabels{labelPool: l.pool}); err != nil {
		return nil, fmt.Errorf("list stateless pool %q pods: %w", l.pool, err)
	}
	out := make([]tenantaffinity.Endpoint, 0, len(pods.Items))
	for i := range pods.Items {
		p := &pods.Items[i]
		ip := p.Status.PodIP
		if ip == "" || p.DeletionTimestamp != nil {
			continue
		}
		out = append(out, tenantaffinity.Endpoint{PodIP: ip, Ready: podReady(p)})
	}
	return out, nil
}

// podReady reports the §5.2 line 500 readiness signal for a pod: the
// PodReady condition is True (driven by the pod's slot-capacity
// readiness probe).
func podReady(p *corev1.Pod) bool {
	if p.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// podTenantLabeler stamps the §5.2 line 500 lenny.dev/tenant-id pin label
// on the pod at a given IP when the router newly pins it. It resolves
// IP→pod by scanning the agent namespace (status.podIP is not a
// server-selectable field for pods); the scan runs only on a NewlyPinned
// decision, which is rare (once per tenant per pod). The label backstops
// the in-memory router pin at the Kubernetes layer via the
// lenny-tenant-label-immutability webhook.
type podTenantLabeler struct {
	client    client.Client
	namespace string
}

func (l podTenantLabeler) LabelTenant(ctx context.Context, podIP, tenantID string) error {
	if tenantID == "" || podIP == "" {
		return nil
	}
	var pods corev1.PodList
	if err := l.client.List(ctx, &pods, client.InNamespace(l.namespace)); err != nil {
		return fmt.Errorf("list pods to label IP %s: %w", podIP, err)
	}
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Status.PodIP != podIP {
			continue
		}
		if p.Labels[podclaim.LabelTenant] == tenantID {
			return nil // already pinned
		}
		body := fmt.Sprintf(`{"metadata":{"labels":{%q:%q}}}`, podclaim.LabelTenant, tenantID)
		patch := &corev1.Pod{}
		patch.Name = p.Name
		patch.Namespace = p.Namespace
		return l.client.Patch(ctx, patch, client.RawPatch(types.MergePatchType, []byte(body)),
			client.FieldOwner(string(ownership.Gateway)))
	}
	return fmt.Errorf("no pod found at IP %s to pin", podIP)
}

// buildStatelessRouting assembles the §5.2 concurrent-stateless ingress
// Manager: per-pool EndpointPollers discover pod IPs, the tenant-affinity
// Router decides, and the statelessproxy reverse-proxies to the pinned
// pod. baseCtx bounds every poller. F-5.2.29.
func buildStatelessRouting(
	baseCtx context.Context,
	cl client.Client,
	namespace string,
	pools poolstore.Store,
	metrics tenantaffinity.StatelessMetrics,
) *statelessrouting.Manager {
	return statelessrouting.New(baseCtx, statelessrouting.Options{
		Resolver: func(ctx context.Context, name string) (statelessrouting.PoolInfo, bool, error) {
			p, err := pools.Get(ctx, name)
			if err != nil {
				if err == poolstore.ErrNotFound {
					return statelessrouting.PoolInfo{}, false, nil
				}
				return statelessrouting.PoolInfo{}, false, err
			}
			// spec: §5.2 — service mode is the claimless replicated-service
			// path the stateless router fronts (the former
			// concurrencyStyle: stateless). MaxConcurrent is the service-mode
			// per-pod request capacity.
			stateless := p.ExecutionMode == runtimestore.ExecutionModeService
			return statelessrouting.PoolInfo{
				Stateless:     stateless,
				MaxConcurrent: p.MaxConcurrent,
			}, true, nil
		},
		NewLister: func(name string) tenantaffinity.EndpointLister {
			return podEndpointLister{client: cl, namespace: namespace, pool: name}
		},
		Labeler: podTenantLabeler{client: cl, namespace: namespace},
		Tenant:  tenantFromRequest,
		Metrics: metrics,
	})
}

// tenantFromRequest resolves the requesting tenant from the authenticated
// principal the §10.2 auth middleware stamped on the request context.
func tenantFromRequest(r *http.Request) (string, error) {
	if p, ok := authmw.FromContext(r.Context()); ok {
		return p.TenantID, nil
	}
	return "", fmt.Errorf("no authenticated principal")
}

var _ statelessproxy.PodLabeler = podTenantLabeler{}
