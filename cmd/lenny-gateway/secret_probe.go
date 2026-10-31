// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
)

// tokenServiceSecretProber is the gateway side of the §4.9 admin-time
// Secret probe. It calls the Token Service ProbeSecretAccess RPC over the
// gateway↔Token-Service mTLS link with an unkeyed request and maps the wire
// verdict onto the admin verdict the credential-pool handlers consume.
//
// The admin handlers pass the stored canonical ref. The prober parses it
// with credential.ParseSecretRef against the credentials namespace and
// sends the Secret name alone, because the Token Service reads only its
// configured namespace and the request names no namespace. A ref the
// parser refuses returns an error with no RPC made, which the handler maps
// to 503 CREDENTIAL_PROBE_UNAVAILABLE rather than a silent ALLOWED.
//
// A gRPC error (the RPC could not return a definitive verdict: Token
// Service unreachable, mTLS failure, or a kube-apiserver failure) is
// propagated so the admin handler maps it to 503
// CREDENTIAL_PROBE_UNAVAILABLE and never fails open.
//
// spec: §4.9 (Credential Storage Topology).
type tokenServiceSecretProber struct {
	stub tokensv1.TokenServiceClient
	// credNS is the resolved --credentials-namespace value.
	credNS string
}

// newTokenServiceSecretProber returns a prober over stub that parses each
// stored ref against credNS. The composition root passes the value
// gatewayFlags.resolvedCredentialsNamespace returns, the same value it
// passes to the admin Router, so the ref the Router canonicalizes is the
// ref the prober accepts.
//
// spec: §4.9 (Credential Storage Topology).
func newTokenServiceSecretProber(stub tokensv1.TokenServiceClient, credNS string) *tokenServiceSecretProber {
	return &tokenServiceSecretProber{stub: stub, credNS: credNS}
}

var _ admin.SecretAccessProber = (*tokenServiceSecretProber)(nil)

func (p *tokenServiceSecretProber) ProbeSecretAccess(ctx context.Context, secretRef string) (admin.SecretProbeVerdict, error) {
	name, err := credential.ParseSecretRef(secretRef, p.credNS)
	if err != nil {
		return 0, fmt.Errorf("probe secretRef: %w", err)
	}
	resp, err := p.stub.ProbeSecretAccess(ctx, &tokensv1.ProbeSecretAccessRequest{SecretName: name})
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
