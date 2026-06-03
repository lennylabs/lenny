// SPDX-License-Identifier: MIT

package prodsource

import (
	"context"
	"sort"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
	"github.com/lennylabs/lenny/pkg/ops/probe"
)

// ProbeConnectivity is the §25.6 connectivity reader. It runs the §25.4
// dependency probes (Postgres, Redis, MinIO, the Kubernetes API, the
// gateway, registered connectors) and projects their results onto the
// §25.6 connectivity dependency list. spec: §25.6 line 2906. F-25.6.1.
type ProbeConnectivity struct {
	probes  map[string]probe.Func
	timeout time.Duration
}

// NewProbeConnectivity returns a connectivity reader over the named
// probes, bounding each probe by timeout.
func NewProbeConnectivity(probes map[string]probe.Func, timeout time.Duration) *ProbeConnectivity {
	return &ProbeConnectivity{probes: probes, timeout: timeout}
}

// Compile-time assertion that *ProbeConnectivity satisfies the seam.
var _ Connectivity = (*ProbeConnectivity)(nil)

// Probe runs every dependency probe concurrently and returns the results
// ordered by dependency name so the report is deterministic.
func (p *ProbeConnectivity) Probe(ctx context.Context) ([]diagnostics.ConnectivityDependency, error) {
	results := probe.Run(ctx, p.probes, p.timeout)
	deps := make([]diagnostics.ConnectivityDependency, 0, len(results))
	for name, res := range results {
		deps = append(deps, diagnostics.ConnectivityDependency{
			Name:       name,
			Reachable:  res.OK,
			DurationMs: res.Duration.Milliseconds(),
			Detail:     res.Detail,
		})
	}
	sort.Slice(deps, func(i, j int) bool { return deps[i].Name < deps[j].Name })
	return deps, nil
}
