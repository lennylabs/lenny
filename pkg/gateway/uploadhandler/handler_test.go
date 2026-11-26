// SPDX-License-Identifier: MIT

package uploadhandler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/uploadhandler"
	"github.com/lennylabs/lenny/pkg/runtime/runtimestore"
)

// fakeCore stands in for the session core's side of the upload handler's
// seam operations, so these tests drive the handler without a session
// server. Each field sets one operation's answer.
type fakeCore struct {
	session   seam.Session
	lookupErr error
	tokenErr  error
	reserve   seam.StorageReservation
	quotaErr  error
	adjusted  []int64
	forward   seam.ForwardOutcome
	written   int
	writeErr  error
	files     []seam.WorkspaceFile
	audits    []seam.UploadAuditEvent
}

// LookupSession returns the configured session record under the id asked
// for, so a test can drive several sessions through one fake.
func (f *fakeCore) LookupSession(_ context.Context, _, sessionID string) (seam.Session, error) {
	s := f.session
	s.ID = sessionID
	return s, f.lookupErr
}

func (f *fakeCore) AuthorizeSession(context.Context, string, string, seam.SessionAction) (seam.Session, error) {
	return f.session, f.lookupErr
}

func (f *fakeCore) RequestTenant(*http.Request) string { return "acme" }

func (f *fakeCore) ForwardRequest(w http.ResponseWriter, _ *http.Request, _ seam.Session, _ seam.ForwardKind, _ io.Reader) seam.ForwardOutcome {
	if f.forward == seam.ForwardDelivered {
		w.WriteHeader(http.StatusAccepted)
	}
	return f.forward
}

func (f *fakeCore) WriteWorkspace(_ context.Context, _ string, files []seam.WorkspaceFile) (int, error) {
	f.files = files
	return f.written, f.writeErr
}

func (f *fakeCore) VerifyUploadToken(context.Context, string, string, string) error {
	return f.tokenErr
}

func (f *fakeCore) ReserveStorage(context.Context, string, int64) (seam.StorageReservation, error) {
	return f.reserve, f.quotaErr
}

func (f *fakeCore) AdjustStorage(_ context.Context, _ string, delta int64) error {
	f.adjusted = append(f.adjusted, delta)
	return nil
}

func (f *fakeCore) AuditUpload(_ context.Context, ev seam.UploadAuditEvent) {
	f.audits = append(f.audits, ev)
}

// recordingRoutes is a seam.Routes that records each route and refuses the
// route at index refuseAt, when it is not negative.
type recordingRoutes struct {
	routes   []seam.Route
	refuseAt int
}

func (r *recordingRoutes) HandleRoute(rt seam.Route) error {
	if len(r.routes) == r.refuseAt {
		return seam.ErrInvalidRoute
	}
	r.routes = append(r.routes, rt)
	return nil
}

// fakeRuntimes is a runtime registry that declares the mid-session upload
// capability on every runtime.
type fakeRuntimes struct{}

func (fakeRuntimes) Get(_ context.Context, name string) (runtimestore.Runtime, error) {
	return runtimestore.Runtime{Name: name, Capabilities: &runtimestore.RuntimeCapabilities{MidSessionUpload: true}}, nil
}

// newFakeHandler builds a handler over core, with opts' other fields, and
// returns its routes mounted on a mux the way the session server mounts
// them, without the access checks those tests do not exercise.
func newFakeHandler(t *testing.T, core *fakeCore, opts uploadhandler.Options) (*uploadhandler.Handler, http.Handler) {
	t.Helper()
	opts.Sessions, opts.Identity, opts.Forwarding = core, core, core
	opts.Workspaces, opts.Tokens, opts.Quota, opts.Audit = core, core, core, core
	h, err := uploadhandler.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recordingRoutes{refuseAt: -1}
	if err := h.RegisterRoutes(rec); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	for _, r := range rec.routes {
		if r.Listener == seam.ListenerClient {
			mux.Handle(r.MuxPattern(), r.Handler)
		}
	}
	return h, mux
}

