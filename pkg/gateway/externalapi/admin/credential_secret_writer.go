// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
)

// This file holds the Lenny-written credential Secret path of the admin
// credential-pool writes: the secretData request check, the Secret
// operations each write performs, and the refusals a missing writer or a
// failed operation produces. The handlers run the operations inside the
// credentialPools.Update mutate closure, after every check of the request
// has passed and after an explicit credentialpoolstore.Validate, as the
// last step before the row commits.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API.

// CredentialSecretWriter writes and deletes Lenny-written credential
// Secrets in the credentials namespace. It never reads one: the gateway
// holds create, update, delete, and deletecollection there, and no get,
// list, watch, or patch.
//
// spec: §4.9 (Credential Storage Topology) — Credentials namespace rule 4.
type CredentialSecretWriter interface {
	// Put creates the Secret name with data, or replaces its data, labels,
	// and annotations when it exists.
	Put(ctx context.Context, name string, meta SecretOwner, data map[string]string) error
	// Delete deletes the Secret name. A Secret that is absent returns nil.
	Delete(ctx context.Context, name string) error
	// DeleteTenant deletes every Secret whose lenny.dev/tenant-hash label
	// matches tenant, in one delete-collection.
	DeleteTenant(ctx context.Context, tenant string) error
}

// SecretOwner is the owner a Lenny-written Secret records in its
// lenny.dev/tenant-id, lenny.dev/credential-pool, and
// lenny.dev/credential-id annotations, and, hashed, in its
// lenny.dev/tenant-hash label.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 2.
type SecretOwner struct {
	TenantID     string
	Pool         string
	CredentialID string
}

// unavailableCredentialSecretWriter is the writer of a gateway that has a
// Token Service but no cluster client. Every operation fails, so a write
// that needs a Secret operation takes the rule 5 503 and tenant deletion
// Phase 4 fails and retries. A nil writer would instead report that no Token
// Service is configured, which rule 6 reserves for exactly that condition.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rules 5 and 6.
type unavailableCredentialSecretWriter struct {
	cause error
}

// NewUnavailableCredentialSecretWriter returns a CredentialSecretWriter
// whose every operation fails with an error wrapping cause.
func NewUnavailableCredentialSecretWriter(cause error) CredentialSecretWriter {
	return &unavailableCredentialSecretWriter{cause: cause}
}

// Put implements CredentialSecretWriter.
func (w *unavailableCredentialSecretWriter) Put(context.Context, string, SecretOwner, map[string]string) error {
	return w.err()
}

// Delete implements CredentialSecretWriter.
func (w *unavailableCredentialSecretWriter) Delete(context.Context, string) error {
	return w.err()
}

// DeleteTenant implements CredentialSecretWriter.
func (w *unavailableCredentialSecretWriter) DeleteTenant(context.Context, string) error {
	return w.err()
}

func (w *unavailableCredentialSecretWriter) err() error {
	return fmt.Errorf("credential Secret writer unavailable: %w", w.cause)
}

// WithCredentialSecretWriter wires the Lenny-written Secret writer onto the
// Router. Without it, a write that needs a Secret operation is refused with
// 422 TOKEN_SERVICE_NOT_CONFIGURED and changes nothing.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 6.
func (r *Router) WithCredentialSecretWriter(w CredentialSecretWriter) *Router {
	r.secretWriter = w
	return r
}

// errNoCredentialSecretWriter is the rule 6 refusal: the write needs a
// Secret operation and no Token Service, so no writer, is configured.
var errNoCredentialSecretWriter = &adminWriteError{
	status:  http.StatusUnprocessableEntity,
	code:    "TOKEN_SERVICE_NOT_CONFIGURED",
	message: "this credential-pool write needs a credential Secret operation and no Token Service is configured",
}

// errCredentialSecretOp marks a failed Put or Delete. The refusal the
// handler writes for it is 503 SERVICE_UNAVAILABLE.
var errCredentialSecretOp = errors.New("credential Secret operation failed")

// credentialSecretOpError returns the rule 5 refusal for a failed writer
// call. The message is fixed so no writer error text, which could name a
// value, reaches the response.
func credentialSecretOpError(cause error) error {
	return &adminWriteError{
		status:  http.StatusServiceUnavailable,
		code:    "SERVICE_UNAVAILABLE",
		message: "the credential Secret operation failed; the write was not applied",
		cause:   fmt.Errorf("%w: %w", errCredentialSecretOp, cause),
	}
}

// writeAfterSecretOp writes the 503 SERVICE_UNAVAILABLE refusal for a
// write whose closure reached the writer and reports whether it did. Once
// a writer call was made, a later failure of the row update or the commit
// cannot be told from a lost Secret operation, so every such failure is
// 503 rather than the validation 400 the default mapping gives.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 5.
func writeAfterSecretOp(w http.ResponseWriter, err error, ops *secretOps) bool {
	if ops == nil || !ops.called {
		return false
	}
	if writeAdminWriteError(w, err) {
		return true
	}
	writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE",
		"the credential-pool write did not commit after its credential Secret operation", nil)
	return true
}

// secretPut is the rule 3 Put of one entry's Lenny-written Secret.
type secretPut struct {
	name  string
	owner SecretOwner
	data  map[string]string
}

// secretOps is the set of Secret operations one write performs, and
// whether the closure has called the writer.
type secretOps struct {
	put     *secretPut
	deletes []string
	called  bool
}

func (o *secretOps) empty() bool { return o.put == nil && len(o.deletes) == 0 }

// reset clears the operations a previous closure run planned, so a store
// that re-runs the closure plans afresh against the row it holds.
func (o *secretOps) reset() {
	o.put = nil
	o.deletes = nil
}

