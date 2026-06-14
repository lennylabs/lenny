// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/transcriptstore"
)

// messageNodeWire mirrors the §15.4.1 MessageDAG node the handler emits.
type messageNodeWire struct {
	ID   string `json:"id"`
	Seq  uint64 `json:"seq"`
	From struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	} `json:"from"`
	Role          string `json:"role"`
	Content       string `json:"content"`
	ThreadID      string `json:"threadId"`
	SchemaVersion int    `json:"schemaVersion"`
	CreatedAt     string `json:"createdAt"`
	Delivery      struct {
		Status      string `json:"status"`
		DeliveredAt string `json:"deliveredAt"`
	} `json:"delivery"`
}

type messagesEnvelopeWire struct {
	Items   []messageNodeWire `json:"items"`
	Cursor  string            `json:"cursor"`
	HasMore bool              `json:"hasMore"`
}

func seedMessagesServer(t *testing.T) (http.Handler, *transcriptstore.Memory, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	transcripts := transcriptstore.NewMemory()
	srv := sessionserver.New(store, sessionserver.Options{Transcripts: transcripts})
	return srv.Handler(), transcripts, store
}

func getMessages(t *testing.T, h http.Handler, path, tenant string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	h.ServeHTTP(rr, req)
	return rr
}

// TestMessagesList_spec_15_1_692 verifies GET /v1/sessions/{id}/messages
// returns the §15.4.1 MessageDAG over the durable session_messages store:
// every recorded node carries a stable id, the derived `from` object, the
// role/content, the delivery state, and the seq ordering.
//
// spec: §15.1 line 692; §15.4.1 lines 1696-1707, 1788-1798. F-15.1.3.
func TestMessagesList_spec_15_1_692(t *testing.T) {
	h, transcripts, store := seedMessagesServer(t)
	ctx := context.Background()
	_ = store.Create(ctx, sessionstore.Session{
		ID: "s1", TenantID: "acme", State: session.StateRunning, CreatedAt: time.Unix(1, 0),
	})
	if err := transcripts.Append(
		ctx, "acme", "s1",
		transcriptstore.Entry{Role: "user", Content: "hi", Timestamp: time.Unix(10, 0)},
		transcriptstore.Entry{Role: "assistant", Content: "hello", Timestamp: time.Unix(11, 0)},
		transcriptstore.Entry{Role: "system", Content: "rotated creds", Timestamp: time.Unix(12, 0)},
	); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}

	rr := getMessages(t, h, "/v1/sessions/s1/messages", "acme")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var env messagesEnvelopeWire
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rr.Body.String())
	}
	if len(env.Items) != 3 {
		t.Fatalf("items = %d, want 3; body=%s", len(env.Items), rr.Body.String())
	}
	// Ordering by coordinator-local seq.
	if env.Items[0].Seq != 1 || env.Items[1].Seq != 2 || env.Items[2].Seq != 3 {
		t.Errorf("seq order = %d,%d,%d", env.Items[0].Seq, env.Items[1].Seq, env.Items[2].Seq)
	}
	// Stable id surfaced on every node.
	for i, it := range env.Items {
		if it.ID == "" {
			t.Errorf("item[%d] missing stable id", i)
		}
		if it.Delivery.Status != "delivered" {
			t.Errorf("item[%d] delivery.status = %q, want delivered", i, it.Delivery.Status)
		}
		if it.SchemaVersion != transcriptstore.SchemaVersion {
			t.Errorf("item[%d] schemaVersion = %d, want %d", i, it.SchemaVersion, transcriptstore.SchemaVersion)
		}
	}
	// Derived from-attribution per role.
	if env.Items[0].From.Kind != "client" || env.Items[0].Role != "user" {
		t.Errorf("user node from = %+v role=%q", env.Items[0].From, env.Items[0].Role)
	}
	// spec: §15.4.1 line 1703 — an agent `from.id` is `sess_{session_id}`.
	if env.Items[1].From.Kind != "agent" || env.Items[1].From.ID != "sess_s1" {
		t.Errorf("assistant node from = %+v, want agent/sess_s1", env.Items[1].From)
	}
	if env.Items[2].From.Kind != "system" || env.Items[2].From.ID != "lenny-gateway" {
		t.Errorf("system node from = %+v, want system/lenny-gateway", env.Items[2].From)
	}
}

