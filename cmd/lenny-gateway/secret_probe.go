// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
)

// tokenServiceSecretProber is the §4.9 gateway-side RBAC live-probe. It
// calls the Token Service ProbeSecretAccess RPC over the gateway↔Token-
// Service mTLS link and maps the wire verdict onto the admin verdict the
// credential-pool handlers consume. The probe is Token-Service-owned:
// the gateway never reviews the Secret access itself, it asks the Token
// Service whether the Token Service's own ServiceAccount can read it.
//
// A gRPC error (the RPC could not return a definitive verdict — Token
// Service unreachable, mTLS failure, upstream Kubernetes API timeout) is
// propagated so the admin handler maps it to 503
// CREDENTIAL_PROBE_UNAVAILABLE and never fails open.
//
// spec: §4.9 line 1212.
type tokenServiceSecretProber struct {
	stub tokensv1.TokenServiceClient
}

var _ admin.SecretAccessProber = (*tokenServiceSecretProber)(nil)

func (p *tokenServiceSecretProber) ProbeSecretAccess(ctx context.Context, secretRef string) (admin.SecretProbeVerdict, error) {
	resp, err := p.stub.ProbeSecretAccess(ctx, &tokensv1.ProbeSecretAccessRequest{SecretName: secretRef})
	if err != nil {
		return 0, err
	}
	switch resp.GetVerdict() {
	case tokensv1.Verdict_VERDICT_ALLOWED:
		return admin.SecretProbeAllowed, nil
	case tokensv1.Verdict_VERDICT_NOT_FOUND:
		return admin.SecretProbeNotFound, nil
	case tokensv1.Verdict_VERDICT_DENIED:
		return admin.SecretProbeDenied, nil
	default:
		// An unspecified verdict is not a definitive ALLOWED; treat it as
		// an indeterminate probe so the handler rejects the write rather
		// than persisting an unprobed secretRef.
		return 0, fmt.Errorf("Token Service returned unspecified secret-access verdict for %q", secretRef)
	}
}
