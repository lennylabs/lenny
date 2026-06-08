// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionusage"
	"github.com/lennylabs/lenny/pkg/gateway/taskusage"
	"github.com/lennylabs/lenny/pkg/gateway/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
	"github.com/lennylabs/lenny/pkg/task"
)

// fakeCatalog is a minimal artifactcatalog.Store: only ListBySession is
// implemented. The embedded nil interface panics if the materialization
// path calls any other method, keeping the fake honest about what it
// actually reads.
type fakeCatalog struct {
	artifactcatalog.Store
	rows []artifactcatalog.Record
}

func (f fakeCatalog) ListBySession(_ context.Context, _, _ string) ([]artifactcatalog.Record, error) {
	return f.rows, nil
}

var taskResultClock = func() time.Time { return time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC) }

// TestMaterializeTaskResultCompletedOutput_spec_8_8_2 asserts a completed
// child's §8.8 TaskResult.output carries the child's final emitted turn
// as a part and only its deliverable (live, non-internal) artifacts as
// blob refs. usage / treeUsage stay nil when no TaskUsage Builder is
// wired (the developer posture). F-8.8.2.
func TestMaterializeTaskResultCompletedOutput_spec_8_8_2(t *testing.T) {
	tx := transcriptstore.NewMemory()
	if err := tx.Append(context.Background(), "acme", "sess_c",
		transcriptstore.Entry{Role: "user", Content: "do X"},
		transcriptstore.Entry{Role: "assistant", Content: "result: done"},
	); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}
	cat := fakeCatalog{rows: []artifactcatalog.Record{
		{URI: "lenny-blob://acme/workspace/sess_c/part_1", State: artifactcatalog.StateLive, ArtifactType: artifactcatalog.ArtifactTypeWorkspace},
		{URI: "lenny-blob://acme/checkpoint/sess_c/ck_1", State: artifactcatalog.StateLive, ArtifactType: artifactcatalog.ArtifactTypeCheckpoint},
		{URI: "lenny-blob://acme/workspace/sess_c/part_gone", State: artifactcatalog.StateSoftDeleted, ArtifactType: artifactcatalog.ArtifactTypeWorkspace},
	}}
	srv := New(memstore.New(), Options{Clock: taskResultClock, Transcripts: tx, Artifacts: cat})

	res := srv.materializeTaskResult(context.Background(),
		sessionstore.Session{ID: "sess_c", TenantID: "acme", State: session.StateCompleted}, 0)

	if res.SchemaVersion != task.SchemaVersion || res.State != "completed" || res.Error != nil {
		t.Fatalf("result = %+v, want schemaVersion=%d state=completed error=nil", res, task.SchemaVersion)
	}
	if res.Usage != nil || res.TreeUsage != nil {
		t.Errorf("usage/treeUsage = %+v/%+v, want nil when no TaskUsage Builder is wired", res.Usage, res.TreeUsage)
	}
	if res.Output == nil || len(res.Output.Parts) != 1 || res.Output.Parts[0].Inline != "result: done" {
		t.Fatalf("output.parts = %+v, want the final agent turn 'result: done'", res.Output)
	}
	if len(res.Output.ArtifactRefs) != 1 || res.Output.ArtifactRefs[0] != "lenny-blob://acme/workspace/sess_c/part_1" {
		t.Errorf("output.artifactRefs = %v, want only the live workspace blob", res.Output.ArtifactRefs)
	}
}