// TestMessagesListPaginationAndSince_spec_15_1_692 verifies the canonical
// cursor envelope and the spec-named `?since=` seq filter. spec: §15.4.1
// lines 1792-1793. F-15.1.3.
func TestMessagesListPaginationAndSince_spec_15_1_692(t *testing.T) {
	h, transcripts, store := seedMessagesServer(t)
	ctx := context.Background()
	_ = store.Create(ctx, sessionstore.Session{ID: "s1", TenantID: "acme", State: session.StateRunning, CreatedAt: time.Unix(1, 0)})
	for i := 0; i < 5; i++ {
		_ = transcripts.Append(ctx, "acme", "s1", transcriptstore.Entry{Role: "user", Content: "m"})
	}

	// limit=2 yields a cursor + hasMore.
	rr := getMessages(t, h, "/v1/sessions/s1/messages?limit=2", "acme")
	var env messagesEnvelopeWire
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	if len(env.Items) != 2 || !env.HasMore || env.Cursor == "" {
		t.Fatalf("page1 items=%d hasMore=%v cursor=%q", len(env.Items), env.HasMore, env.Cursor)
	}
	if env.Items[0].Seq != 1 || env.Items[1].Seq != 2 {
		t.Errorf("page1 seqs = %d,%d", env.Items[0].Seq, env.Items[1].Seq)
	}

	// ?since=3 skips the first three messages.
	rr = getMessages(t, h, "/v1/sessions/s1/messages?since=3", "acme")
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	if len(env.Items) != 2 || env.Items[0].Seq != 4 || env.Items[1].Seq != 5 {
		t.Errorf("since=3 items=%d seqs=%v", len(env.Items), env.Items)
	}

	// A non-integer ?since is a 400.
	if rr := getMessages(t, h, "/v1/sessions/s1/messages?since=soon", "acme"); rr.Code != http.StatusBadRequest {
		t.Errorf("since=soon status = %d, want 400", rr.Code)
	}
}

// TestMessagesListThreadFilter_spec_15_4_1_1791 verifies the v1 implicit
// single-thread model: a `?threadId=` naming a concrete thread matches no
// node, while an absent filter returns the implicit thread. spec: §15.4.1
// lines 1791, 1796. F-15.1.3.
func TestMessagesListThreadFilter_spec_15_4_1_1791(t *testing.T) {
	h, transcripts, store := seedMessagesServer(t)
	ctx := context.Background()
	_ = store.Create(ctx, sessionstore.Session{ID: "s1", TenantID: "acme", State: session.StateRunning, CreatedAt: time.Unix(1, 0)})
	_ = transcripts.Append(ctx, "acme", "s1", transcriptstore.Entry{Role: "user", Content: "m"})

	rr := getMessages(t, h, "/v1/sessions/s1/messages?threadId=t-other", "acme")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var env messagesEnvelopeWire
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	if len(env.Items) != 0 {
		t.Errorf("threadId=t-other items = %d, want 0 (v1 implicit thread)", len(env.Items))
	}
}

// TestMessagesListNotFound_spec_15_1_661 verifies the §15.1 line 661
// 404 contract: a missing session, a cross-tenant probe, a derive_failure
// audit row, and a gateway with no transcript store all return 404. spec:
// §15.1 line 661. F-15.1.3.
func TestMessagesListNotFound_spec_15_1_661(t *testing.T) {
	h, transcripts, store := seedMessagesServer(t)
	ctx := context.Background()
	_ = store.Create(ctx, sessionstore.Session{ID: "s1", TenantID: "acme", State: session.StateRunning, CreatedAt: time.Unix(1, 0)})
	_ = transcripts.Append(ctx, "acme", "s1", transcriptstore.Entry{Role: "user", Content: "m"})
	// A derive_failure audit row: terminal failed + failureClass.
	_ = store.Create(ctx, sessionstore.Session{
		ID: "df", TenantID: "acme", State: session.StateFailed,
		FailureClass: session.FailureClassDeriveFailure, CreatedAt: time.Unix(2, 0),
	})

	if rr := getMessages(t, h, "/v1/sessions/nope/messages", "acme"); rr.Code != http.StatusNotFound {
		t.Errorf("missing session status = %d, want 404", rr.Code)
	}
	if rr := getMessages(t, h, "/v1/sessions/s1/messages", "globex"); rr.Code != http.StatusNotFound {
		t.Errorf("cross-tenant status = %d, want 404", rr.Code)
	}
	if rr := getMessages(t, h, "/v1/sessions/df/messages", "acme"); rr.Code != http.StatusNotFound {
		t.Errorf("derive_failure status = %d, want 404", rr.Code)
	}

	// No transcript store wired → 404.
	noStore := sessionserver.New(store, sessionserver.Options{})
	if rr := getMessages(t, noStore.Handler(), "/v1/sessions/s1/messages", "acme"); rr.Code != http.StatusNotFound {
		t.Errorf("no-transcript-store status = %d, want 404", rr.Code)
	}
}
