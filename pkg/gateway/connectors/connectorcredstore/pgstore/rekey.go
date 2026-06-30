// SPDX-License-Identifier: MIT

package pgstore

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
	"github.com/lennylabs/lenny/pkg/kms/envelope"
)

// RekeyName identifies this store in the §4.9.1 re-encryption job
// summary. A connector_credentials row holds the OAuth access and
// refresh tokens the §4.9.1 procedure re-encrypts alongside the
// user-supplied API keys. spec: §4.9.1 line 1714.
func (s *Store) RekeyName() string { return "connector_credentials" }

// CountStale runs the §4.9.1 verification query for tenantID: the count
// of connector_credentials rows holding an access or refresh token still
// wrapped under a KEK version below the current one. A row with no
// refresh token (the refresh column is empty) is counted only on its
// access token.
//
// spec: §4.9.1 line 1723.
func (s *Store) CountStale(ctx context.Context, tenantID string) (int, error) {
	current, err := s.kms.CurrentKEKVersion(ctx, kekAlias(tenantID))
	if err != nil {
		return 0, fmt.Errorf("connectorcredstore/pgstore: current KEK version: %w", err)
	}
	var n int
	err = pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM connector_credentials
			 WHERE tenant_id = $1 AND (
			   (octet_length(access_token_blob) > 0 AND access_token_key_version < $2)
			   OR (octet_length(refresh_token_blob) > 0 AND refresh_token_key_version IS NOT NULL AND refresh_token_key_version < $2)
			 )`,
			tenantID, current).Scan(&n)
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// RekeyTenant re-wraps every connector_credentials row's access and
// refresh token DEKs under tenantID's current KEK version, the §4.9.1
// re-encryption loop. It selects only rows with at least one token below
// the current version, re-wraps whichever blobs are stale, and leaves a
// row already at the current version untouched (idempotent). Reseal
// re-wraps the DEK without re-encrypting the token, so the access-token
// hash and updated_at are unchanged; re-keying is at-rest maintenance,
// not a token rotation.
//
// spec: §4.9.1 lines 1718-1721.
func (s *Store) RekeyTenant(ctx context.Context, tenantID string) (int, error) {
	current, err := s.kms.CurrentKEKVersion(ctx, kekAlias(tenantID))
	if err != nil {
		return 0, fmt.Errorf("connectorcredstore/pgstore: current KEK version: %w", err)
	}
	cipher, err := s.cipher(tenantID)
	if err != nil {
		return 0, err
	}
	rekeyed := 0
	txErr := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT connector_id, user_id, environment,
			        access_token_blob, access_token_key_version,
			        refresh_token_blob, refresh_token_key_version
			 FROM connector_credentials
			 WHERE tenant_id = $1 AND (
			   (octet_length(access_token_blob) > 0 AND access_token_key_version < $2)
			   OR (octet_length(refresh_token_blob) > 0 AND refresh_token_key_version IS NOT NULL AND refresh_token_key_version < $2)
			 )
			 FOR UPDATE`,
			tenantID, current)
		if err != nil {
			return err
		}
		type staleRow struct {
			connectorID, userID, environment string
			accBlob                          []byte
			accVer                           int
			refBlob                          []byte
			refVer                           *int
		}
		var stale []staleRow
		for rows.Next() {
			var r staleRow
			if err := rows.Scan(&r.connectorID, &r.userID, &r.environment,
				&r.accBlob, &r.accVer, &r.refBlob, &r.refVer); err != nil {
				rows.Close()
				return err
			}
			stale = append(stale, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, r := range stale {
			accBlob, accVer := r.accBlob, r.accVer
			if len(r.accBlob) > 0 && r.accVer < current {
				blob, ver, err := resealBlob(ctx, cipher, r.accBlob, "access token", r.connectorID)
				if err != nil {
					return err
				}
				accBlob, accVer = blob, ver
			}
			refBlob, refVer := r.refBlob, r.refVer
			if len(r.refBlob) > 0 && r.refVer != nil && *r.refVer < current {
				blob, ver, err := resealBlob(ctx, cipher, r.refBlob, "refresh token", r.connectorID)
				if err != nil {
					return err
				}
				refBlob = blob
				refVer = &ver
			}
			if _, err := tx.Exec(ctx,
				`UPDATE connector_credentials SET
				   access_token_blob = $5, access_token_key_version = $6,
				   refresh_token_blob = $7, refresh_token_key_version = $8
				 WHERE tenant_id = $1 AND connector_id = $2 AND user_id = $3 AND environment = $4`,
				tenantID, r.connectorID, r.userID, r.environment,
				accBlob, accVer, refBlob, refVer); err != nil {
				return err
			}
			rekeyed++
		}
		return nil
	})
	if txErr != nil {
		return 0, txErr
	}
	return rekeyed, nil
}

// resealBlob decodes an envelope blob, re-wraps its DEK under the
// cipher's current KEK version, and re-encodes it, returning the new
// blob and KEK version.
func resealBlob(ctx context.Context, cipher *envelope.Cipher, blob []byte, label, connectorID string) ([]byte, int, error) {
	sealed, err := envelope.Decode(blob)
	if err != nil {
		return nil, 0, fmt.Errorf("connectorcredstore/pgstore: decode %s for %q: %w", label, connectorID, err)
	}
	resealed, err := cipher.Reseal(ctx, sealed)
	if err != nil {
		return nil, 0, fmt.Errorf("connectorcredstore/pgstore: reseal %s for %q: %w", label, connectorID, err)
	}
	out, err := envelope.Encode(resealed)
	if err != nil {
		return nil, 0, fmt.Errorf("connectorcredstore/pgstore: encode resealed %s for %q: %w", label, connectorID, err)
	}
	return out, resealed.KEKVersion, nil
}
