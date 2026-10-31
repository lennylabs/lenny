// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// isolationPrincipals are the callers of the isolation driver. dave holds
// the session-reader custom role, which grants the tenant read bypass.
var isolationPrincipals = []authmw.Principal{
	{Subject: "alice@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleUser}},
	{Subject: "bob@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleUser}},
	{Subject: "carol@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleUser}},
	{Subject: "dave@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.Role(sessionReader)}},
}

// isolationWorld is the driver's server and the owner each row must carry.
type isolationWorld struct {
	t       *testing.T
	h       http.Handler
	store   *memstore.Store
	want    map[string]string
	nextID  int
	derived int
	// ownerRefusals records the owner's derives and replays that did not
	// create a session, for the failure message.
	ownerRefusals []string
}

// newIsolationWorld seeds one terminal source session per principal, each
// with its own chunked checkpoint, so a derive or a replay from it copies a
// workspace.
func newIsolationWorld(t *testing.T) *isolationWorld {
	t.Helper()
	w := &isolationWorld{t: t, store: memstore.New(), want: map[string]string{}}
	blobs := blobstore.NewMemoryStore(nil)
	manifests := partialmanifeststore.NewMemoryStore(nil)
	for i, p := range isolationPrincipals {
		id := fmt.Sprintf("sess_seed_%d", i)
		seedChunkedCheckpoint(t, blobs, manifests, id, "ckpt_"+id, sourceChunkBytes)
		newSourceSession(t, w.store, func(row *sessionstore.Session) {
			row.ID, row.UserID = id, p.Subject
			row.WorkspaceSnapshot.Ref = "/acme/checkpoints/" + id + "/ckpt_" + id
		})
		w.want[id] = p.Subject
	}
	srv := sessionserver.New(w.store, sessionserver.Options{
		DevTenantHeader:          true,
		CustomRoles:              accessRoles(t),
		Blobs:                    blobs,
		CheckpointManifestReader: manifests,
		CheckpointManifestWriter: manifests,
		ResumeChunkResolver:      stubChunkResolver{},
		IDFunc: func() string {
			w.nextID++
			return fmt.Sprintf("sess_gen_%d", w.nextID)
		},
	})
	t.Cleanup(srv.Close)
	w.h = srv.Handler()
	return w
}

// do sends one request as p and returns the recorder.
func (w *isolationWorld) do(p authmw.Principal, method, path string, body any) *httptest.ResponseRecorder {
	w.t.Helper()
	var buf []byte
	if body != nil {
		buf, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req = req.WithContext(authmw.WithPrincipal(req.Context(), p))
	rr := httptest.NewRecorder()
	w.h.ServeHTTP(rr, req)
	return rr
}

// readable is the §10.2 read rule the checker holds every response to.
func readable(p authmw.Principal, owner string) bool {
	return owner == p.Subject || p.Roles[0] == pkgauth.Role(sessionReader)
}

// createdID returns the id of the session a 201 response created.
func createdID(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		ID        string `json:"id"`
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode create response %s: %v", rr.Body.String(), err)
	}
	if out.ID != "" {
		return out.ID
	}
	return out.SessionID
}

// step runs one randomly chosen operation as a random principal and
// records the owner every new row must carry.
func (w *isolationWorld) step(r *rand.Rand, ids []string) {
	w.t.Helper()
	p := isolationPrincipals[r.IntN(len(isolationPrincipals))]
	other := isolationPrincipals[r.IntN(len(isolationPrincipals))].Subject
	target := ids[r.IntN(len(ids))]
	switch r.IntN(5) {
	case 0:
		rr := w.do(p, http.MethodPost, "/v1/sessions", map[string]string{"runtimeRef": "claude-code", "userId": other})
		if rr.Code == http.StatusCreated {
			w.want[createdID(w.t, rr)] = p.Subject
		}
	case 1, 2:
		path, body := "/v1/sessions/"+target+"/derive", map[string]string{"userId": other}
		if r.IntN(2) == 0 {
			path, body = "/v1/sessions/"+target+"/replay", map[string]string{"targetRuntime": "x", "replayMode": "workspace_derive", "userId": other}
		}
		rr := w.do(p, http.MethodPost, path, body)
		if rr.Code == http.StatusCreated {
			w.want[createdID(w.t, rr)] = w.want[target]
			w.derived++
		} else if p.Subject == w.want[target] {
			w.ownerRefusals = append(w.ownerRefusals, fmt.Sprintf("%s = %d %s", path, rr.Code, rr.Body.String()))
		} else if rr.Code != http.StatusNotFound {
			w.t.Fatalf("%s by non-owner %s = %d %s, want 404", path, p.Subject, rr.Code, rr.Body.String())
		}
	case 3:
		rr := w.do(p, http.MethodGet, "/v1/sessions/"+target, nil)
		if ok := readable(p, w.want[target]); ok != (rr.Code == http.StatusOK) {
			w.t.Fatalf("GET %s (owner %s) by %s = %d, readable %v", target, w.want[target], p.Subject, rr.Code, ok)
		}
	default:
		w.checkList(p)
	}
}

// checkList asserts that p's list exposes only rows p may read, and every
// row p owns.
func (w *isolationWorld) checkList(p authmw.Principal) {
	w.t.Helper()
	rr := w.do(p, http.MethodGet, "/v1/sessions?limit=1000", nil)
	if rr.Code != http.StatusOK {
		w.t.Fatalf("list by %s = %d %s", p.Subject, rr.Code, rr.Body.String())
	}
	var env struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		w.t.Fatalf("decode list: %v", err)
	}
	for _, it := range env.Items {
		if !readable(p, w.want[it.ID]) {
			w.t.Fatalf("list by %s exposed %s owned by %s", p.Subject, it.ID, w.want[it.ID])
		}
	}
}

// checkOwners asserts every stored row carries the owner the driver
// recorded: its creating subject, or its source's owner.
func (w *isolationWorld) checkOwners() []string {
	w.t.Helper()
	rows, err := w.store.List(context.Background(), "acme", sessionstore.ListFilter{})
	if err != nil {
		w.t.Fatalf("list store: %v", err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		want, ok := w.want[row.ID]
		if !ok || row.UserID != want {
			w.t.Fatalf("row %s owner = %q, want %q (recorded %v)", row.ID, row.UserID, want, ok)
		}
		ids = append(ids, row.ID)
	}
	return ids
}

// TestSessionOwnerIsolationInvariant_spec_10_2 drives randomized create,
// derive, replay, get, and list operations from several principals, each
// create and derive naming a random other user as userId. After every
// operation the checker asserts that every row's owner is its creating
// subject or its source's owner, and that no response exposed a row the
// §10.2 read rule refuses the caller. The seed is logged so a failure
// replays.
//
// spec: §10.2 (Session ownership) rules 1, 2, 4, 5, 6, and 7.
func TestSessionOwnerIsolationInvariant_spec_10_2(t *testing.T) {
	seed := uint64(time.Now().UnixNano())
	t.Logf("seed %d", seed)
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	w := newIsolationWorld(t)
	ids := w.checkOwners()
	for range 300 {
		w.step(r, ids)
		ids = w.checkOwners()
	}
	if w.derived == 0 {
		t.Fatalf("no derive or replay succeeded; the driver did not exercise owner inheritance; owner refusals: %v", w.ownerRefusals)
	}
}