// The tests below check the upload handler's own wiring and its mapping of
// the core's answers; they cite the operations they exercise.

// TestNewRefusesOptionsWithoutARequiredOperation checks that the handler
// needs the session lookup, the request identity, the upload token
// verification, and the storage quota, so a wiring that leaves out the token
// or the quota operation fails to build rather than admitting uploads
// unchecked.
//
// spec: GWSEAM-LookupSession, GWSEAM-RequestTenant, GWSEAM-VerifyUploadToken, GWSEAM-ReserveStorage
func TestNewRefusesOptionsWithoutARequiredOperation(t *testing.T) {
	core := &fakeCore{}
	for name, opts := range map[string]uploadhandler.Options{
		"none":        {},
		"no identity": {Sessions: core, Tokens: core, Quota: core},
		"no sessions": {Identity: core, Tokens: core, Quota: core},
		"no tokens":   {Sessions: core, Identity: core, Quota: core},
		"no quota":    {Sessions: core, Identity: core, Tokens: core},
	} {
		if _, err := uploadhandler.New(opts); !errors.Is(err, uploadhandler.ErrIncomplete) {
			t.Errorf("%s: err = %v, want ErrIncomplete", name, err)
		}
	}
}

// TestRegisterRoutesDeclaresEachRouteWithItsAccessCheck checks the routes
// the upload handler registers: the three upload routes behind manage
// access to the path's session and the blob read behind the tenant check,
// all on the client listener, the mid-session upload also on the forward
// listener (decision 34 of change 0117), and that a refused route stops the
// registration with the refusal.
//
// spec: GWSEAM-RegisterRoutes
func TestRegisterRoutesDeclaresEachRouteWithItsAccessCheck(t *testing.T) {
	core := &fakeCore{}
	h, err := uploadhandler.New(uploadhandler.Options{Sessions: core, Identity: core, Tokens: core, Quota: core})
	if err != nil {
		t.Fatal(err)
	}
	rec := &recordingRoutes{refuseAt: -1}
	if err := h.RegisterRoutes(rec); err != nil {
		t.Fatal(err)
	}
	type key struct {
		listener seam.Listener
		route    string
	}
	want := map[key]seam.RouteAccess{
		{seam.ListenerClient, "POST /v1/sessions/{id}/upload"}:             seam.AccessSessionManage,
		{seam.ListenerClient, "POST /v1/sessions/{id}/upload-archive"}:     seam.AccessSessionManage,
		{seam.ListenerClient, "POST /v1/sessions/{id}/upload-to-session"}:  seam.AccessSessionManage,
		{seam.ListenerForward, "POST /v1/sessions/{id}/upload-to-session"}: seam.AccessSessionManage,
		{seam.ListenerClient, "GET /v1/blobs/{ref...}"}:                    seam.AccessTenant,
	}
	if len(rec.routes) != len(want) {
		t.Fatalf("registered %d routes, want %d", len(rec.routes), len(want))
	}
	for _, r := range rec.routes {
		k := key{r.Listener, r.MuxPattern()}
		if access, ok := want[k]; !ok || r.Access != access || r.Validate() != nil {
			t.Errorf("route %v: access %d, valid %v", k, r.Access, r.Validate())
		}
	}

	if err := h.RegisterRoutes(&recordingRoutes{refuseAt: 1}); !errors.Is(err, seam.ErrInvalidRoute) {
		t.Errorf("a refused route: err = %v, want ErrInvalidRoute", err)
	}
}

// fakeMetrics counts the extraction aborts by error type.
type fakeMetrics struct{ aborts map[string]int }

func (*fakeMetrics) AddUploadBytes(int64)    {}
func (*fakeMetrics) SetUploadQueueDepth(int) {}
func (m *fakeMetrics) AddExtractionAbort(errorType string) {
	if m.aborts == nil {
		m.aborts = map[string]int{}
	}
	m.aborts[errorType]++
}

