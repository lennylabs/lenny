// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	tenantpg "github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	transcriptpg "github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	userpg "github.com/lennylabs/lenny/pkg/gateway/environment/userstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
	erasurepg "github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob/pgstore"
	"github.com/lennylabs/lenny/pkg/kms"
)

// Transcripts is the part of the transcript store a World uses: seeding a
// source session's transcript, reading a session's entries, and the
// tenant-deletion Phase 4 transcripts entry.
type Transcripts interface {
	Append(ctx context.Context, tenantID, sessionID string, entries ...transcriptstore.Entry) (uint64, error)
	Get(ctx context.Context, tenantID, sessionID string) ([]transcriptstore.Entry, error)
	DeleteByTenant(ctx context.Context, tenantID string) (int, error)
}

// Stores is one posture's production stores, wired as cmd/lenny-gateway
// wires them: the session store's creation fence reads the tenant and user
// rows, and its replay copy writes the transcript store.
type Stores struct {
	Name        string
	Sessions    sessionstore.Store
	Tenants     tenantstore.Store
	Users       userstore.Store
	Jobs        erasurejob.Store
	Transcripts Transcripts
	// TranscriptEraser is the session-scoped transcripts eraser. It is nil
	// in the Postgres posture, where session_messages cascades from the
	// session row.
	TranscriptEraser *erasure.SessionEraser

	// startFault fails the restriction write inside Jobs.Start for the
	// job IDs an Erasure arms.
	startFault startFault
	seedTenant func(ctx context.Context, id string) error
}

// MemoryStores builds the in-memory posture.
func MemoryStores() *Stores {
	return memoryStores(func(read sessionstore.MarkerReader) sessionstore.MarkerReader { return read })
}

// memoryStores builds the in-memory posture with the session store's fence
// reading markers through wrap, so a harness self-test can stand in a fence
// that admits what the production fence refuses.
func memoryStores(wrap func(sessionstore.MarkerReader) sessionstore.MarkerReader) *Stores {
	tenants, users, transcripts := tenantstore.NewMemory(), userstore.NewMemory(), transcriptstore.NewMemory()
	restrictions := newMemStartFault(users)
	return &Stores{
		Name: "memory",
		Sessions: memstore.New(
			memstore.WithCreationMarkers(wrap(sessionstore.StoreMarkers(tenants, users))),
			memstore.WithTranscriptCopier(transcripts.CopySession),
		),
		Tenants:          tenants,
		Users:            users,
		Jobs:             erasurejob.NewMemory(erasurejob.WithRestrictionWriter(restrictions)),
		Transcripts:      transcripts,
		TranscriptEraser: &erasure.SessionEraser{Name: "transcripts", DeleteBySession: transcripts.DeleteBySession},
		startFault:       restrictions,
		seedTenant: func(ctx context.Context, id string) error {
			return tenants.Create(ctx, tenantstore.Tenant{ID: id})
		},
	}
}

// PostgresStores builds the Postgres posture over pool, which must carry
// the repository's migrations. The tenant store envelope-encrypts the
// erasure salt under a local random KMS key, as the billing
// pseudonymization requires. It installs the start-fault trigger on users,
// which fires only for job IDs an Erasure arms.
func PostgresStores(ctx context.Context, pool *pgxpool.Pool) (*Stores, error) {
	provider, err := kms.NewLocalRandom()
	if err != nil {
		return nil, fmt.Errorf("local kms provider: %w", err)
	}
	fault, err := installPGStartFault(ctx, pool)
	if err != nil {
		return nil, err
	}
	return &Stores{
		Name:        "postgres",
		Sessions:    sessionpg.New(pool, sessionpg.WithTranscriptCopier(transcriptpg.CopySessionTx)),
		Tenants:     tenantpg.New(pool, tenantpg.WithKMS(provider)),
		Users:       userpg.New(pool),
		Jobs:        erasurepg.New(pool),
		Transcripts: transcriptpg.New(pool),
		startFault:  fault,
		seedTenant: func(ctx context.Context, id string) error {
			_, err := pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)`, id, []byte{0x01})
			return err
		},
	}, nil
}

// transcriptLen returns the number of transcript entries under sessionID,
// treating a missing transcript as zero.
func (s *Stores) transcriptLen(ctx context.Context, tenantID, sessionID string) (int, error) {
	got, err := s.Transcripts.Get(ctx, tenantID, sessionID)
	if errors.Is(err, transcriptstore.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read transcript of %s: %w", sessionID, err)
	}
	return len(got), nil
}
