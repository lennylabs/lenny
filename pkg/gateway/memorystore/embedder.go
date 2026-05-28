// SPDX-License-Identifier: MIT

package memorystore

import (
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// EmbeddingDim is the §9.4 embedding vector width. The migration-0044
// agent_memory.embedding column is declared vector(256) to match it,
// and a provider-backed Embedder MUST project its model output to this
// width before returning. Changing this constant requires a new
// migration that re-declares the column dimension.
const EmbeddingDim = 256

// Embedder turns memory text into a fixed-width semantic vector. §9.4
// names Postgres + pgvector as the default memory backend but does not
// mandate a specific embedding source, so the embedding is pluggable:
// the gateway ships HashingEmbedder as the deterministic local default
// and a deployer can supply a provider-backed implementation for
// higher-quality semantic recall.
//
// Embed MUST return a slice of length EmbeddingDim. An implementation
// that cannot produce an embedding (a provider outage, an empty input)
// returns a nil slice and a nil error — the caller then falls back to
// the §9.4 substring match rather than failing the operation.
type Embedder interface {
	Embed(text string) ([]float32, error)
}

// ValidateEmbedder is the startup preflight for a provider-backed
// Embedder. It calls Embed with a known-non-empty preflight token and
// fails when the returned vector's length does not match EmbeddingDim
// — the §9.4 line 198 "MUST project its model output to this width
// before returning" guarantee the pgvector column and the ivfflat
// index rely on. A misconfigured custom Embedder fails fast at boot
// rather than corrupting the §9.4 vector(256) column at every Write
// (where the pgvector dimension-mismatch error arrives unfriendly and
// per-operation).
//
// A nil Embedder is accepted (callers fall back to recency-only
// ranking per memorystore semantics). An Embed call that returns a
// nil slice with a nil error is also accepted: the §9.4 contract
// permits an Embedder to short-circuit a query it cannot serve, and
// the preflight token is reserved for that signal (a provider that
// rejects every input still produces no corruption risk because the
// column write is skipped). A non-nil slice whose length differs from
// EmbeddingDim is the only failure mode this preflight catches —
// every other Embedder defect is the implementation's responsibility.
//
// spec: §9.4 line 198; F-9.4.8.
func ValidateEmbedder(e Embedder) error {
	if e == nil {
		return nil
	}
	vec, err := e.Embed(EmbedderPreflightToken)
	if err != nil {
		return fmt.Errorf("memorystore: embedder preflight failed: %w", err)
	}
	if vec == nil {
		return nil
	}
	if len(vec) != EmbeddingDim {
		return fmt.Errorf("memorystore: embedder returned vector of length %d, want %d (§9.4 line 198 dimension width)",
			len(vec), EmbeddingDim)
	}
	return nil
}

// EmbedderPreflightToken is the deterministic, non-empty input
// ValidateEmbedder hands to the Embedder so the dimension check
// always exercises the production path (an empty token would let an
// Embedder short-circuit before producing a vector). It is exported
// so tests for custom Embedders can assert the preflight contract
// without re-discovering the magic string.
// spec: §9.4 line 198; F-9.4.8.
const EmbedderPreflightToken = "memorystore-embedder-preflight"

// HashingEmbedder is the deterministic, dependency-free default
// Embedder: it hashes the bag of word tokens into a fixed-width
// feature vector and L2-normalises the result, so the cosine distance
// pgvector computes is a usable lexical-overlap similarity. It needs no
// external API, which keeps a dev install and the test suite free of a
// network dependency, and it is deterministic, so a query embeds to the
// same vector every time.
//
// HashingEmbedder is a feature-hashing embedder, not a learned model:
// it captures token overlap, not deeper semantic similarity. A
// deployer who wants semantic recall beyond shared vocabulary supplies
// a provider-backed Embedder through the same interface.
type HashingEmbedder struct{}

// NewHashingEmbedder returns the deterministic local Embedder.
func NewHashingEmbedder() HashingEmbedder { return HashingEmbedder{} }

var _ Embedder = HashingEmbedder{}

// Embed implements Embedder. It tokenises text on non-alphanumeric
// runes, folds each token to lower case, hashes it into one of
// EmbeddingDim buckets with a sign bit, and L2-normalises the
// accumulated vector. Empty or token-free text yields a nil slice so
// the caller takes the substring fallback path.
func (HashingEmbedder) Embed(text string) ([]float32, error) {
	tokens := tokenize(text)
	if len(tokens) == 0 {
		return nil, nil
	}
	vec := make([]float32, EmbeddingDim)
	for _, tok := range tokens {
		h := fnv.New64a()
		_, _ = h.Write([]byte(tok))
		sum := h.Sum64()
		bucket := sum % EmbeddingDim
		// The low bit of the hash picks the sign so distinct tokens that
		// collide on a bucket do not always reinforce each other.
		if sum&(1<<63) != 0 {
			vec[bucket]--
		} else {
			vec[bucket]++
		}
	}
	normalize(vec)
	return vec, nil
}

// tokenize splits text into lower-cased alphanumeric tokens.
func tokenize(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

// normalize scales vec to unit L2 length in place. A zero vector is
// left unchanged.
func normalize(vec []float32) {
	var sumSquares float64
	for _, v := range vec {
		sumSquares += float64(v) * float64(v)
	}
	if sumSquares == 0 {
		return
	}
	norm := float32(math.Sqrt(sumSquares))
	for i := range vec {
		vec[i] /= norm
	}
}

// CosineDistance is the pgvector `<=>` cosine-distance metric: one
// minus the cosine similarity of a and b, so identical-direction
// vectors are distance 0 and opposite ones are distance 2. It is the
// metric the migration-0044 ivfflat index ranks by, and InMemory.Query
// uses it so the in-memory and Postgres backends order matches the
// same way. Mismatched lengths or a zero-magnitude vector yield
// distance 1 (orthogonal), the neutral pgvector result for an
// undefined cosine.
func CosineDistance(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 1
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 1
	}
	sim := dot / (math.Sqrt(na) * math.Sqrt(nb))
	// Floating-point rounding can push the similarity a hair outside
	// [-1, 1]; clamp so the distance stays in [0, 2].
	switch {
	case sim > 1:
		sim = 1
	case sim < -1:
		sim = -1
	}
	return 1 - sim
}
