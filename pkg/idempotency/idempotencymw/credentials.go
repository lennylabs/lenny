// SPDX-License-Identifier: MIT

package idempotencymw

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/idempotency"
)

// CredentialRedactor removes credentials from a response body before an
// idempotency store holds it, and restores them when the stored response
// is read back for a replay.
type CredentialRedactor interface {
	// Strip returns body with every credential replaced by a reference
	// from which the credential cannot be recovered.
	Strip(body []byte) []byte
	// Remint returns the stored body with each reference it can account
	// for replaced by the credential.
	Remint(stored []byte) []byte
}

// WithCredentials returns a Store that holds no credential: Put stores the
// record's response body through redactor.Strip, and Get and Claim return
// a stored record's response body through redactor.Remint. The redaction
// sits at the store boundary, so the REST middleware and the MCP
// idempotency hook, which share one Store, both store and replay through
// it. A nil redactor returns store unchanged.
//
// spec: §11.5 (Idempotency)
func WithCredentials(store Store, redactor CredentialRedactor) Store {
	if redactor == nil {
		return store
	}
	return &credentialStore{inner: store, redactor: redactor}
}

// credentialStore is the Store WithCredentials returns.
type credentialStore struct {
	inner    Store
	redactor CredentialRedactor
}

func (c *credentialStore) Get(ctx context.Context, key idempotency.Key) (idempotency.Record, bool, error) {
	rec, found, err := c.inner.Get(ctx, key)
	if err != nil || !found {
		return rec, found, err
	}
	return c.remint(rec), true, nil
}

func (c *credentialStore) Put(ctx context.Context, rec idempotency.Record) error {
	if len(rec.Response.Body) > 0 {
		rec.Response.Body = c.redactor.Strip(rec.Response.Body)
	}
	return c.inner.Put(ctx, rec)
}

func (c *credentialStore) Claim(ctx context.Context, key idempotency.Key, bodyHash string, now time.Time) (idempotency.Record, bool, error) {
	existing, claimed, err := c.inner.Claim(ctx, key, bodyHash, now)
	if err != nil || claimed {
		return existing, claimed, err
	}
	return c.remint(existing), false, nil
}

func (c *credentialStore) Release(ctx context.Context, key idempotency.Key) error {
	return c.inner.Release(ctx, key)
}

// remint returns rec with the credentials of its stored body restored.
func (c *credentialStore) remint(rec idempotency.Record) idempotency.Record {
	if len(rec.Response.Body) > 0 {
		rec.Response.Body = c.redactor.Remint(rec.Response.Body)
	}
	return rec
}