// TestRecordExtractionAbortCountsByErrorType checks that the archive
// extraction abort the core reports is counted by its error type, and that
// a handler without metrics drops it.
//
// spec: GWSEAM-RecordExtractionAbort
func TestRecordExtractionAbortCountsByErrorType(t *testing.T) {
	m := &fakeMetrics{}
	h, _ := newFakeHandler(t, &fakeCore{}, uploadhandler.Options{Metrics: m})
	h.RecordExtractionAbort("symlink")
	h.RecordExtractionAbort("symlink")
	h.RecordExtractionAbort("max_decompressed_size")
	if m.aborts["symlink"] != 2 || m.aborts["max_decompressed_size"] != 1 {
		t.Errorf("aborts = %v", m.aborts)
	}
	bare, _ := newFakeHandler(t, &fakeCore{}, uploadhandler.Options{})
	bare.RecordExtractionAbort("symlink")
}

// TestCloseUploadsClosesTheSessionsUploadChannel checks that once the core
// closes a session's upload window, a later upload to the session aborts
// with UPLOAD_CHANNEL_CLOSED and stores nothing, while another session's
// window stays open.
//
// spec: GWSEAM-CloseUploads; §7.4.
func TestCloseUploadsClosesTheSessionsUploadChannel(t *testing.T) {
	core := &fakeCore{session: seam.Session{ID: "sess-a", TenantID: "acme", State: "created"}}
	h, mux := newFakeHandler(t, core, uploadhandler.Options{Blobs: blobstore.NewMemoryStore(nil)})
	post := func(id string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+id+"/upload", strings.NewReader("abc")))
		return rr
	}
	if rr := post("sess-a"); rr.Code != http.StatusCreated {
		t.Fatalf("upload before the window closed = %d, want 201", rr.Code)
	}
	h.CloseUploads("sess-a")
	if rr := post("sess-a"); rr.Code != http.StatusGone || errorCode(t, rr) != "UPLOAD_CHANNEL_CLOSED" {
		t.Fatalf("upload after the window closed = %d %s, want 410 UPLOAD_CHANNEL_CLOSED", rr.Code, rr.Body.String())
	}
	if rr := post("sess-b"); rr.Code != http.StatusCreated {
		t.Fatalf("upload to another session = %d, want 201", rr.Code)
	}
}

// TestUploadMapsEachTokenRefusalToItsEnvelope checks the envelope of each
// upload token refusal the core reports, and that a failure to read the
// token's consumed state, which matches no refusal, fails the upload closed
// with a server error.
//
// spec: GWSEAM-VerifyUploadToken; §7.1.
func TestUploadMapsEachTokenRefusalToItsEnvelope(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{seam.ErrUploadTokenExpired, http.StatusUnauthorized, "UPLOAD_TOKEN_EXPIRED"},
		{seam.ErrUploadTokenMismatch, http.StatusForbidden, "UPLOAD_TOKEN_MISMATCH"},
		{seam.ErrUploadTokenConsumed, http.StatusGone, "UPLOAD_TOKEN_CONSUMED"},
		{seam.ErrUploadTokenInvalid, http.StatusUnauthorized, "UPLOAD_TOKEN_INVALID"},
		{errors.New("read upload token state: store unreachable"), http.StatusInternalServerError, "INTERNAL_ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			core := &fakeCore{session: seam.Session{ID: "sess-a", State: "created"}, tokenErr: tc.err}
			_, mux := newFakeHandler(t, core, uploadhandler.Options{Blobs: blobstore.NewMemoryStore(nil)})
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/sessions/sess-a/upload", strings.NewReader("x")))
			if rr.Code != tc.status || errorCode(t, rr) != tc.code {
				t.Fatalf("answer = %d %s, want %d %s", rr.Code, rr.Body.String(), tc.status, tc.code)
			}
		})
	}
}