// TestMaterializeTaskResultFailedError_spec_8_8_4 asserts a non-completed
// terminal child's §8.8 TaskResult.error carries the code, the §15.2.1
// classifier category, and a retriesExhausted sourced from the row's
// retry budget. Output stays nil for a failure. F-8.8.4.
func TestMaterializeTaskResultFailedError_spec_8_8_4(t *testing.T) {
	srv := New(memstore.New(), Options{Clock: taskResultClock})
	cases := []struct {
		name              string
		state             session.State
		failureReason     string
		retryCount        int64
		maxRetries        int
		wantCode          string
		wantCategory      string
		wantRetriesExh    bool
		wantProtocolState string
	}{
		{"budget exhausted", session.StateFailed, "DELEGATION_BUDGET_EXHAUSTED", 2, 2, "DELEGATION_BUDGET_EXHAUSTED", "POLICY", true, "failed"},
		{"no reason falls back", session.StateFailed, "", 0, 0, "CHILD_FAILED", "TRANSIENT", false, "failed"},
		{"expired maps to failed", session.StateExpired, "expired:lease", 0, 0, "expired:lease", "TRANSIENT", false, "failed"},
		{"cancelled", session.StateCancelled, "", 0, 0, "CHILD_CANCELLED", "TRANSIENT", false, "canceled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sess := sessionstore.Session{
				ID: "sess_c", TenantID: "acme", State: tc.state,
				FailureReason: tc.failureReason, RetryCount: tc.retryCount,
			}
			if tc.maxRetries > 0 {
				sess.RetryPolicy = &session.RetryPolicy{MaxRetries: tc.maxRetries}
			}
			res := srv.materializeTaskResult(context.Background(), sess, 0)
			if res.Output != nil {
				t.Errorf("output = %+v, want nil for a failure", res.Output)
			}
			if res.State != tc.wantProtocolState {
				t.Errorf("state = %q, want %q", res.State, tc.wantProtocolState)
			}
			if res.Error == nil {
				t.Fatalf("error = nil, want a populated error block")
			}
			if res.Error.Code != tc.wantCode || res.Error.Category != tc.wantCategory || res.Error.RetriesExhausted != tc.wantRetriesExh {
				t.Errorf("error = %+v, want code=%q category=%q retriesExhausted=%v",
					res.Error, tc.wantCode, tc.wantCategory, tc.wantRetriesExh)
			}
		})
	}
}

// TestIsDeliverableArtifact_spec_8_8_2 pins the artifactRefs filter: only
// live workspace/export artifacts (the child's deliverable output) count;
// internal kinds and non-live states are excluded. F-8.8.2.
func TestIsDeliverableArtifact_spec_8_8_2(t *testing.T) {
	cases := []struct {
		name string
		rec  artifactcatalog.Record
		want bool
	}{
		{"live workspace", artifactcatalog.Record{State: artifactcatalog.StateLive, ArtifactType: artifactcatalog.ArtifactTypeWorkspace}, true},
		{"live export", artifactcatalog.Record{State: artifactcatalog.StateLive, ArtifactType: artifactcatalog.ArtifactTypeExport}, true},
		{"live default type", artifactcatalog.Record{State: artifactcatalog.StateLive, ArtifactType: ""}, true},
		{"live checkpoint", artifactcatalog.Record{State: artifactcatalog.StateLive, ArtifactType: artifactcatalog.ArtifactTypeCheckpoint}, false},
		{"live eviction_context", artifactcatalog.Record{State: artifactcatalog.StateLive, ArtifactType: artifactcatalog.ArtifactTypeEvictionContext}, false},
		{"live session_log", artifactcatalog.Record{State: artifactcatalog.StateLive, ArtifactType: artifactcatalog.ArtifactTypeSessionLog}, false},
		{"soft-deleted workspace", artifactcatalog.Record{State: artifactcatalog.StateSoftDeleted, ArtifactType: artifactcatalog.ArtifactTypeWorkspace}, false},
		{"tombstoned export", artifactcatalog.Record{State: artifactcatalog.StateTombstoned, ArtifactType: artifactcatalog.ArtifactTypeExport}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDeliverableArtifact(tc.rec); got != tc.want {
				t.Errorf("isDeliverableArtifact(%+v) = %v, want %v", tc.rec, got, tc.want)
			}
		})
	}
}

