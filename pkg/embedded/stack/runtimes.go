// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/ctl"
	"github.com/lennylabs/lenny/pkg/embedded/oidc"
)

// defaultTenant is the §17.4 tenant lenny up grants reference-runtime
// access to so the developer can invoke any runtime without further
// configuration.
const defaultTenant = "default"

// installReferenceRuntimes registers the §26 reference-runtime catalog
// against the running gateway and wires the §17.4 Embedded Mode
// defaults: every runtime is a platform-global record, the default
// tenant is created, and the default tenant is granted access to every
// runtime.
//
// The §26 reference runtimes register as platform-global records with no
// warm pool: they are placeholder-pinned (§26.3), so under active §4.7
// placement a session against one does not start until a runnable digest,
// an applied Runtime CRD, and a warm pool exist for it. In addition to
// those records, the bootstrap seeds the §15.4.4 echo conformance
// exemplar with a runnable image, an applied Runtime CRD
// (deploymentModel: embedded), and a single-pod warm pool (warmCount: 1,
// the §5.2 hot-pool taxonomy), so a credential-free session runs on an
// in-cluster pod. The WarmPoolController pre-warms one echo pod at initial
// fill; the first session claims it once it is idle.
//
// The handler authenticates with the §17.4 dev-header path: the
// gateway runs in dev mode, where the X-Lenny-Roles dev header admits
// a self-claimed platform-admin.
//
// spec: §17.4 (Embedded Mode seed), §5.2 (warm pool), §15.4.4 (echo
// conformance exemplar), §26.1 (auto-grant).
func installReferenceRuntimes(ctx context.Context, gatewayURL string, out io.Writer) error {
	client := ctl.New(ctl.Options{
		BaseURL:   gatewayURL,
		DevTenant: defaultTenant,
		DevRoles:  "platform-admin",
		Timeout:   30 * time.Second,
	})

	// The bootstrap seed creates the default tenant and registers
	// every reference runtime as a platform-global record in one
	// idempotent call.
	seed := buildBootstrapSeed()
	var bootstrapResp map[string]any
	if err := client.Do(ctx, "POST", "/v1/admin/bootstrap", seed, &bootstrapResp); err != nil {
		return fmt.Errorf("register reference runtimes: %w", err)
	}

	// Grant the default tenant access to each reference runtime and the
	// seeded echo runtime. §26.1: lenny up auto-grants the default tenant
	// access to every reference runtime it installs. The grant endpoint is
	// idempotent.
	//
	// echo is not a §26 reference runtime, so it is appended to the grant
	// set explicitly: without the grant the default tenant holds no access
	// for echo and tenantReachesRuntime denies `lenny session new --runtime
	// echo` for the default tenant (the tier-4 smoke runs against the
	// default tenant). spec: §26.1 (auto-grant), §15.4.4 (echo exemplar).
	//
	// Each failure is collected with the runtime name and the underlying
	// error so the returned error names the failing runtimes — an operator
	// hitting this path has no §24.3 CLI retry loop, so the structured
	// value, not just the stdout log, must carry enough detail to act on.
	// F-24.3.4.
	granted, failures := grantDefaultTenantAccess(ctx, client, out, grantedRuntimeNames())
	fmt.Fprintf(out, "  installed %d reference runtimes plus the echo runtime; granted default-tenant access to %d\n",
		len(referenceRuntimes), granted)
	// spec: §26.1 line 5 / §26.3 lines 215-223 — the reference-runtime
	// images are published by their own first-party CI, so the digests are
	// not known at lenny build time and the catalog ships placeholder-pinned.
	// A placeholder-pinned image registers fine but fails to pull on the
	// first session start. Surface this loudly so an operator following the
	// "day-one utility" promise is not surprised by an ImagePullBackOff.
	// F-26.3.6.
	if pinned := placeholderPinnedRuntimes(); len(pinned) > 0 {
		fmt.Fprintf(out, "  [WARN] %d reference runtime(s) are pinned to a placeholder image digest and cannot start a session until re-pinned: %s\n",
			len(pinned), strings.Join(pinned, ", "))
		fmt.Fprintf(out, "         Re-register each runtime with its published digest, or import a local image with `lenny image import <name>`, before invoking it.\n")
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d reference-runtime access grant(s) failed: %w",
			len(failures), errors.Join(failures...))
	}
	return nil
}

