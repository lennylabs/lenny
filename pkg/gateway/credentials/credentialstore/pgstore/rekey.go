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
// summary. spec: §4.9.1 line 1718.
func (s *Store) RekeyName() string { return "credentials" }

// CountStale runs the §4.9.1 verification query for tenantID: the count
// of credential rows whose secret is still wrapped under a KEK version
// below the current one. The operator disables the old KEK version only
// once this returns 0 for every sealed store. Rows with no secret
// material are excluded; they carry no wrapped DEK to re-key.
//
// spec: §4.9.1 line 1723.
func (s *Store) CountStale(ctx context.Context, tenantID string) (int, error) {
	current, err := s.kms.CurrentKEKVersion(ctx, kekAlias(tenantID))
	if err != nil {
		return 0, fmt.Errorf("credentialstore/pgstore: current KEK version: %w", err)
	}
	var n int
	err = pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM credentials
			 WHERE tenant_id = $1 AND secret_key_version < $2 AND octet_length(secret) > 0`,
			tenantID, current).Scan(&n)
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// RekeyTenant re-wraps every credential row's data-encryption key under
// tenantID's current KEK version, the §4.9.1 re-encryption loop. It
// selects only rows below the current version, so a row already at the
// current version is left untouched and a re-run after a partial failure
// re-keys only the rows still pending (idempotent). Reseal re-wraps the
// DEK without re-encrypting the record, so the GCM nonce and ciphertext
// are unchanged and the plaintext secret is never decrypted; only the
// secret column blob and secret_key_version advance. updated_at is left
// unchanged: re-keying is an at-rest maintenance operation, not a
// credential rotation, so it does not surface on the §15.1 response.
//
// spec: §4.9.1 lines 1718-1721.
func (s *Store) RekeyTenant(ctx context.Context, tenantID string) (int, error) {
	current, err := s.kms.CurrentKEKVersion(ctx, kekAlias(tenantID))
	if err != nil {
		return 0, fmt.Errorf("credentialstore/pgstore: current KEK version: %w", err)
	}
	cipher, err := s.cipher(tenantID)
	if err != nil {
		return 0, err
	}
	rekeyed := 0
	txErr := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		// Read every stale row first, then issue the updates: pgx does
		// not allow a second query on the transaction connection while a
		// rows cursor is open. FOR UPDATE locks the rows for the tx so a
		// concurrent rotation cannot race the re-wrap.
		rows, err := tx.Query(ctx,
			`SELECT ref, secret FROM credentials
			 WHERE tenant_id = $1 AND secret_key_version < $2 AND octet_length(secret) > 0
			 FOR UPDATE`,
			tenantID, current)
		if err != nil {
			return err
		}
		type staleRow struct {
			ref  string
			blob []byte
		}
		var stale []staleRow
		for rows.Next() {
			var r staleRow
			if err := rows.Scan(&r.ref, &r.blob); err != nil {
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
			sealed, err := envelope.Decode(r.blob)
			if err != nil {
				return fmt.Errorf("credentialstore/pgstore: decode sealed secret for %q: %w", r.ref, err)
			}
			resealed, err := cipher.Reseal(ctx, sealed)
			if err != nil {
				return fmt.Errorf("credentialstore/pgstore: reseal %q: %w", r.ref, err)
			}
			newBlob, err := envelope.Encode(resealed)
			if err != nil {
				return fmt.Errorf("credentialstore/pgstore: encode resealed secret for %q: %w", r.ref, err)
			}
			if _, err := tx.Exec(ctx,
				`UPDATE credentials SET secret = $3, secret_key_version = $4
				 WHERE tenant_id = $1 AND ref = $2`,
				tenantID, r.ref, newBlob, resealed.KEKVersion); err != nil {
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
