// SPDX-License-Identifier: MIT

package memorystore_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
)

// spec: §9.4 pgvector embedding — the deterministic local Embedder.

func TestHashingEmbedderIsDeterministic(t *testing.T) {
	e := memorystore.NewHashingEmbedder()
	a, err := e.Embed("the deploy key lives in vault")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	b, _ := e.Embed("the deploy key lives in vault")
	if len(a) != memorystore.EmbeddingDim {
		t.Fatalf("embedding width = %d, want %d", len(a), memorystore.EmbeddingDim)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("embedding not deterministic at index %d: %v vs %v", i, a[i], b[i])
		}
	}
}

func TestHashingEmbedderEmptyTextYieldsNil(t *testing.T) {
	e := memorystore.NewHashingEmbedder()
	for _, in := range []string{"", "   ", "!!! ??? ..."} {
		v, err := e.Embed(in)
		if err != nil {
			t.Fatalf("Embed(%q): %v", in, err)
		}
		if v != nil {
			t.Errorf("Embed(%q) = %v, want nil (no tokens)", in, v)
		}
	}
}

func TestHashingEmbedderIsUnitLength(t *testing.T) {
	e := memorystore.NewHashingEmbedder()
	v, _ := e.Embed("vault deploy key rotation policy")
	var sumSquares float64
	for _, x := range v {
		sumSquares += float64(x) * float64(x)
	}
	if math.Abs(math.Sqrt(sumSquares)-1.0) > 1e-5 {
		t.Errorf("embedding L2 norm = %v, want 1.0", math.Sqrt(sumSquares))
	}
}

func TestCosineDistanceRanksRelatedTextCloser(t *testing.T) {
	e := memorystore.NewHashingEmbedder()
	query, _ := e.Embed("where is the deploy key")
	near, _ := e.Embed("the deploy key is stored in vault")
	far, _ := e.Embed("lunch today was a good sandwich")

	dNear := memorystore.CosineDistance(query, near)
	dFar := memorystore.CosineDistance(query, far)
	if dNear >= dFar {
		t.Errorf("cosine distance: near=%v should be < far=%v", dNear, dFar)
	}
	// An identical vector is distance 0.
	if d := memorystore.CosineDistance(query, query); math.Abs(d) > 1e-6 {
		t.Errorf("self-distance = %v, want 0", d)
	}
}

func TestCosineDistanceMismatchedLengthIsOrthogonal(t *testing.T) {
	if d := memorystore.CosineDistance([]float32{1, 0}, []float32{1, 0, 0}); d != 1 {
		t.Errorf("mismatched-length distance = %v, want 1", d)
	}
	if d := memorystore.CosineDistance(nil, nil); d != 1 {
		t.Errorf("empty-vector distance = %v, want 1", d)
	}
}

// TestQueryRanksBySemanticSimilarity verifies the §9.4 vector-ranking
// path: among the memories that match the query string, the one whose
// content is closest to the query embedding is returned first, even
// when it is the oldest write.
func TestQueryRanksBySemanticSimilarity(t *testing.T) {
	s := memorystore.NewInMemory(0, nil)
	ctx := context.Background()
	base := time.Date(2026, 5, 18, 9, 0, 0, 0, time.UTC)
	// Both memories contain the phrase "deploy key vault" as a
	// substring, so both survive the substring filter. The first
	// memory's content is exactly the query phrase, so its embedding
	// equals the query embedding (cosine distance 0); the second
	// carries extra tokens and is therefore farther. The first memory
	// is the older write, so a recency-only ordering would rank it
	// last — the embedding ranking must override that.
	if err := s.Write(ctx, acmeAlice(), []memorystore.Memory{
		{Content: "deploy key vault", CreatedAt: base},
		{Content: "deploy key vault rotation schedule audit log review", CreatedAt: base.Add(time.Hour)},
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	ranked, err := s.Query(ctx, acmeAlice(), "deploy key vault", 0)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(ranked) != 2 {
		t.Fatalf("Query returned %d, want 2", len(ranked))
	}
	if ranked[0].Content != "deploy key vault" {
		t.Errorf("semantic ranking = %v, want the exact-match memory first despite being the oldest write",
			contentsOf(ranked))
	}
}

func contentsOf(ms []memorystore.Memory) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Content
	}
	return out
}

// TestWriteComputesEmbedding verifies Write stamps an embedding so the
// pgvector column has a value to store.
func TestWriteComputesEmbedding(t *testing.T) {
	s := memorystore.NewInMemory(0, nil)
	ctx := context.Background()
	if err := s.Write(ctx, acmeAlice(), []memorystore.Memory{
		{Content: "remember the vault path"},
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, _ := s.List(ctx, acmeAlice(), memorystore.MemoryFilter{})
	if len(got) != 1 {
		t.Fatalf("List returned %d, want 1", len(got))
	}
	if len(got[0].Embedding) != memorystore.EmbeddingDim {
		t.Errorf("Write did not compute an embedding: len=%d, want %d",
			len(got[0].Embedding), memorystore.EmbeddingDim)
	}
}

// TestNilEmbedderFallsBackToRecency verifies that a store with no
// Embedder still serves Query, ordered newest-first.
func TestNilEmbedderFallsBackToRecency(t *testing.T) {
	s := memorystore.NewInMemoryWithEmbedder(0, nil, nil)
	ctx := context.Background()
	base := time.Date(2026, 5, 18, 9, 0, 0, 0, time.UTC)
	if err := s.Write(ctx, acmeAlice(), []memorystore.Memory{
		{Content: "key one", CreatedAt: base},
		{Content: "key two", CreatedAt: base.Add(time.Hour)},
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, _ := s.Query(ctx, acmeAlice(), "key", 0)
	if len(got) != 2 || got[0].Content != "key two" {
		t.Errorf("nil-embedder Query = %v, want newest-first [key two, key one]", contentsOf(got))
	}
	if got[0].Embedding != nil {
		t.Errorf("nil embedder should leave Embedding nil, got %v", got[0].Embedding)
	}
}
