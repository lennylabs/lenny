// SPDX-License-Identifier: MIT

package bindattempt

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// Bind attempt tokens. The adapter compares them for equality and reads
// nothing else from them, so any distinct non-empty strings serve.
const (
	TokenA = "attempt-token-a"
	TokenB = "attempt-token-b"
	// probeToken is a token no case binds under. A Shutdown naming it
	// removes nothing wherever an entry stands, so it reads presence without
	// disturbing the entry: absent where none stands, superseded otherwise.
	probeToken = "attempt-token-probe"
)

// Sessions the cases bind. Each is also the slot identifier.
const (
	alice = "alice"
	bob   = "bob"
)

// Reclaim outcomes a Shutdown response reports.
const (
	reclaimed  = adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED
	absent     = adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_ABSENT
	superseded = adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_SUPERSEDED
)

// callTimeout bounds every call a case makes, so a handler that blocks where
// the contract says it answers fails the case instead of hanging the tier.
const callTimeout = 10 * time.Second

// directLease is a direct-mode lease whose delivery writes the per-slot
// credential file.
func directLease() map[string]*adapterv1.CredentialLease {
	return map[string]*adapterv1.CredentialLease{
		"anthropic_direct": {
			LeaseId:  "lease-1",
			Provider: "anthropic_direct",
			Payload:  []byte(`{"deliveryMode":"direct","materializedConfig":{"apiKey":"sk-ant-x"}}`),
		},
	}
}

func sid(id string) *adapterv1.SessionId { return &adapterv1.SessionId{Value: id} }

// prepareFrame is one PrepareWorkspace frame.
func prepareFrame(id, token string, midSession bool, chunk string) *adapterv1.PrepareWorkspaceRequest {
	return &adapterv1.PrepareWorkspaceRequest{
		SessionId:   sid(id),
		BindAttempt: token,
		MidSession:  midSession,
		UploadRef:   "lenny-blob://acme/upload",
		Chunk:       []byte(chunk),
	}
}

func finalizeReq(id, token string, midSession bool) *adapterv1.FinalizeWorkspaceRequest {
	return &adapterv1.FinalizeWorkspaceRequest{
		SessionId:     sid(id),
		BindAttempt:   token,
		MidSession:    midSession,
		WorkspacePlan: &adapterv1.WorkspacePlan{SchemaVersion: 1},
	}
}

func fencedReq(id, token string) *adapterv1.ShutdownRequest {
	return &adapterv1.ShutdownRequest{SessionId: sid(id), BindAttempt: token}
}

func unconditionalReq(id string) *adapterv1.ShutdownRequest {
	return &adapterv1.ShutdownRequest{SessionId: sid(id), UnconditionalTeardown: true}
}

// request drives one request the admission rules govern for id, carrying
// token where the request carries one.
type request struct {
	name string
	call func(ctx context.Context, p Pod, id, token string) error
}

// tokenedRequests are the bind-sequence requests that carry a non-empty
// bind_attempt when they are not marked mid_session.
func tokenedRequests() []request {
	return []request{
		{"PrepareWorkspace", func(ctx context.Context, p Pod, id, token string) error {
			_, err := p.PrepareWorkspace(ctx, prepareFrame(id, token, false, "x"))
			return err
		}},
		{"FinalizeWorkspace", func(ctx context.Context, p Pod, id, token string) error {
			_, err := p.FinalizeWorkspace(ctx, finalizeReq(id, token, false))
			return err
		}},
		{"RunSetup", func(ctx context.Context, p Pod, id, token string) error {
			_, err := p.RunSetup(ctx, &adapterv1.RunSetupRequest{SessionId: sid(id), BindAttempt: token})
			return err
		}},
		{"AssignCredentials", func(ctx context.Context, p Pod, id, token string) error {
			_, err := p.AssignCredentials(ctx, &adapterv1.AssignCredentialsRequest{SessionId: sid(id), BindAttempt: token})
			return err
		}},
		{"Resume", func(ctx context.Context, p Pod, id, token string) error {
			_, err := p.Resume(ctx, &adapterv1.ResumeRequest{SessionId: sid(id), CheckpointId: "ckpt-1", BindAttempt: token})
			return err
		}},
	}
}

// admissionRequests are every request the admission rules govern: the
// tokened bind-sequence requests and the two starts that carry no token.
func admissionRequests() []request {
	return append(
		tokenedRequests(),
		request{"StartSession", func(ctx context.Context, p Pod, id, _ string) error {
			_, err := p.StartSession(ctx, &adapterv1.StartSessionRequest{SessionId: sid(id), Runtime: "echo"})
			return err
		}},
		request{"ConfigureWorkspace", func(ctx context.Context, p Pod, id, _ string) error {
			_, err := p.ConfigureWorkspace(ctx, &adapterv1.ConfigureWorkspaceRequest{SessionId: sid(id), Cwd: "/workspace"})
			return err
		}},
	)
}

// callCtx returns a context bounded by callTimeout.
func callCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	t.Cleanup(cancel)
	return ctx
}