// grantedRuntimeNames returns the runtime names lenny up auto-grants the
// default tenant access to: the §26 reference runtimes in catalog order
// followed by the §15.4.4 echo runtime. echo is appended explicitly
// because it is not a §26 reference runtime and so is absent from the
// referenceRuntimes slice. spec: §26.1 (auto-grant), §15.4.4 (echo exemplar).
func grantedRuntimeNames() []string {
	names := make([]string, 0, len(referenceRuntimes)+1)
	for _, rt := range referenceRuntimes {
		names = append(names, rt.Name)
	}
	names = append(names, EchoRuntimeName)
	return names
}

// grantDefaultTenantAccess grants the default tenant access to each named
// runtime through the idempotent §26.1 tenant-access endpoint. It returns
// the count granted and one collected error per failure naming the runtime
// (F-24.3.4). spec: §26.1 (auto-grant default-tenant access).
func grantDefaultTenantAccess(ctx context.Context, client *ctl.Client, out io.Writer, names []string) (int, []error) {
	var granted int
	var failures []error
	for _, name := range names {
		body := map[string]string{"tenantId": defaultTenant}
		if err := client.Do(ctx, "POST", "/v1/admin/runtimes/"+name+"/tenant-access", body, nil); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", name, err))
			fmt.Fprintf(out, "  runtime %-20s tenant-access grant failed: %v\n", name, err)
			continue
		}
		granted++
	}
	return granted, failures
}

// bootstrapSeed is the §24.1 bootstrap seed payload. It mirrors the
// gateway's admin.BootstrapRequest fields the Embedded Mode install
// uses, declared locally so the stack package does not depend on the
// gateway's admin package.
type bootstrapSeed struct {
	Tenants  []seedTenant  `json:"tenants,omitempty"`
	Runtimes []seedRuntime `json:"runtimes,omitempty"`
	Users    []seedUser    `json:"users,omitempty"`
	// Pools mirrors admin.BootstrapRequest.Pools so the §17.4 Embedded
	// Mode seed creates the echo warm pool in the same idempotent
	// bootstrap call that registers the runtime records. The PoolScaling
	// controller (activated by the embedded --agent-namespaces thread)
	// materializes each seeded poolstore row into a SandboxWarmPool CRD
	// per §4.6.2. spec: §5.2 (warm pool), §4.6.2 (registry-to-CRD).
	Pools []seedPool `json:"pools,omitempty"`
}

// seedPool mirrors the gateway's admin.PoolPayload fields the §17.4
// Embedded Mode echo warm pool seed sets, declared locally so the stack
// package does not depend on the gateway admin package (matching the
// local seedRuntime declaration). The JSON tags mirror
// pkg/gateway/admin.PoolPayload.
type seedPool struct {
	Name             string `json:"name"`
	RuntimeRef       string `json:"runtimeRef,omitempty"`
	WarmCount        int    `json:"warmCount,omitempty"`
	ResourceClass    string `json:"resourceClass,omitempty"`
	EgressProfile    string `json:"egressProfile,omitempty"`
	IsolationProfile string `json:"isolationProfile,omitempty"`
	// AllowStandardIsolation is the §5.3 deployer opt-in the gateway pool
	// admission path requires before it admits an explicitly-set
	// `standard` (runc) profile. The embedded single-node cluster degrades
	// `sandboxed`/`microvm`, so the echo pool runs `standard` under the
	// §17.4 local-fidelity disclosure. spec: §17.4, §5.3.
	AllowStandardIsolation bool `json:"allowStandardIsolation,omitempty"`
	// DNSPolicy is the §13.2 per-pool DNS opt-out. `cluster-default`
	// reverts the pool's pods to the Kubernetes default ClusterFirst
	// resolver (kube-system CoreDNS) instead of a dedicated lenny-system
	// CoreDNS instance, and the WarmPoolController stamps the
	// lenny.dev/dns-policy: cluster-default label. The embedded substrate
	// installs CRD definitions only and runs no dedicated lenny-system
	// CoreDNS, so the echo pool opts out, mirroring the working Kind
	// precedent echo-pool-embedded. The opt-out is admitted only for a
	// `standard` (runc) pool (poolstore.ValidateDNSPolicy). spec: §13.2.
	DNSPolicy string `json:"dnsPolicy,omitempty"`
}

type seedTenant struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
}

