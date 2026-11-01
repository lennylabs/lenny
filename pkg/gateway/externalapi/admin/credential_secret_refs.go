// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// This file holds the secretRef checks every admin credential-pool write
// and the bootstrap seed run, in the order §4.9 fixes: the ref parse and
// canonical form (Secret references rules 1 and 2), the If-Match compare
// against the locked row, the platform-admin registration rule
// (Registering a Secret by reference), the reserved lenny-cred- prefix
// (Secret references rule 3), and the admin-time Secret probe. A write
// refused by one check runs no later check. The checks that read the pool
// row run inside the credentialPools.Update mutate closure, against the
// row the store holds locked, because a check against an earlier unlocked
// read races a concurrent write.
//
// spec: §4.9 (Credential Storage Topology), §15.1.

// adminWriteError is a refusal a credential-pool check returns, carrying
// the §15.1 status, code, and details the handler writes. A mutate closure
// returns it so the handler can tell a refusal from a store failure, which
// the default arm of writeCredentialMutateError would otherwise map to a
// 400.
type adminWriteError struct {
	status  int
	code    string
	message string
	details map[string]any
	// cause is the failure behind the refusal, when there is one. It is
	// reachable through errors.Is and errors.As and never written to the
	// response.
	cause error
}

func (e *adminWriteError) Error() string { return e.code + ": " + e.message }

// Unwrap returns the failure behind the refusal, or nil.
func (e *adminWriteError) Unwrap() error { return e.cause }

// writeAdminWriteError writes err when it wraps an *adminWriteError and
// reports whether it did.
func writeAdminWriteError(w http.ResponseWriter, err error) bool {
	var aw *adminWriteError
	if !errors.As(err, &aw) {
		return false
	}
	writeError(w, aw.status, aw.code, aw.message, aw.details)
	return true
}

// invalidSecretRefError is the §4.9 Secret references 400 VALIDATION_ERROR
// for a ref rule 1 or rule 3 refuses.
func invalidSecretRefError(message string) error {
	return &adminWriteError{
		status:  http.StatusBadRequest,
		code:    "VALIDATION_ERROR",
		message: message,
		details: map[string]any{"field": "secretRef"},
	}
}

// errSecretRefRegistrationForbidden is the §4.9 Registering a Secret by
// reference refusal: a caller without platform-admin introduced a ref.
var errSecretRefRegistrationForbidden = &adminWriteError{
	status:  http.StatusForbidden,
	code:    "FORBIDDEN",
	message: "introducing a secretRef into a credential pool requires the platform-admin role",
}

// canonicalizeSecretRefs parses every non-empty secretRef in entries with
// credential.ParseSecretRef and rewrites it in place to the canonical
// "<credentials namespace>/<name>" form the store holds. It returns the
// 400 VALIDATION_ERROR refusal for the first ref the parser refuses.
//
// spec: §4.9 (Credential Storage Topology) — Secret references rules 1
// and 2.
func (r *Router) canonicalizeSecretRefs(entries []CredentialEntryPayload) error {
	for i := range entries {
		if entries[i].SecretRef == "" {
			continue
		}
		name, err := credential.ParseSecretRef(entries[i].SecretRef, r.credNS)
		if err != nil {
			return invalidSecretRefError(err.Error())
		}
		entries[i].SecretRef = credential.CanonicalSecretRef(r.credNS, name)
	}
	return nil
}

// checkSecretRefWrite runs the registration rule, the reserved-prefix
// rule, and the admin-time probe for a write that submits entries against
// the pool credentials have (nil for a pool that does not exist yet). It
// probes only when probe is true; a dryRun request passes false. The
// entries must already be canonical.
//
// spec: §4.9 (Credential Storage Topology) — Registering a Secret by
// reference, Secret references rule 3, and Admin-time Secret probe
// (required), in that order.
func (r *Router) checkSecretRefWrite(ctx context.Context, have []credentialpoolstore.Credential, entries []CredentialEntryPayload, probe bool) error {
	introduced := introducedSecretRefs(have, entries)
	if err := checkSecretRefRegistration(ctx, introduced); err != nil {
		return err
	}
	if err := checkReservedSecretRefs(have, entries); err != nil {
		return err
	}
	if !probe {
		return nil
	}
	return r.probeSecretRefs(ctx, introduced)
}