// TestUploadMapsEachQuotaRefusalToItsEnvelope checks the envelope of each
// storage quota outcome the core reports, and that a failed store write
// releases the whole reservation.
//
// spec: GWSEAM-ReserveStorage, GWSEAM-AdjustStorage; §11.2; §12.4.
func TestUploadMapsEachQuotaRefusalToItsEnvelope(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"over the quota", &seam.StorageQuotaError{CurrentBytes: 9, LimitBytes: 10}, http.StatusTooManyRequests, "STORAGE_QUOTA_EXCEEDED"},
		{"no declared size", seam.ErrContentLengthRequired, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"unreadable quota", seam.ErrStorageQuotaUnavailable, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE"},
		{"store failure", errors.New("storage-quota check failed: boom"), http.StatusInternalServerError, "INTERNAL_ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			core := &fakeCore{session: seam.Session{ID: "sess-a", State: "created"}, quotaErr: tc.err}
			_, mux := newFakeHandler(t, core, uploadhandler.Options{Blobs: blobstore.NewMemoryStore(nil)})
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/sessions/sess-a/upload", strings.NewReader("x")))
			if rr.Code != tc.status || errorCode(t, rr) != tc.code {
				t.Fatalf("answer = %d %s, want %d %s", rr.Code, rr.Body.String(), tc.status, tc.code)
			}
		})
	}

	core := &fakeCore{session: seam.Session{ID: "sess-a", State: "created"}, reserve: seam.StorageReservation{Held: true, Reserved: 1, Headroom: 10}}
	_, mux := newFakeHandler(t, core, uploadhandler.Options{Blobs: failingBlobs{}})
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/sessions/sess-a/upload", strings.NewReader("x")))
	if rr.Code != http.StatusInternalServerError || len(core.adjusted) != 1 || core.adjusted[0] != -1 {
		t.Errorf("failed store write: %d, adjustments %v; want 500 and the reservation released", rr.Code, core.adjusted)
	}
}

// failingBlobs is a blob store whose writes fail with a downstream error.
type failingBlobs struct{ blobstore.Store }

func (failingBlobs) Put(blobstore.URI, string, io.Reader) (string, error) {
	return "", errors.New("minio down")
}

