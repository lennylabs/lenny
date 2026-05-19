// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"fmt"
	"io"
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
	var granted, failed int
	for _, rt := range referenceRuntimes {
		body := map[string]string{"tenantId": defaultTenant}
		err := client.Do(ctx, "POST", "/v1/admin/runtimes/"+rt.Name+"/tenant-access", body, nil)
		if err != nil {
			failed++
			fmt.Fprintf(out, "  runtime %-20s tenant-access grant failed: %v\n", rt.Name, err)
			continue
		}
		granted++
	}
	fmt.Fprintf(out, "  installed %d reference runtimes; granted default-tenant access to %d\n",
		len(referenceRuntimes), granted)
	if failed > 0 {
		return fmt.Errorf("%d reference-runtime access grants failed", failed)
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
			Name:             rt.Name,
			Type:             "agent",
			Image:            rt.Image,
			IntegrationLevel: rt.IntegrationLevel,
			Description:      rt.Description,
		})
	}
	return seed
}
