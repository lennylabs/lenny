// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 HTTP contract tests for the snapshot copy behind POST
// /v1/sessions/{id}/derive and POST /v1/sessions/{id}/replay in both replay
// modes. The copy reads only a §10.1 chunked checkpoint: a source ref with no
// manifest row and a missing source chunk answer 503
// DERIVE_SNAPSHOT_UNAVAILABLE, while a transient manifest read error and a
// gateway with no chunk resolver answer 500 INTERNAL_ERROR. Every error body
// is validated against the response the served OpenAPI document declares for
// the operation.
//
// spec: §7.1 (Normal Flow: Derive copy semantics, Derive restore semantics,
// Concurrent derive serialization), §15.1 (REST API: Session Replay
// Semantics, Error code catalog)
package rest_sessions_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/resumechunks"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/openapi"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// snapCopySourceID and snapCopyCheckpointID name the source session and its
// seeded chunked checkpoint.
const (
	snapCopySourceID     = "sess_snap_source"
	snapCopyCheckpointID = "ckpt_snap_source"
)

// snapCopySourceRef is the four-segment snapshot ref of the seeded checkpoint.
var snapCopySourceRef = "/acme/checkpoints/" + snapCopySourceID + "/" + snapCopyCheckpointID

// snapCopyEndpoint is one operation that copies the source's snapshot.
type snapCopyEndpoint struct {
	name string
	path string
	body any
}

var snapCopyEndpoints = []snapCopyEndpoint{
	{"derive", "/v1/sessions/{id}/derive", sessionserver.DeriveRequest{}},
	{"replay prompt_history", "/v1/sessions/{id}/replay", sessionserver.ReplayRequest{
		ReplayMode: sessionserver.ReplayPromptHistory, TargetRuntime: "echo",
	}},
	{"replay workspace_derive", "/v1/sessions/{id}/replay", sessionserver.ReplayRequest{
		ReplayMode: sessionserver.ReplayWorkspaceDerive, TargetRuntime: "echo",
	}},
}

// snapCopyResolver stands in for the chunk resolver the new session's
// first-bind restore reads. The copy only requires that one is wired.
type snapCopyResolver struct{}

func (snapCopyResolver) Resolve(context.Context, string, string, string) (resumechunks.ResolveResult, error) {
	return resumechunks.ResolveResult{Grants: []adapterclient.ChunkGrant{{URL: "https://objects.example/chunk-0"}}}, nil
}

// snapCopyBrokenManifests fails every manifest read with an error other
// than not found, as a transient database error does.
type snapCopyBrokenManifests struct{}

var errSnapCopyManifest = errors.New("checkpoint_manifest read failed")

func (snapCopyBrokenManifests) Get(context.Context, string, string) (partialmanifeststore.Record, error) {
	return partialmanifeststore.Record{}, errSnapCopyManifest
}

func (snapCopyBrokenManifests) LatestActiveAny(context.Context, string, string) (partialmanifeststore.Record, error) {
	return partialmanifeststore.Record{}, errSnapCopyManifest
}

func (snapCopyBrokenManifests) LatestFull(context.Context, string, string) (partialmanifeststore.Record, error) {
	return partialmanifeststore.Record{}, errSnapCopyManifest
}

// snapCopyFixture is a terminal source session whose snapshot ref names a
// one-chunk checkpoint, with the stores a server copies it from.
type snapCopyFixture struct {
	store     *memstore.Store
	blobs     *blobstore.MemoryStore
	manifests *partialmanifeststore.MemoryStore
}

