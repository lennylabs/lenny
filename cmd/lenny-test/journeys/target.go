// SPDX-License-Identifier: MIT

package journeys

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
)

// TargetKind is the only cluster target the tier has.
const TargetKind = "kind"

// Target creates and deletes the cluster one trial runs on. Kind is the
// only implementation. A cloud target (a managed cluster on AWS, GCP, or
// Azure) implements the same methods, reports what the cluster cost, and
// is selected by the scenario's target field, so adding one changes
// neither the runner nor the scenario format.
type Target interface {
	// Name is the value of the scenario's target field.
	Name() string
	// Create creates the cluster and prepares it to the setup level,
	// writing its progress to log. It removes a cluster left by an
	// earlier run first.
	Create(ctx context.Context, setup string, log io.Writer) error
	// Kubeconfig is the path of a kubeconfig with cluster-admin access
	// to the created cluster.
	Kubeconfig() string
	// Delete deletes the cluster and everything Create made.
	Delete(ctx context.Context, log io.Writer) error
	// VerifyDeleted confirms that nothing Create made is left. The
	// runner calls it after Delete on every exit path, and a failure
	// stops the run, because a leaked cluster keeps costing.
	VerifyDeleted(ctx context.Context) error
	// AgentEndpoint returns the Docker network the agent container joins
	// and a kubeconfig whose server is reachable from that network. The
	// agent reaches the cluster through it and through nothing on the
	// host.
	AgentEndpoint(ctx context.Context) (AgentEndpoint, error)
	// Facts are values Create resolved that a scenario may name, such as
	// echoImage, the digest-pinned echo runtime image.
	Facts() map[string]string
	// CostUSD is the infrastructure cost of the trial so far. Kind
	// reports zero.
	CostUSD() float64
	// MaxCostUSD is the most one trial's infrastructure may cost. The
	// ledger reserves it, with the agent's cap, before a trial starts.
	MaxCostUSD() float64
}

// AgentEndpoint is how the agent container reaches a cluster.
type AgentEndpoint struct {
	// Network is the Docker network the container joins.
	Network string
	// Kubeconfig is a kubeconfig valid from that network.
	Kubeconfig []byte
}

// TargetFactory builds a target for one trial. dir is the trial's
// scratch directory for kubeconfigs and generated files.
type TargetFactory func(dir string) Target

// Targets maps a scenario's target field to its factory.
type Targets map[string]TargetFactory

// New returns the target the scenario names.
func (t Targets) New(name, dir string) (Target, error) {
	f, ok := t[name]
	if !ok {
		names := make([]string, 0, len(t))
		for n := range t {
			names = append(names, n)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("no cluster target %q; the targets are %s", name, strings.Join(names, ", "))
	}
	return f(dir), nil
}