// checkWriterFor returns the rule 6 refusal when ops needs a Secret
// operation and no writer is wired. A write with no operation passes.
func (r *Router) checkWriterFor(ops *secretOps) error {
	if ops.empty() || r.secretWriter != nil {
		return nil
	}
	return errNoCredentialSecretWriter
}

// applySecretOps runs the planned Put and Deletes through the writer. It
// sets ops.called before the first call, and wraps a writer failure in the
// rule 5 refusal. No compensating operation follows a failure.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rules 3, 4, and 5.
func (r *Router) applySecretOps(ctx context.Context, ops *secretOps) error {
	if err := r.checkWriterFor(ops); err != nil {
		return err
	}
	if ops.empty() {
		return nil
	}
	ops.called = true
	if ops.put != nil {
		if err := r.secretWriter.Put(ctx, ops.put.name, ops.put.owner, ops.put.data); err != nil {
			return credentialSecretOpError(err)
		}
	}
	for _, name := range ops.deletes {
		if err := r.secretWriter.Delete(ctx, name); err != nil {
			return credentialSecretOpError(err)
		}
	}
	return nil
}

// managedRefs returns the canonical refs in creds whose Secret name is
// Lenny-written, in credential order.
func (r *Router) managedRefs(creds []credentialpoolstore.Credential) []string {
	var out []string
	for _, c := range creds {
		if c.SecretRef == "" {
			continue
		}
		name, err := credential.ParseSecretRef(c.SecretRef, r.credNS)
		if err != nil || !credential.IsManaged(name) {
			continue
		}
		out = append(out, c.SecretRef)
	}
	return out
}

// droppedSecretNames returns the Secret names of the refs in before that
// no credential in after carries: the rule 3 dropped references.
func droppedSecretNames(before []string, after []credentialpoolstore.Credential) []string {
	kept := make(map[string]struct{}, len(after))
	for _, c := range after {
		kept[c.SecretRef] = struct{}{}
	}
	var out []string
	for _, ref := range before {
		if _, ok := kept[ref]; !ok {
			out = append(out, secretNameOf(ref))
		}
	}
	return out
}

// planEntryPut records the rule 3 Put for an entry that carries
// secretData and points the entry at the Lenny-written Secret. An entry
// with no secretData plans nothing.
func (r *Router) planEntryPut(ops *secretOps, c *credentialpoolstore.Credential, tenant, pool string, data map[string]string) {
	if data == nil {
		return
	}
	name := credential.ManagedSecretName(tenant, pool, c.ID)
	c.SecretRef = credential.CanonicalSecretRef(r.credNS, name)
	ops.put = &secretPut{
		name:  name,
		owner: SecretOwner{TenantID: tenant, Pool: pool, CredentialID: c.ID},
		data:  data,
	}
}

// planMutation applies mutate to p against a snapshot of p's Lenny-written
// refs, validates the result, and records each dropped ref's Delete in
// ops. The snapshot is taken before mutate because the pool PUT and the
// seed replace p.Credentials wholesale. Validate runs here, before any
// writer call, because the store validates only after the closure returns
// and a refusal there would roll back a row whose Secret is already gone.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rules 3 and 4.
func (r *Router) planMutation(p *credentialpoolstore.CredentialPool, ops *secretOps, mutate func(*credentialpoolstore.CredentialPool)) error {
	before := r.managedRefs(p.Credentials)
	mutate(p)
	if err := credentialpoolstore.Validate(*p); err != nil {
		return err
	}
	ops.deletes = droppedSecretNames(before, p.Credentials)
	return nil
}

// checkSecretData applies rule 1 to one credential addition or update
// body: at most one of secretRef, secretData, and roleArn, and a
// secretData with at least one key, each key a valid Secret data key, and
// values totalling no more than corev1.MaxSecretSize.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 1.
func checkSecretData(e CredentialEntryRequest) error {
	set := 0
	for _, present := range []bool{e.SecretRef != "", e.SecretData != nil, e.RoleArn != ""} {
		if present {
			set++
		}
	}
	if set > 1 {
		return &adminWriteError{
			status:  http.StatusBadRequest,
			code:    "VALIDATION_ERROR",
			message: "a credential carries at most one of secretRef, secretData, and roleArn",
		}
	}
	if e.SecretData == nil {
		return nil
	}
	return validateSecretData(e.SecretData)
}

// validateSecretData refuses an empty secretData, a key Kubernetes Secret
// validation refuses, and values totalling more than corev1.MaxSecretSize.
// The refusal names the field and never the value.
func validateSecretData(data map[string]string) error {
	if len(data) == 0 {
		return invalidSecretDataError("secretData must carry at least one key")
	}
	total := 0
	for k, v := range data {
		if errs := validation.IsConfigMapKey(k); len(errs) > 0 {
			return invalidSecretDataError("secretData carries a key that is not a valid Secret data key")
		}
		total += len(v)
	}
	if total > corev1.MaxSecretSize {
		return invalidSecretDataError(fmt.Sprintf("secretData values exceed the %d-byte Secret size limit", corev1.MaxSecretSize))
	}
	return nil
}

func invalidSecretDataError(message string) error {
	return &adminWriteError{
		status:  http.StatusBadRequest,
		code:    "VALIDATION_ERROR",
		message: message,
		details: map[string]any{"field": "secretData"},
	}
}

// errSecretDataOnPool is the rule 1 refusal of secretData on pool creation,
// pool update, and the bootstrap seed.
var errSecretDataOnPool = &adminWriteError{
	status:  http.StatusBadRequest,
	code:    "VALIDATION_ERROR",
	message: "secretData is accepted only on credential addition and credential update",
	details: map[string]any{"field": "secretData"},
}