// introducedSecretRefs returns the distinct non-empty refs in entries that
// no credential in have carries, in submission order. A ref any entry of
// the pool carries is not an introduction, whichever credential id
// carries it.
//
// spec: §4.9 (Credential Storage Topology) — Registering a Secret by
// reference.
func introducedSecretRefs(have []credentialpoolstore.Credential, entries []CredentialEntryPayload) []string {
	seen := make(map[string]struct{}, len(have)+len(entries))
	for _, c := range have {
		if c.SecretRef != "" {
			seen[c.SecretRef] = struct{}{}
		}
	}
	var out []string
	for _, e := range entries {
		if e.SecretRef == "" {
			continue
		}
		if _, ok := seen[e.SecretRef]; ok {
			continue
		}
		seen[e.SecretRef] = struct{}{}
		out = append(out, e.SecretRef)
	}
	return out
}

// checkSecretRefRegistration refuses a non-empty introduced set from a
// caller without the platform-admin role with 403 FORBIDDEN. The check is
// on the role rather than on manage_credential_pools, because a custom
// role may hold that permission. The bootstrap seed does not call it.
//
// spec: §4.9 (Credential Storage Topology) — Registering a Secret by
// reference; §10.2.
func checkSecretRefRegistration(ctx context.Context, introduced []string) error {
	if len(introduced) == 0 {
		return nil
	}
	p, ok := authmw.FromContext(ctx)
	if !ok || !p.HasRole(auth.RolePlatformAdmin) {
		return errSecretRefRegistrationForbidden
	}
	return nil
}

// checkReservedSecretRefs applies the reserved lenny-cred- prefix per
// credential id: a submitted ref whose Secret name carries the prefix is
// accepted only when the credential in have with the same id already
// carries the identical ref. The comparison is per id because the
// pool-wide introduced set misses a Lenny-written ref moved from one entry
// onto another.
//
// spec: §4.9 (Credential Storage Topology) — Secret references rule 3.
func checkReservedSecretRefs(have []credentialpoolstore.Credential, entries []CredentialEntryPayload) error {
	stored := make(map[string]string, len(have))
	for _, c := range have {
		stored[c.ID] = c.SecretRef
	}
	for _, e := range entries {
		if e.SecretRef == "" || !credential.IsManaged(secretNameOf(e.SecretRef)) {
			continue
		}
		if ref, ok := stored[e.ID]; ok && ref == e.SecretRef {
			continue
		}
		return invalidSecretRefError("secretRef names a Secret reserved for credential values the gateway writes")
	}
	return nil
}

// secretNameOf returns the Secret name of a canonical ref, the segment
// after the last slash.
func secretNameOf(ref string) string {
	return ref[strings.LastIndexByte(ref, '/')+1:]
}

// probeSecretRefs runs the §4.9 admin-time Secret probe over refs, the
// canonical refs the write introduces. It returns nil when every ref is
// readable or no prober is wired. On a DENIED or NOT_FOUND verdict it
// returns the 422 CREDENTIAL_SECRET_RBAC_MISSING refusal whose
// details.missingSecrets names every failing ref. On a call that returns
// no verdict it returns the 503 CREDENTIAL_PROBE_UNAVAILABLE refusal
// naming that ref in details.secretRef; the handler MUST NOT persist a
// write whose probe could not be evaluated.
//
// spec: §4.9 (Credential Storage Topology) — Admin-time Secret probe
// (required); §15.1 error catalog.
func (r *Router) probeSecretRefs(ctx context.Context, refs []string) error {
	if r.secretProber == nil {
		return nil
	}
	var missing []string
	for _, ref := range refs {
		verdict, err := r.secretProber.ProbeSecretAccess(ctx, ref)
		if err != nil {
			return &adminWriteError{
				status:  http.StatusServiceUnavailable,
				code:    "CREDENTIAL_PROBE_UNAVAILABLE",
				message: "Token Service secret-access probe could not be evaluated; the write was rejected",
				details: map[string]any{"secretRef": ref},
			}
		}
		if verdict != SecretProbeAllowed {
			missing = append(missing, ref)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &adminWriteError{
		status:  http.StatusUnprocessableEntity,
		code:    "CREDENTIAL_SECRET_RBAC_MISSING",
		message: "the Token Service cannot read every referenced Secret; create each Secret in the credentials namespace and retry",
		details: map[string]any{"missingSecrets": missing},
	}
}