// assign binds id's credentials under token, creating the entry when none
// stands. withLease delivers a lease, which writes the credential file.
func (f *Fixture) assign(t *testing.T, id, token string, withLease bool) {
	t.Helper()
	req := &adapterv1.AssignCredentialsRequest{SessionId: sid(id), BindAttempt: token}
	if withLease {
		req.Leases = directLease()
	}
	if _, err := f.Pod.AssignCredentials(callCtx(t), req); err != nil {
		t.Fatalf("AssignCredentials(%s): %v", id, err)
	}
}

// start starts id's session with StartSession, which carries no token.
func (f *Fixture) start(t *testing.T, id string) {
	t.Helper()
	if _, err := f.Pod.StartSession(callCtx(t), &adapterv1.StartSessionRequest{SessionId: sid(id), Runtime: "echo"}); err != nil {
		t.Fatalf("StartSession(%s): %v", id, err)
	}
}

// configure starts or repeats id's SDK-warm start.
func (f *Fixture) configure(t *testing.T, id string) error {
	t.Helper()
	_, err := f.Pod.ConfigureWorkspace(callCtx(t), &adapterv1.ConfigureWorkspaceRequest{SessionId: sid(id), Cwd: "/workspace"})
	return err
}

// bindAndStart puts id in the running state under token, with its
// credential file written.
func (f *Fixture) bindAndStart(t *testing.T, id, token string) {
	t.Helper()
	f.assign(t, id, token, true)
	f.start(t, id)
}

// shutdown sends req and fails the case unless it succeeds, because rule 15
// answers every reclaim outcome on a successful call.
func (f *Fixture) shutdown(t *testing.T, req *adapterv1.ShutdownRequest) *adapterv1.ShutdownResponse {
	t.Helper()
	resp, err := f.Pod.Shutdown(callCtx(t), req)
	if err != nil {
		t.Fatalf("Shutdown(%s) = %v, want a successful call carrying the reclaim outcome", req.GetSessionId().GetValue(), err)
	}
	return resp
}

// wantOutcome sends req and fails the case unless it reports want.
func (f *Fixture) wantOutcome(t *testing.T, what string, req *adapterv1.ShutdownRequest, want adapterv1.SlotReclaimOutcome) *adapterv1.ShutdownResponse {
	t.Helper()
	resp := f.shutdown(t, req)
	if got := resp.GetSlotReclaim(); got != want {
		t.Errorf("%s reported %v, want %v", what, got, want)
	}
	return resp
}

// wantNoEntry fails the case unless the adapter holds no entry for id.
func (f *Fixture) wantNoEntry(t *testing.T, id string) {
	t.Helper()
	f.wantOutcome(t, "a probe of "+id+" that must find no entry", fencedReq(id, probeToken), absent)
}

// wantEntry fails the case unless the adapter holds an entry for id. It
// removes nothing.
func (f *Fixture) wantEntry(t *testing.T, id string) {
	t.Helper()
	f.wantOutcome(t, "a probe of "+id+" that must find an entry", fencedReq(id, probeToken), superseded)
}

// wantOwnedBy fails the case unless id's entry carries token, which it shows
// by reclaiming the entry with a Shutdown naming token. It is destructive,
// so a case calls it last.
func (f *Fixture) wantOwnedBy(t *testing.T, id, token string) {
	t.Helper()
	f.wantOutcome(t, "a Shutdown naming the token "+id+"'s entry must carry", fencedReq(id, token), reclaimed)
}

// errorDetail returns the adapterv1.Error a status carries, or nil.
func errorDetail(err error) *adapterv1.Error {
	st, ok := status.FromError(err)
	if !ok {
		return nil
	}
	for _, d := range st.Details() {
		if e, ok := d.(*adapterv1.Error); ok {
			return e
		}
	}
	return nil
}

// wantRefusal fails the case unless err is answered on code and carries the
// typed ErrorCode and category.
func wantRefusal(t *testing.T, what string, err error, code codes.Code, errCode adapterv1.Error_ErrorCode, category adapterv1.Error_Category) {
	t.Helper()
	if status.Code(err) != code {
		t.Errorf("%s = %v, want %v", what, err, code)
		return
	}
	d := errorDetail(err)
	if d.GetCode() != errCode || d.GetCategory() != category {
		t.Errorf("%s carried detail %v/%v, want %v/%v", what, d.GetCode(), d.GetCategory(), errCode, category)
	}
}

// wantSupersededRefusal fails the case unless err is rule 5's refusal.
func wantSupersededRefusal(t *testing.T, what string, err error) {
	t.Helper()
	wantRefusal(t, what, err, codes.Aborted,
		adapterv1.Error_ERROR_CODE_SLOT_BIND_ATTEMPT_SUPERSEDED, adapterv1.Error_CATEGORY_TRANSIENT)
}

// wantStartedRefusal fails the case unless err is rule 6's refusal.
func wantStartedRefusal(t *testing.T, what string, err error) {
	t.Helper()
	wantRefusal(t, what, err, codes.FailedPrecondition,
		adapterv1.Error_ERROR_CODE_SLOT_BIND_ALREADY_STARTED, adapterv1.Error_CATEGORY_PERMANENT)
}