// TestArchiveSettledChildPreservesSchemaVersion_spec_8_8_11 asserts the
// §8.10 archive write reconciles the envelope schemaVersion: a re-archive
// of a node whose prior body recorded a different version keeps the
// original, while a fresh node takes the current producer version. This
// is the read-modify-write enforcement of the §8.8 immutability rule.
// F-8.8.11.
func TestArchiveSettledChildPreservesSchemaVersion_spec_8_8_11(t *testing.T) {
	store := memstore.New()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_p", TenantID: "acme", State: session.StateRunning,
	}); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	archive := treearchive.NewMemory()
	srv := New(store, Options{Clock: taskResultClock, TreeArchive: archive})

	// A prior writer recorded this node at schema version 7.
	prior, _ := json.Marshal(task.Result{SchemaVersion: 7, TaskID: "sess_c", State: "completed"})
	if err := archive.Archive(context.Background(), treearchive.ArchivedNode{
		TenantID: "acme", RootSessionID: "sess_p", NodeSessionID: "sess_c",
		ParentSessionID: "sess_p", State: "completed", Result: string(prior),
		SettledAt: taskResultClock(),
	}); err != nil {
		t.Fatalf("seed prior archive: %v", err)
	}

	srv.archiveSettledChild(context.Background(), sessionstore.Session{
		ID: "sess_c", TenantID: "acme", State: session.StateCompleted, ParentSessionID: "sess_p",
	})
	if got := archivedSchemaVersion(t, archive, "sess_c"); got != 7 {
		t.Errorf("re-archived schemaVersion = %d, want the preserved 7", got)
	}

	// A node with no prior archive takes the current producer version.
	srv.archiveSettledChild(context.Background(), sessionstore.Session{
		ID: "sess_d", TenantID: "acme", State: session.StateCompleted, ParentSessionID: "sess_p",
	})
	if got := archivedSchemaVersion(t, archive, "sess_d"); got != task.SchemaVersion {
		t.Errorf("fresh schemaVersion = %d, want producer %d", got, task.SchemaVersion)
	}

	// The materialize helper preserves an explicit prior version directly.
	res := srv.materializeTaskResult(context.Background(),
		sessionstore.Session{ID: "sess_e", TenantID: "acme", State: session.StateCompleted}, 9)
	if res.SchemaVersion != 9 {
		t.Errorf("materialize with existing=9 → schemaVersion %d, want 9", res.SchemaVersion)
	}
}

// TestMaterializeTaskResultStampsUsage_spec_8_8_3 asserts that with a
// TaskUsage Builder wired, a settled leaf task's materialized §8.8
// TaskResult carries its own usage (tokens from the accumulator, time
// dimensions derived from the row) and a treeUsage with totalTasks=1.
// F-8.8.3 / F-8.9.4.
func TestMaterializeTaskResultStampsUsage_spec_8_8_3(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	tokens := sessionusage.NewMemory()
	archive := treearchive.NewMemory()

	created := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	terminal := created.Add(90 * time.Second) // 1.5 pod/lease minutes
	sess := sessionstore.Session{
		ID: "sess_u", TenantID: "acme", RootSessionID: "sess_u",
		State: session.StateCompleted, PodAssignment: "pod-1",
		CreatedAt: created, UpdatedAt: terminal,
	}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := tokens.Add(ctx, "acme", "sess_u", 15000, 8000); err != nil {
		t.Fatalf("add tokens: %v", err)
	}

	builder := taskusage.New(store, tokens, archive, func() time.Time { return terminal })
	srv := New(store, Options{Clock: taskResultClock, TreeArchive: archive, TaskUsage: builder})

	res := srv.materializeTaskResult(ctx, sess, 0)
	if res.Usage == nil {
		t.Fatal("usage nil; want the §8.8 per-task usage block")
	}
	if res.Usage.InputTokens != 15000 || res.Usage.OutputTokens != 8000 {
		t.Errorf("usage tokens = %d/%d, want 15000/8000", res.Usage.InputTokens, res.Usage.OutputTokens)
	}
	if res.Usage.WallClockSeconds != 90 || res.Usage.PodMinutes != 1.5 || res.Usage.CredentialLeaseMinutes != 1.5 {
		t.Errorf("usage time dims = %v/%v/%v, want 90/1.5/1.5",
			res.Usage.WallClockSeconds, res.Usage.PodMinutes, res.Usage.CredentialLeaseMinutes)
	}
	if res.TreeUsage == nil {
		t.Fatal("treeUsage nil; a settled leaf should roll up to its own usage")
	}
	if res.TreeUsage.TotalTasks != 1 || res.TreeUsage.InputTokens != 15000 {
		t.Errorf("treeUsage = %+v, want totalTasks=1 inputTokens=15000", res.TreeUsage)
	}
}

func archivedSchemaVersion(t *testing.T, archive treearchive.Store, nodeID string) int {
	t.Helper()
	node, err := archive.GetByNode(context.Background(), "acme", nodeID)
	if err != nil {
		t.Fatalf("GetByNode %s: %v", nodeID, err)
	}
	var res task.Result
	if err := json.Unmarshal([]byte(node.Result), &res); err != nil {
		t.Fatalf("decode archived body %q: %v", node.Result, err)
	}
	return res.SchemaVersion
}