func newSnapCopyFixture(t *testing.T) snapCopyFixture {
	t.Helper()
	f := snapCopyFixture{
		store:     memstore.New(),
		blobs:     blobstore.NewMemoryStore(nil),
		manifests: partialmanifeststore.NewMemoryStore(nil),
	}
	ctx := context.Background()
	enc := partialmanifeststore.ChunkEncodingTar
	chunk := resumechunks.ChunkObjectURI("acme", snapCopySourceID, snapCopyCheckpointID, 0, enc)
	chunk.TTL = time.Hour
	if _, err := f.blobs.Put(chunk, "application/x-tar", strings.NewReader("workspace-bytes")); err != nil {
		t.Fatalf("seed source chunk: %v", err)
	}
	if err := f.manifests.Put(ctx, partialmanifeststore.Record{
		TenantID: "acme", CheckpointID: snapCopyCheckpointID, SessionID: snapCopySourceID,
		ManifestReason: partialmanifeststore.ReasonInProgress, ChunkEncoding: enc,
		ChunkObjectKeyPrefix: snapCopySourceRef + "/",
	}); err != nil {
		t.Fatalf("seed source manifest: %v", err)
	}
	if err := f.manifests.ConfirmChunk(ctx, "acme", snapCopyCheckpointID, 0, int64(len("workspace-bytes"))); err != nil {
		t.Fatalf("confirm source chunk: %v", err)
	}
	if err := f.manifests.Finalise(ctx, "acme", snapCopyCheckpointID, false, partialmanifeststore.ReasonComplete); err != nil {
		t.Fatalf("finalise source manifest: %v", err)
	}
	if err := f.store.Create(ctx, sessionstore.Session{
		ID: snapCopySourceID, TenantID: "acme", UserID: "alice@acme.com",
		State: session.StateCompleted, RuntimeRef: "echo", PoolRef: "default-pool",
		IsolationProfile: isolation.ProfileSandboxed,
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{
			Ref: snapCopySourceRef, Source: sessionstore.WorkspaceSnapshotSealed,
		},
	}); err != nil {
		t.Fatalf("seed source session: %v", err)
	}
	return f
}

// handler builds a session server over the fixture with every copy
// dependency wired, then applies mod.
func (f snapCopyFixture) handler(mod func(*sessionserver.Options)) http.Handler {
	ids := []string{"sess_snap_new", "ckpt_snap_new"}
	o := sessionserver.Options{
		DevTenantHeader: true,
		IDFunc: func() string {
			id := ids[0]
			ids = append(ids[1:], "id_exhausted")
			return id
		},
		Blobs:                    f.blobs,
		CheckpointManifestReader: f.manifests,
		CheckpointManifestWriter: f.manifests,
		ResumeChunkResolver:      snapCopyResolver{},
	}
	if mod != nil {
		mod(&o)
	}
	return ownerprincipal.Wrap(sessionserver.New(f.store, o).Handler(), ownerprincipal.Alice)
}

// call posts ep's body to ep's path for the source session.
func (ep snapCopyEndpoint) call(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(ep.body)
	req := httptest.NewRequest(http.MethodPost, strings.Replace(ep.path, "{id}", snapCopySourceID, 1), bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// snapCopyValidator validates a response body against the schema the
// served OpenAPI document declares for the operation's status.
type snapCopyValidator struct {
	doc      servedSessionDocument
	compiler *jsonschema.Compiler
}

func newSnapCopyValidator(t *testing.T) snapCopyValidator {
	t.Helper()
	ts := httptest.NewServer(openapi.Handler())
	t.Cleanup(ts.Close)
	raw := fetchServedDocument(t, ts.URL+"/openapi.json")
	var doc servedSessionDocument
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("decode served /openapi.json: %v", err)
	}
	c := schematest.NewCompiler(t)
	if err := c.AddResource("openapi.json", strings.NewReader(raw)); err != nil {
		t.Fatalf("add served document to the schema compiler: %v", err)
	}
	return snapCopyValidator{doc: doc, compiler: c}
}

// validate checks body against the schema the operation declares for
// status. A status the operation does not declare is validated against the
// Error envelope, which the document states every gateway error response
// carries.
func (v snapCopyValidator) validate(t *testing.T, path, status string, body []byte) {
	t.Helper()
	op := servedOperation(t, v.doc, path, "post")
	ref := "#/components/schemas/Error"
	if resp, ok := op.Responses[status]; ok {
		ref = schemaRef(resp.Content)
		if ref == "" {
			t.Fatalf("POST %s declares its %s response with no application/json schema", path, status)
		}
	}
	schema, err := v.compiler.Compile("openapi.json" + ref)
	if err != nil {
		t.Fatalf("compile %s: %v", ref, err)
	}
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("response body is not JSON: %v; body %s", err, body)
	}
	if err := schema.Validate(doc); err != nil {
		t.Errorf("POST %s %s body does not match %s: %v; body %s", path, status, ref, err, body)
	}
}