type seedRuntime struct {
	Name             string `json:"name"`
	Type             string `json:"type,omitempty"`
	Image            string `json:"image,omitempty"`
	IntegrationLevel string `json:"integrationLevel,omitempty"`
	Description      string `json:"description,omitempty"`

	// Labels are the §5.1 line-51 required runtime labels the bootstrap
	// handler rejects a create without. JSON tag mirrors RuntimePayload.
	Labels map[string]string `json:"labels,omitempty"`

	// The §26.1 / §26.2 declarations the bootstrap handler stores on the
	// Runtime record. The JSON tags mirror the gateway admin
	// RuntimePayload so the seed reaches §26 parity with the chart's
	// reference-runtimes.yaml. F-26.2.3 / F-26.1.3.
	AllowedResourceClasses []string                `json:"allowedResourceClasses,omitempty"`
	SupportedProviders     []string                `json:"supportedProviders,omitempty"`
	Capabilities           *runtimeCapabilities    `json:"capabilities,omitempty"`
	CredentialCapabilities *credentialCapabilities `json:"credentialCapabilities,omitempty"`
	Limits                 *runtimeLimits          `json:"limits,omitempty"`
	SetupCommandPolicy     *setupCommandPolicy     `json:"setupCommandPolicy,omitempty"`
	SetupPolicy            *setupPolicy            `json:"setupPolicy,omitempty"`
	DefaultPoolConfig      *defaultPoolConfig      `json:"defaultPoolConfig,omitempty"`
}

type seedUser struct {
	Subject  string   `json:"subject"`
	TenantID string   `json:"tenantId"`
	Email    string   `json:"email,omitempty"`
	Roles    []string `json:"roles,omitempty"`
}

// buildBootstrapSeed assembles the §17.4 Embedded Mode bootstrap seed:
// the default tenant, the built-in user, and the §26 reference-runtime
// catalog as platform-global records.
func buildBootstrapSeed() bootstrapSeed {
	seed := bootstrapSeed{
		Tenants: []seedTenant{
			{ID: defaultTenant, DisplayName: "Default (Embedded Mode)"},
		},
		Users: []seedUser{
			{
				Subject:  oidc.BuiltInUser,
				TenantID: defaultTenant,
				Email:    oidc.BuiltInUser,
				Roles:    []string{"platform-admin"},
			},
		},
	}
	for _, rt := range referenceRuntimes {
		seed.Runtimes = append(seed.Runtimes, seedRuntimeFrom(rt))
	}
	// echo is the §15.4.4 conformance exemplar rather than a §26 reference
	// runtime, so it is seeded explicitly outside the referenceRuntimes
	// loop. It carries the runnable echo-embedded image the bring-up
	// resolves at import time (S5/S6 overwrite echoRuntime.Image before this
	// seed is built), a single-pod warm pool, and credential-free labels.
	// spec: §15.4.4 (echo conformance exemplar), §17.4 (Embedded Mode seed).
	seed.Runtimes = append(seed.Runtimes, seedRuntimeFrom(echoRuntime))
	// The echo warm pool (warmCount: 1, §5.2 hot-pool taxonomy) is the only
	// seeded pool: the §26 reference runtimes register without a pool
	// (placeholder-pinned, no §4.7 pod placement). The pool runs `standard`
	// (runc) under the §17.4 local-fidelity disclosure, with
	// allowStandardIsolation set so the gateway admits the explicit
	// `standard` profile, and dnsPolicy: cluster-default so its pods resolve
	// through the embedded substrate's kube-system CoreDNS rather than a
	// dedicated lenny-system instance the embedded substrate does not run.
	// This mirrors the working Kind precedent echo-pool-embedded.
	// spec: §5.2 (warm pool), §13.2 (per-pool DNS), §17.4 (Embedded Mode seed).
	seed.Pools = append(seed.Pools, seedPool{
		Name:                   "echo-pool-embedded",
		RuntimeRef:             EchoRuntimeName,
		WarmCount:              1,
		ResourceClass:          "small",
		EgressProfile:          "restricted",
		IsolationProfile:       "standard",
		AllowStandardIsolation: true,
		DNSPolicy:              "cluster-default",
	})
	return seed
}

// seedRuntimeFrom maps a catalog ReferenceRuntime into the bootstrap
// seedRuntime wire record. It is shared by the §26 reference-runtime
// loop and the explicit echo seed so the two paths stay identical.
func seedRuntimeFrom(rt ReferenceRuntime) seedRuntime {
	return seedRuntime{
		Name:                   rt.Name,
		Type:                   "agent",
		Image:                  rt.Image,
		IntegrationLevel:       rt.IntegrationLevel,
		Description:            rt.Description,
		Labels:                 rt.Labels,
		AllowedResourceClasses: rt.AllowedResourceClasses,
		SupportedProviders:     rt.SupportedProviders,
		Capabilities:           rt.Capabilities,
		CredentialCapabilities: rt.CredentialCapabilities,
		Limits:                 rt.Limits,
		SetupCommandPolicy:     rt.SetupCommandPolicy,
		SetupPolicy:            rt.SetupPolicy,
		DefaultPoolConfig:      rt.DefaultPoolConfig,
	}
}
