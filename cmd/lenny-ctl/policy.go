// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"io"

	"github.com/lennylabs/lenny/pkg/ctl"
	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// cmdPolicy implements the §24.14 policy-management group. Its single
// subcommand, `audit-isolation`, is a read-only platform-admin audit
// that reports every DelegationPolicy rule × pool combination where a
// delegation would be rejected at runtime by the §8.3 isolation
// monotonicity check.
//
// spec: §24.14 line 172.
func cmdPolicy(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: policy requires a subcommand (audit-isolation)")
		return 2
	}
	switch args[0] {
	case "audit-isolation":
		return cmdPolicyAuditIsolation(ctx, c, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown policy subcommand %q\n", args[0])
		return 2
	}
}

// policyListWire is the GET /v1/admin/delegation-policies envelope.
type policyListWire struct {
	DelegationPolicies []policyWire `json:"delegationPolicies"`
}

type policyWire struct {
	Name     string     `json:"name"`
	TenantID string     `json:"tenantId"`
	Rules    []ruleWire `json:"rules"`
}

type ruleWire struct {
	Target targetWire `json:"target"`
	Allow  bool       `json:"allow"`
}

type targetWire struct {
	MatchLabels map[string]string `json:"matchLabels"`
	IDs         []string          `json:"ids"`
	Types       []string          `json:"types"`
}

// poolListWire is the GET /v1/admin/pools envelope (only the fields the
// audit needs).
type poolListWire struct {
	Pools []poolWire `json:"pools"`
}

type poolWire struct {
	Name             string `json:"name"`
	RuntimeRef       string `json:"runtimeRef"`
	IsolationProfile string `json:"isolationProfile"`
}

// runtimeListWire is the GET /v1/admin/runtimes envelope. The audit
// resolves each pool's runtimeRef to the runtime's type and labels —
// the §8.3 match candidate a DelegationPolicy rule evaluates — because
// the rule matches the runtime a delegation targets, and the pool
// supplies that runtime's isolation profile.
type runtimeListWire struct {
	Runtimes []runtimeWire `json:"runtimes"`
}

type runtimeWire struct {
	Name   string            `json:"name"`
	Type   string            `json:"type"`
	Labels map[string]string `json:"labels"`
}

// isolationViolationOut is the §24.14 report row: the offending policy
// rule and the source/target pools with their isolation profiles.
type isolationViolationOut struct {
	Policy        string `json:"policy"`
	TenantID      string `json:"tenantId,omitempty"`
	RuleIndex     int    `json:"ruleIndex"`
	SourcePool    string `json:"sourcePool"`
	SourceProfile string `json:"sourceProfile"`
	TargetPool    string `json:"targetPool"`
	TargetProfile string `json:"targetProfile"`
}

// cmdPolicyAuditIsolation fetches the DelegationPolicy, pool, and
// runtime inventories, performs the §24.14 client-side join, and prints
// the rule × pool monotonicity violations as JSON. The command is
// read-only: a non-empty report exits 0 because the violations are the
// deliverable, not a failure of the audit itself.
//
// spec: §24.14 line 172; §8.3 lines 346-352.
func cmdPolicyAuditIsolation(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "lenny-ctl: policy audit-isolation takes no arguments (got %q)\n", args[0])
		return 2
	}

	var policies policyListWire
	if err := c.Do(ctx, "GET", "/v1/admin/delegation-policies", nil, &policies); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	var pools poolListWire
	if err := c.Do(ctx, "GET", "/v1/admin/pools", nil, &pools); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	var runtimes runtimeListWire
	if err := c.Do(ctx, "GET", "/v1/admin/runtimes", nil, &runtimes); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	violations := auditIsolation(policies.DelegationPolicies, pools.Pools, runtimes.Runtimes)

	out := make([]isolationViolationOut, 0, len(violations))
	for _, v := range violations {
		out = append(out, isolationViolationOut{
			Policy:        v.Policy,
			TenantID:      v.TenantID,
			RuleIndex:     v.RuleIndex,
			SourcePool:    v.SourcePool,
			SourceProfile: v.SourceProfile,
			TargetPool:    v.TargetPool,
			TargetProfile: v.TargetProfile,
		})
	}
	printJSON(stdout, map[string]any{
		"violations":     out,
		"violationCount": len(out),
	})
	return 0
}

// auditIsolation builds the §24.14 join inputs from the three admin
// inventories and runs the monotonicity audit. Each pool becomes a
// PoolCandidate whose match candidate is the runtime it runs (resolved
// by runtimeRef → runtime type/labels); a pool whose runtimeRef names
// no known runtime still matches id-only and empty-target rules through
// its runtimeRef as the candidate id.
func auditIsolation(policies []policyWire, pools []poolWire, runtimes []runtimeWire) []delegationpolicystore.IsolationViolation {
	byName := make(map[string]runtimeWire, len(runtimes))
	for _, rt := range runtimes {
		byName[rt.Name] = rt
	}

	candidates := make([]delegationpolicystore.PoolCandidate, 0, len(pools))
	for _, p := range pools {
		rt := byName[p.RuntimeRef]
		candidates = append(candidates, delegationpolicystore.PoolCandidate{
			PoolName:         p.Name,
			IsolationProfile: p.IsolationProfile,
			IsolationRank:    isolation.Rank(isolation.Profile(p.IsolationProfile)),
			Candidate: delegationpolicystore.Candidate{
				ID:     p.RuntimeRef,
				Type:   rt.Type,
				Labels: rt.Labels,
			},
		})
	}

	storePolicies := make([]delegationpolicystore.DelegationPolicy, 0, len(policies))
	for _, p := range policies {
		dp := delegationpolicystore.DelegationPolicy{Name: p.Name, TenantID: p.TenantID}
		for _, r := range p.Rules {
			dp.Rules = append(dp.Rules, delegationpolicystore.Rule{
				Target: delegationpolicystore.Target{
					MatchLabels: r.Target.MatchLabels,
					IDs:         r.Target.IDs,
					Types:       r.Target.Types,
				},
				Allow: r.Allow,
			})
		}
		storePolicies = append(storePolicies, dp)
	}

	return delegationpolicystore.AuditIsolation(storePolicies, candidates)
}