// TestMidSessionUploadMapsTheCoresAnswers checks how the mid-session upload
// answers each outcome of the core's forwarding and workspace write: a
// delivered forward is the holder's answer, a missing binding or workspace
// write operation is TARGET_NOT_READY, a failure that names no phase is
// answered and audited as a staging failure, and a write carries each
// file's path, content, and trimmed mode.
//
// spec: GWSEAM-ForwardRequest, GWSEAM-WriteWorkspace; §7.4.
func TestMidSessionUploadMapsTheCoresAnswers(t *testing.T) {
	running := seam.Session{ID: "sess-a", TenantID: "acme", RuntimeRef: "rt", State: "running"}
	body := `{"files":[{"path":"a.txt","content":"eA==","mode":" 0600 "}]}`
	post := func(mux http.Handler) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/sessions/sess-a/upload-to-session", bytes.NewBufferString(body)))
		return rr
	}
	opts := uploadhandler.Options{Runtimes: fakeRuntimes{}, MidSessionUploadEnabled: true}

	delivered := &fakeCore{session: running, forward: seam.ForwardDelivered}
	_, mux := newFakeHandler(t, delivered, opts)
	if rr := post(mux); rr.Code != http.StatusAccepted || delivered.files != nil {
		t.Errorf("delivered forward: %d, files %v; want the holder's 202 and no local write", rr.Code, delivered.files)
	}

	unbound := &fakeCore{session: running, forward: seam.ForwardLocal, writeErr: seam.ErrNoLiveBinding}
	_, mux = newFakeHandler(t, unbound, opts)
	if rr := post(mux); rr.Code != http.StatusConflict || errorCode(t, rr) != "TARGET_NOT_READY" {
		t.Errorf("no binding: %d %s, want 409 TARGET_NOT_READY", rr.Code, rr.Body.String())
	}

	core := &fakeCore{session: running, forward: seam.ForwardLocal}
	opts.Sessions, opts.Identity, opts.Forwarding = core, core, core
	opts.Tokens, opts.Quota = core, core
	noWrite, err := uploadhandler.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recordingRoutes{refuseAt: -1}
	if err := noWrite.RegisterRoutes(rec); err != nil {
		t.Fatal(err)
	}
	bare := http.NewServeMux()
	for _, r := range rec.routes {
		if r.Listener == seam.ListenerClient {
			bare.Handle(r.MuxPattern(), r.Handler)
		}
	}
	if rr := post(bare); rr.Code != http.StatusConflict {
		t.Errorf("no workspace write operation: %d, want 409", rr.Code)
	}
	opts.Sessions, opts.Identity, opts.Forwarding, opts.Tokens, opts.Quota = nil, nil, nil, nil, nil

	broken := &fakeCore{session: running, forward: seam.ForwardLocal, writeErr: errors.New("unexpected")}
	_, mux = newFakeHandler(t, broken, opts)
	if rr := post(mux); rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), "failed to stage mid-session upload onto the pod: unexpected") {
		t.Errorf("write failure naming no phase: %d %s, want 502 naming staging", rr.Code, rr.Body.String())
	}
	if len(broken.audits) != 1 || broken.audits[0].Outcome != seam.UploadRejected || broken.audits[0].Reason != "stage_failed" {
		t.Errorf("audits = %+v, want one rejected stage_failed", broken.audits)
	}

	ok := &fakeCore{session: running, forward: seam.ForwardLocal, written: 1}
	_, mux = newFakeHandler(t, ok, opts)
	if rr := post(mux); rr.Code != http.StatusOK {
		t.Fatalf("write: %d %s, want 200", rr.Code, rr.Body.String())
	}
	if len(ok.files) != 1 || ok.files[0].Path != "a.txt" || string(ok.files[0].Content) != "x" || ok.files[0].Mode != "0600" {
		t.Errorf("files = %+v", ok.files)
	}
	if len(ok.audits) != 1 || ok.audits[0].Outcome != seam.UploadAccepted || ok.audits[0].Detail != "1" {
		t.Errorf("audits = %+v, want one accepted with the file count", ok.audits)
	}
}

// TestRegisterFlagsReadsEachDefaultFromItsEnvironmentVariable checks that
// the upload handler's flags take their defaults from their environment
// variables and that the command line overrides them.
//
// The flags are plumbing that decides no platform behavior, so the test
// carries no specification annotation.
func TestRegisterFlagsReadsEachDefaultFromItsEnvironmentVariable(t *testing.T) {
	t.Setenv("LENNY_UPLOAD_MAX_CONCURRENT_PER_SESSION", "3")
	t.Setenv("LENNY_UPLOAD_MAX_CONCURRENT_GLOBAL", "40")
	t.Setenv("LENNY_UPLOAD_MAX_BYTES_PER_SESSION", "68719476736")
	t.Setenv("LENNY_MID_SESSION_UPLOAD", "yes")
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	f := uploadhandler.RegisterFlags(fs)
	if err := fs.Parse([]string{"-upload-max-concurrent-global=50"}); err != nil {
		t.Fatal(err)
	}
	if *f.MaxConcurrentPerSession != 3 || *f.MaxConcurrentGlobal != 50 || *f.MaxBytesPerSession != 68719476736 || !*f.MidSessionUpload {
		t.Errorf("flags = %d %d %d %v", *f.MaxConcurrentPerSession, *f.MaxConcurrentGlobal, *f.MaxBytesPerSession, *f.MidSessionUpload)
	}

	t.Setenv("LENNY_MID_SESSION_UPLOAD", "")
	off := uploadhandler.RegisterFlags(flag.NewFlagSet("t", flag.ContinueOnError))
	if *off.MidSessionUpload {
		t.Error("mid-session upload defaults on with its variable unset")
	}
}

// errorCode returns the code of the client API error envelope in rr.
func errorCode(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v; body=%s", err, rr.Body.String())
	}
	return env.Error.Code
}