// spec: §7.1 (Normal Flow: Derive copy semantics, Derive restore semantics, Concurrent derive serialization), §15.1 (REST API: Session Replay Semantics, Error code catalog)
// diagnosis: a failure means derive or replay answered a missing source
// snapshot with a code other than 503 DERIVE_SNAPSHOT_UNAVAILABLE, answered a
// transient manifest read error or an unwired chunk resolver with other than
// 500 INTERNAL_ERROR, or wrote a body the served OpenAPI document does not
// declare for the operation. A 503 on the transient read means the copy
// treats a manifest read error as a missing snapshot; a 201 with no resolver
// means the copy stores a ref the new session's first bind cannot restore.
// Check copyWorkspaceSnapshot and writeSnapshotCopyError in
// pkg/gateway/sessionserver/snapshot_copy.go, and the 503 responses of the
// derive and replay operations in pkg/gateway/externalapi/openapi/openapi.json.
func TestDeriveAndReplaySnapshotCopyErrorContract_spec_7_1(t *testing.T) {
	v := newSnapCopyValidator(t)
	cases := []struct {
		name   string
		mod    func(t *testing.T, f snapCopyFixture) func(*sessionserver.Options)
		status int
		code   string
	}{
		{
			name: "complete checkpoint copies", status: http.StatusCreated,
			mod: func(*testing.T, snapCopyFixture) func(*sessionserver.Options) { return nil },
		},
		{
			name: "missing manifest", status: http.StatusServiceUnavailable, code: "DERIVE_SNAPSHOT_UNAVAILABLE",
			mod: func(*testing.T, snapCopyFixture) func(*sessionserver.Options) {
				return func(o *sessionserver.Options) {
					o.CheckpointManifestReader = partialmanifeststore.NewMemoryStore(nil)
				}
			},
		},
		{
			name: "missing source chunk", status: http.StatusServiceUnavailable, code: "DERIVE_SNAPSHOT_UNAVAILABLE",
			mod: func(t *testing.T, f snapCopyFixture) func(*sessionserver.Options) {
				if _, err := f.blobs.DeleteBySession(context.Background(), "acme", snapCopySourceID); err != nil {
					t.Fatalf("delete source chunk: %v", err)
				}
				return nil
			},
		},
		{
			name: "transient manifest read error", status: http.StatusInternalServerError, code: "INTERNAL_ERROR",
			mod: func(*testing.T, snapCopyFixture) func(*sessionserver.Options) {
				return func(o *sessionserver.Options) { o.CheckpointManifestReader = snapCopyBrokenManifests{} }
			},
		},
		{
			name: "unwired chunk resolver", status: http.StatusInternalServerError, code: "INTERNAL_ERROR",
			mod: func(*testing.T, snapCopyFixture) func(*sessionserver.Options) {
				return func(o *sessionserver.Options) { o.ResumeChunkResolver = nil }
			},
		},
	}
	for _, ep := range snapCopyEndpoints {
		for _, tc := range cases {
			t.Run(ep.name+"/"+tc.name, func(t *testing.T) {
				f := newSnapCopyFixture(t)
				rr := ep.call(t, f.handler(tc.mod(t, f)))
				if rr.Code != tc.status {
					t.Fatalf("status = %d, want %d; body %s", rr.Code, tc.status, rr.Body.String())
				}
				if tc.status == http.StatusCreated {
					return
				}
				v.validate(t, ep.path, strconv.Itoa(rr.Code), rr.Body.Bytes())
				var env struct {
					Error struct {
						Code    string         `json:"code"`
						Details map[string]any `json:"details"`
					} `json:"error"`
				}
				if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
					t.Fatalf("decode error envelope: %v", err)
				}
				if env.Error.Code != tc.code {
					t.Errorf("code = %s, want %s", env.Error.Code, tc.code)
				}
				if tc.code == "DERIVE_SNAPSHOT_UNAVAILABLE" && env.Error.Details["snapshotRef"] != snapCopySourceRef {
					t.Errorf("details.snapshotRef = %v, want the source ref %s", env.Error.Details["snapshotRef"], snapCopySourceRef)
				}
				if _, err := f.store.Get(context.Background(), "acme", "sess_snap_new"); !errors.Is(err, sessionstore.ErrNotFound) {
					t.Errorf("the failed copy inserted a row (err %v)", err)
				}
			})
		}
	}
}
