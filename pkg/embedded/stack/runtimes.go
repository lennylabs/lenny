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
// §17.4 also overrides the warm-pool default to warmCount: 0. The
// bootstrap registers runtime records only; it creates no warm pool,
// so every runtime cold-starts on first use, which is the warmCount: 0
// behavior. The reference-runtime images are pulled lazily on the
// first session start for each runtime.
//
// The handler authenticates with the §17.4 dev-header path: the
// gateway runs in dev mode, where the X-Lenny-Roles dev header admits
// a self-claimed platform-admin.
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

	// Grant the default tenant access to each reference runtime.
	// §26.1: lenny up auto-grants the default tenant access to every
	// reference runtime it installs. The grant endpoint is idempotent.
	//
	// Each failure is collected with the runtime name and the
	// underlying error so the returned error names the failing
	// runtimes — an operator hitting this path has no §24.3 CLI retry
	// loop, so the structured value, not just the stdout log, must
	// carry enough detail to act on. F-24.3.4.
	var granted int
	var failures []error
	for _, rt := range referenceRuntimes {
		body := map[string]string{"tenantId": defaultTenant}
		err := client.Do(ctx, "POST", "/v1/admin/runtimes/"+rt.Name+"/tenant-access", body, nil)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", rt.Name, err))
			fmt.Fprintf(out, "  runtime %-20s tenant-access grant failed: %v\n", rt.Name, err)
			continue
		}
		granted++
	}
	fmt.Fprintf(out, "  installed %d reference runtimes; granted default-tenant access to %d\n",
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

// bootstrapSeed is the §24.1 bootstrap seed payload. It mirrors the
// gateway's admin.BootstrapRequest fields the Embedded Mode install
// uses, declared locally so the stack package does not depend on the
// gateway's admin package.
type bootstrapSeed struct {
	Tenants  []seedTenant  `json:"tenants,omitempty"`
	Runtimes []seedRuntime `json:"runtimes,omitempty"`
	Users    []seedUser    `json:"users,omitempty"`
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
		seed.Runtimes = append(seed.Runtimes, seedRuntime{
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
		})
	}
	return seed
}
