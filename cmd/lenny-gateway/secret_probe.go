// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/provisioning/vcscred"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
)

// tokenServiceSecretProber is the gateway side of the §4.9 admin-time
// Secret probe and of the gateway's VCS token read (Resolve). It calls the Token Service ProbeSecretAccess RPC over the
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
// Every RPC runs under timeout through secretAccess. The deadline covers a
// Token Service that accepted the call and never answers, which the Token
// Service's own Secret read deadline cannot, because a frozen process does
// not enforce it. A hang therefore ends as a gRPC DeadlineExceeded, the same
// no-verdict error as any other failed call.
//
// spec: §4.9 (Credential Storage Topology).
type tokenServiceSecretProber struct {
	stub tokensv1.TokenServiceClient
	// credNS is the resolved --credentials-namespace value.
	credNS string
	// tokenService is the §4.3 token_service subsystem that gates the VCS
	// keyed read (Resolve). Nil leaves the read ungated. The admin probe
	// does not use it.
	tokenService *subsystem.Subsystem
	// timeout bounds each ProbeSecretAccess call. It is fixed at
	// credassign.DefaultClientTimeout, the bound on every other gateway call
	// on this link, and tests shorten it.
	timeout time.Duration
}

// newTokenServiceSecretProber returns a prober over stub that parses each
// stored ref against credNS. The composition root passes the value
// gatewayFlags.resolvedCredentialsNamespace returns, the same value it
// passes to the admin Router, so the ref the Router canonicalizes is the
// ref the prober accepts.
//
// spec: §4.9 (Credential Storage Topology).
func newTokenServiceSecretProber(stub tokensv1.TokenServiceClient, credNS string) *tokenServiceSecretProber {
	return &tokenServiceSecretProber{stub: stub, credNS: credNS, timeout: credassign.DefaultClientTimeout}
}

// newTokenServiceVCSReader returns the gateway VCS SecretReader over stub:
// the same adapter as the admin prober, with the keyed read gated by the
// token_service subsystem the §4.3 credential-assignment client shares.
//
// spec: §4.9 (Token Service secret resolution), §14.
func newTokenServiceVCSReader(stub tokensv1.TokenServiceClient, credNS string, tokenService *subsystem.Subsystem) *tokenServiceSecretProber {
	return &tokenServiceSecretProber{stub: stub, credNS: credNS, tokenService: tokenService, timeout: credassign.DefaultClientTimeout}
}

// vcsTokenKey is the Secret key the github provider's token is stored
// under (§4.9 Secret shape).
const vcsTokenKey = "token"

var (
	_ admin.SecretAccessProber = (*tokenServiceSecretProber)(nil)
	_ vcscred.SecretReader     = (*tokenServiceSecretProber)(nil)
)

// secretAccess issues one ProbeSecretAccess call under the prober's deadline.
// The admin probe runs inside the pool row's transaction and a VCS read gates
// session creation, so a call with no deadline would hold the row lock, or
// leave the token_service breaker unfed, for as long as the Token Service
// stays silent.
//
// spec: §4.9 (Credential Storage Topology, Token Service secret resolution).
func (p *tokenServiceSecretProber) secretAccess(ctx context.Context, req *tokensv1.ProbeSecretAccessRequest) (*tokensv1.ProbeSecretAccessResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	return p.stub.ProbeSecretAccess(ctx, req)
}

// Resolve is the gateway's VCS token read. It parses the stored ref with
// credential.ParseSecretRef against the credentials namespace and asks the
// Token Service for the Secret's `token` key with one ProbeSecretAccess
// call, which the Token Service answers from one uncached get.
//
// ALLOWED returns the value. NOT_FOUND, DENIED, and a ref the parser
// refuses wrap vcscred.ErrNoUsableCredential, the parse refusal with no RPC
// made. A gRPC error or an open breaker is returned wrapped, with no retry
// and no earlier value served, so the read fails closed and the caller
// surfaces the TRANSIENT category.
//
// The RPC alone runs inside Do on the token_service subsystem, so an open
// breaker refuses with no RPC made. The closure returns only the gRPC
// error, so a definitive verdict, NOT_FOUND and DENIED included, counts as
// a breaker success rather than a failure. A silent Token Service ends at the
// call deadline, so the hang reaches the caller as the TRANSIENT category and
// counts as a breaker failure.
//
// spec: §4.9 (Token Service secret resolution), §4.3, §14.
func (p *tokenServiceSecretProber) Resolve(ctx context.Context, ref string) (string, error) {
	name, err := credential.ParseSecretRef(ref, p.credNS)
	if err != nil {
		return "", fmt.Errorf("%w: secretRef refused: %w", vcscred.ErrNoUsableCredential, err)
	}
	var resp *tokensv1.ProbeSecretAccessResponse
	call := func(ctx context.Context) error {
		r, err := p.secretAccess(ctx, &tokensv1.ProbeSecretAccessRequest{SecretName: name, Key: vcsTokenKey})
		resp = r
		return err
	}
	if p.tokenService != nil {
		err = p.tokenService.Do(ctx, call)
	} else {
		err = call(ctx)
	}
	if err != nil {
		return "", fmt.Errorf("read VCS token Secret %q through the Token Service: %w", name, err)
	}
	return vcsTokenFromVerdict(resp, name)
}

// vcsTokenFromVerdict maps a keyed ProbeSecretAccess response onto the
// SecretReader result. An unspecified verdict is not a definitive answer,
// so it fails the read as indeterminate rather than as a missing
// credential.
func vcsTokenFromVerdict(resp *tokensv1.ProbeSecretAccessResponse, name string) (string, error) {
	switch resp.GetVerdict() {
	case tokensv1.Verdict_VERDICT_ALLOWED:
		return string(resp.GetValue()), nil
	case tokensv1.Verdict_VERDICT_NOT_FOUND:
		return "", fmt.Errorf("%w: Secret %q or its %s key not found", vcscred.ErrNoUsableCredential, name, vcsTokenKey)
	case tokensv1.Verdict_VERDICT_DENIED:
		return "", fmt.Errorf("%w: Token Service read of Secret %q denied", vcscred.ErrNoUsableCredential, name)
	default:
		return "", fmt.Errorf("the Token Service returned unspecified secret-access verdict for Secret %q", name)
	}
}

func (p *tokenServiceSecretProber) ProbeSecretAccess(ctx context.Context, secretRef string) (admin.SecretProbeVerdict, error) {
	name, err := credential.ParseSecretRef(secretRef, p.credNS)
	if err != nil {
		return 0, fmt.Errorf("probe secretRef: %w", err)
	}
	resp, err := p.secretAccess(ctx, &tokensv1.ProbeSecretAccessRequest{SecretName: name})
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
