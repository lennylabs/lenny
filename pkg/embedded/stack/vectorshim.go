// SPDX-License-Identifier: MIT

package stack

import (
	"database/sql"
	"fmt"
)

// installVectorShim installs a pure-SQL stand-in for the parts of the
// pgvector extension the §9.4 schema and the Postgres-backed memory
// store depend on, for use when the embedded PostgreSQL 16 bundle does
// not ship pgvector.
//
// spec: §17.4 (Embedded Mode runs the production schema and the
// production §9.4 Postgres memory store against the embedded Postgres;
// only the storage driver selection differs, with no mode-dependent
// business-logic split). The production pgstore SQL is byte-identical
// across hosts: it always writes `embedding vector(256)`, casts with
// `::vector`/`::text`, and ranks with the `<=>` cosine-distance
// operator. The shim supplies exactly those surfaces so that SQL parses
// and runs unchanged. What the shim cannot supply is the `ivfflat`
// approximate-nearest-neighbour access method (a C-level index method);
// migration 0044 guards its index on that access method, so the
// embedded stack runs without the §9.4 semantic-search index and the
// `<=>` operator returns a constant distance, degrading semantic
// ranking to the recency-ordered substring fallback (§9.4). Every other
// feature is fully migrated.
//
// The shim is installed only when pgvectorAvailable reports the real
// extension is absent, so it never shadows a genuine pgvector
// installation. It is idempotent: a second lenny up against a data
// directory that already carries the shim is a no-op.
func installVectorShim(db *sql.DB) error {
	// Each statement is guarded so a re-run against a data directory that
	// already carries the shim does not error. pg_type / pg_proc /
	// pg_cast / pg_operator existence checks keep the install idempotent
	// without DROP, which would risk cascading into the agent_memory
	// column on a populated database.
	stmts := []string{
		// The shell type, then the I/O functions, then the full type.
		// vector is stored as its text representation; the production
		// pgstore renders and parses the `[v1,v2,...]` literal in Go, so
		// the server only needs to round-trip the text unchanged.
		`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'vector') THEN
				CREATE TYPE vector;
			END IF;
		END $$`,
		`CREATE OR REPLACE FUNCTION vector_shim_in(cstring, oid, integer)
			RETURNS vector LANGUAGE internal IMMUTABLE STRICT AS 'textin'`,
		`CREATE OR REPLACE FUNCTION vector_shim_out(vector)
			RETURNS cstring LANGUAGE internal IMMUTABLE STRICT AS 'textout'`,
		// typmod_in lets `vector(256)` parse; the width is not enforced by
		// the shim because the production HashingEmbedder fixes the width
		// and the column is never queried by dimension.
		`CREATE OR REPLACE FUNCTION vector_shim_typmod_in(cstring[])
			RETURNS integer LANGUAGE sql IMMUTABLE STRICT AS 'SELECT 0'`,
		`DO $$ BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_type WHERE typname = 'vector' AND typinput = 'vector_shim_in'::regproc
			) THEN
				CREATE TYPE vector (
					INPUT = vector_shim_in,
					OUTPUT = vector_shim_out,
					TYPMOD_IN = vector_shim_typmod_in,
					INTERNALLENGTH = VARIABLE,
					STORAGE = extended
				);
			END IF;
		END $$`,
		// The pgstore casts text literals to vector on write and vector to
		// text on read. WITHOUT FUNCTION because the storage is the text
		// representation, so the cast is a no-op relabel.
		`DO $$ BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_cast WHERE castsource = 'text'::regtype AND casttarget = 'vector'::regtype
			) THEN
				CREATE CAST (text AS vector) WITHOUT FUNCTION AS IMPLICIT;
			END IF;
		END $$`,
		`DO $$ BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_cast WHERE castsource = 'vector'::regtype AND casttarget = 'text'::regtype
			) THEN
				CREATE CAST (vector AS text) WITHOUT FUNCTION AS IMPLICIT;
			END IF;
		END $$`,
		// The `<=>` cosine-distance operator the §9.4 ORDER BY uses. With
		// no real vector arithmetic available the shim returns a constant
		// distance, so the ORDER BY collapses to the recency tiebreak —
		// the documented no-semantic-search degradation. The operator must
		// exist so the production query SQL parses and runs.
		`CREATE OR REPLACE FUNCTION vector_shim_cosine_distance(vector, vector)
			RETURNS double precision LANGUAGE sql IMMUTABLE AS 'SELECT 0.0::double precision'`,
		`DO $$ BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_operator
				WHERE oprname = '<=>'
				  AND oprleft = 'vector'::regtype
				  AND oprright = 'vector'::regtype
			) THEN
				CREATE OPERATOR <=> (
					LEFTARG = vector,
					RIGHTARG = vector,
					FUNCTION = vector_shim_cosine_distance
				);
			END IF;
		END $$`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("embedded migrate: install pgvector shim: %w", err)
		}
	}
	return nil
}
