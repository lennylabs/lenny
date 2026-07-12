// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §4.7 / §4.9 credential_lifecycle suite
// (TESTING.md §12.4: "Credential assignment → rotation (credentials_rotated
// lifecycle message) → runtime re-bind → emergency revoke → active session
// terminated"). It drives the full direct-delivery credential lifecycle of
// one session against a REAL Full-level runtime process
// (cmd/runtimes/streaming-echo) connected to the production adapter over the
// real §4.7 lifecycle channel (a live Unix socket):
//
//  1. Assignment — the credential-assignment service mints a direct-mode
//     lease from a §4.9 pool and the adapter materializes it into the
//     runtime credential file.
//  2. Rotation + runtime re-bind — the adapter runs the §4.7 Full-level
//     rotation protocol: it sends credentials_rotated on the lifecycle
//     channel, the real streaming-echo runtime rebinds and replies
//     credentials_acknowledged, and the adapter rewrites the credential
//     file to the rotated lease. This is the credentials_rotated /
//     credentials_acknowledged handshake through a real runtime process.
//  3. Emergency revoke → active session terminated — the real §4.9 admin
//     emergency-revocation endpoint revokes the session's active pool
//     credential, terminating its lease and denying the credential. In
//     direct mode the session cannot continue: the §4.9 step-5 replacement
//     mint from the now-exhausted pool fails with pool-exhausted, so the
//     session terminates. Propagation completes within the documented SLO.
//
// The rotation/re-bind spine is the distinctive coverage here: it exercises
// credentials_rotated → runtime re-bind against a real runtime, which the
// component-tier rotation tests exercise only against an in-process fake
// runtime and the sibling credential_revocation test does not exercise at
// all. Cross-replica deny-list propagation over Redis pub/sub is covered by
// credential_pool_revocation_propagation_test.go; this test is single-replica
// and focuses on the credential_lifecycle chain end to end.

package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/credfile"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

const (
	clPool     = "claude-direct-prod"
	clCredID   = "key-1"
	clTenant   = "acme"
	clSession  = "s-credential-lifecycle"
	clProvider = string(credential.ProviderAnthropicDirect)
	// clUpstream is the real direct-mode API key the assigner materializes
	// into the runtime credential file. It must never leak into logs.
	clUpstreamV1 = "sk-ant-lifecycle-v1-secret"
	clUpstreamV2 = "sk-ant-lifecycle-v2-secret"
)

// lifecycleRevoker mirrors cmd/lenny-gateway's poolCredentialRevoker (the
// admin.PoolCredentialRevoker the emergency-revocation endpoint calls): for
// each revoked credential it adds the source-aware pool identity to the
// deny list and drops the leases this replica holds against it, returning
// the count terminated. It is reconstructed here rather than imported
// because the production glue lives in package main. spec: §4.9 lines
// 1640-1652.
type lifecycleRevoker struct {
	denyList *denylist.DenyList
	leases   *credleasestore.Store
}

func (r *lifecycleRevoker) RevokePoolCredentials(_ context.Context, poolID string, credentialIDs []string) int {
	total := 0
	for _, credID := range credentialIDs {
		key := credential.CredentialKey{
			Source:       credential.SourcePool,
			PoolID:       poolID,
			CredentialID: credID,
		}
		r.denyList.Revoke(key)
		for _, lease := range r.leases.LeasesByCredential(key) {
			r.leases.Remove(lease.LeaseID)
			total++
		}
	}
	return total
}

// spec: 4.7 (Full-level credential rotation protocol, spec/04_system-components.md:
//
//	line 723 "credentials_rotated ... New credentials written; runtime must
//	rebind and reply with credentials_acknowledged"; line 728
//	"credentials_acknowledged ... Runtime has rebound to the new credential";
//	line 831 "Full | Gateway calls RotateCredentials RPC; adapter sends
//	credentials_rotated on lifecycle channel; runtime rebinds provider
//	in-place"; line 960 "The file is rewritten by the adapter on credential
//	rotation; after rewrite, the adapter sends credentials_rotated on the
//	lifecycle channel and awaits credentials_acknowledged from the runtime
//	before resuming operation"),
//
// 4.9 (Emergency Credential Revocation, spec/04_system-components.md lines
//
//	1640-1652: revoke marks the credential revoked, terminates its active
//	leases, and denies it; "For direct-delivery mode: the gateway sends a
//	RotateCredentials RPC to all pods holding leases against credId ... The
//	pod's lease is invalidated; it must acquire a new lease from a different
//	credential in the pool (or the fallback chain) to continue"; line 1649 a
//	revoked credential is never re-assignable so the step-5 replacement mint
//	never re-selects it).
//
// diagnosis: the §4.7/§4.9 direct-delivery credential lifecycle diverged
//
//	across its stages. Either the adapter did not run the Full-level rotation
//	handshake against the real runtime (credentials_rotated sent, runtime
//	rebinds, credentials_acknowledged received, credential file rewritten to
//	the rotated lease), or the §4.9 emergency revocation did not terminate the
//	session's active lease and deny the credential so a step-5 replacement
//	mint from the exhausted pool fails and the session terminates. A break in
//	any stage leaves a session running on a stale, unacknowledged, or revoked
//	credential — the exact windows the lifecycle protocol and emergency
//	revocation exist to close.
func TestCredentialLifecycleAssignRotateRebindRevokeTerminate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A real Full-level runtime (streaming-echo) connected to the production
	// adapter over a live lifecycle-channel Unix socket.
	rt := startLifecycleRuntime(t, ctx)
	if !rt.channel.Supports("credential_rotation") {
		t.Fatalf("streaming-echo did not advertise credential_rotation on the lifecycle handshake")
	}

	credDir := t.TempDir()
	adapterSrv := adapter.New("streaming-echo")
	adapterSrv.CredentialsDir = credDir
	adapterSrv.Lifecycle = rt.channel
	adapterSrv.RuntimeName = "streaming-echo"
	adapterSrv.CheckpointPoolLabel = clPool

	// The §4.9 credential-assignment service over a single-credential
	// direct-mode anthropic_direct pool, sharing its lease store with the
	// emergency-revocation revoker.
	leaseStore := credleasestore.New()
	assign := credassign.New(leaseStore, credcache.New())
	assign.RegisterPool(credassign.Pool{
		Name:         clPool,
		Provider:     credential.ProviderAnthropicDirect,
		DeliveryMode: credential.DeliveryDirect,
		Strategy:     credential.StrategyLeastLoaded,
		Credentials: []credassign.PoolCredential{
			{ID: clCredID, APIKey: clUpstreamV1, Healthy: true},
		},
	})

	// ---- stage 1: assignment ----
	// Mint a direct-mode lease and materialize it into the runtime
	// credential file. The runtime now holds v1.
	v1, err := assign.AssignProto(clPool, clSession, "", clTenant)
	if err != nil {
		t.Fatalf("assign direct-mode lease v1: %v", err)
	}
	if _, err := adapterSrv.AssignCredentials(ctx, &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: clSession},
		Leases:    map[string]*adapterv1.CredentialLease{v1.GetProvider(): v1},
	}); err != nil {
		t.Fatalf("AssignCredentials: %v", err)
	}
	if got := credProviderEntry(t, credDir, clProvider); got["leaseId"] != v1.GetLeaseId() {
		t.Fatalf("after assignment: credential file leaseId = %v, want v1 %s", got["leaseId"], v1.GetLeaseId())
	}
	if !credFileCarriesUpstream(t, credDir, clUpstreamV1) {
		t.Fatalf("after assignment: credential file does not carry the materialized direct-mode key")
	}

	// ---- stage 2: rotation + runtime re-bind via credentials_rotated ----
	// Mint a replacement direct-mode lease (proactive renewal against the
	// same credential) and rotate the pod to it. RotateCredentials returns
	// nil only after the real streaming-echo runtime replied
	// credentials_acknowledged for the rotated lease, so a successful call
	// proves the credentials_rotated handshake completed against a live
	// runtime process.
	assign.RegisterPool(credassign.Pool{ // rotate the pool secret to v2.
		Name:         clPool,
		Provider:     credential.ProviderAnthropicDirect,
		DeliveryMode: credential.DeliveryDirect,
		Strategy:     credential.StrategyLeastLoaded,
		Credentials: []credassign.PoolCredential{
			{ID: clCredID, APIKey: clUpstreamV2, Healthy: true},
		},
	})
	v2, err := assign.AssignProto(clPool, clSession, "", clTenant)
	if err != nil {
		t.Fatalf("assign replacement lease v2: %v", err)
	}
	if v2.GetLeaseId() == v1.GetLeaseId() {
		t.Fatalf("replacement lease reused v1's lease id %s; rotation would be unobservable", v1.GetLeaseId())
	}
	rotateCtx, rotateCancel := context.WithTimeout(ctx, 30*time.Second)
	defer rotateCancel()
	if _, err := adapterSrv.RotateCredentials(rotateCtx, &adapterv1.RotateCredentialsRequest{
		SessionId:       &adapterv1.SessionId{Value: clSession},
		Leases:          map[string]*adapterv1.CredentialLease{v2.GetProvider(): v2},
		RotationTrigger: string(credential.TriggerProactiveRenewal),
	}); err != nil {
		t.Fatalf("RotateCredentials (credentials_rotated handshake against real runtime): %v", err)
	}
	if got := credProviderEntry(t, credDir, clProvider); got["leaseId"] != v2.GetLeaseId() {
		t.Fatalf("after rotation: credential file leaseId = %v, want rotated v2 %s", got["leaseId"], v2.GetLeaseId())
	}
	if !credFileCarriesUpstream(t, credDir, clUpstreamV2) {
		t.Fatalf("after rotation: credential file was not rebound to the rotated key")
	}
	// §4.7 line 850 grace period: release the old lease now that the runtime
	// acknowledged the rebind, leaving v2 as the session's sole active lease.
	assign.Release(v1.GetLeaseId())

	// ---- stage 3: emergency revoke → active session terminated ----
	// The real §4.9 admin emergency-revocation endpoint, wired to the deny
	// list and the shared lease store exactly as cmd/lenny-gateway wires
	// poolCredentialRevoker.
	deny := denylist.New()
	poolStore := credentialpoolstore.NewMemory()
	if err := poolStore.Create(ctx, credentialpoolstore.CredentialPool{
		TenantID: clTenant,
		Name:     clPool,
		Provider: clProvider,
		Credentials: []credentialpoolstore.Credential{
			{ID: clCredID, SecretRef: "lenny-system/key-1"},
		},
	}); err != nil {
		t.Fatalf("seed credential pool: %v", err)
	}
	adminRouter := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithCredentialPools(poolStore).
		WithPoolCredentialRevocation(&lifecycleRevoker{denyList: deny, leases: leaseStore})

	// Precondition: the session's active lease v2 is present before revoke.
	if _, ok := leaseStore.GetByID(v2.GetLeaseId()); !ok {
		t.Fatalf("pre-revoke: session lease v2 %s absent from the lease store", v2.GetLeaseId())
	}

	revokeStart := time.Now()
	resp := adminRevoke(t, adminRouter.Handler(),
		"/v1/admin/credential-pools/"+clPool+"/credentials/"+clCredID+"/revoke")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("admin revoke: status %d, body %s", resp.StatusCode, body)
	}
	var summary struct {
		RevokedCredential string `json:"revokedCredential"`
		LeasesTerminated  int    `json:"leasesTerminated"`
		PropagatedAt      string `json:"propagatedAt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		t.Fatalf("decode revoke summary: %v", err)
	}
	resp.Body.Close()
	if summary.RevokedCredential != clCredID {
		t.Errorf("revokedCredential = %q, want %q", summary.RevokedCredential, clCredID)
	}
	// The session held exactly one active lease (v2) against key-1; the §4.9
	// terminator dropped it.
	if summary.LeasesTerminated != 1 {
		t.Errorf("leasesTerminated = %d, want 1 (the active session lease)", summary.LeasesTerminated)
	}
	if summary.PropagatedAt == "" {
		t.Error("revoke summary missing propagatedAt")
	}

	// The session's active lease is terminated and the credential denied.
	if _, ok := leaseStore.GetByID(v2.GetLeaseId()); ok {
		t.Fatalf("post-revoke: session lease v2 %s still present; the active session was not terminated", v2.GetLeaseId())
	}
	credKey := credential.CredentialKey{Source: credential.SourcePool, PoolID: clPool, CredentialID: clCredID}
	if !deny.Revoked(credKey) {
		t.Fatalf("post-revoke: pool credential not on the deny list")
	}

	// §4.9 step 5 (direct mode): the session must acquire a replacement lease
	// from the pool to continue. The revoked credential is never re-assignable
	// (spec line 1649), so with it the pool's only member the replacement mint
	// fails with pool-exhausted and the session terminates.
	assign.RevokeCredential(clPool, clCredID)
	if _, err := assign.Assign(clPool, clSession, "", clTenant); !errors.Is(err, credential.ErrPoolExhausted) {
		t.Fatalf("step-5 replacement mint after revoke: err = %v, want ErrPoolExhausted (session cannot continue)", err)
	}

	// ---- propagation SLO ----
	// The synchronous revoke terminated the lease and denied the credential
	// well inside the documented propagation SLO (§11.4 "within seconds",
	// under the §16 30s CredentialCompromised alert ceiling).
	const propagationSLO = 5 * time.Second
	if elapsed := time.Since(revokeStart); elapsed > propagationSLO {
		t.Errorf("emergency revocation to session termination took %s, over the %s SLO", elapsed, propagationSLO)
	}
}

// lifecycleRuntime is a running streaming-echo process connected to a live
// adapter lifecycle channel.
type lifecycleRuntime struct {
	channel *adapter.LifecycleChannel
}

// startLifecycleRuntime builds and starts cmd/runtimes/streaming-echo
// connected to a fresh adapter LifecycleChannel over a live Unix socket,
// completes the §4.7 lifecycle handshake, and returns the channel. The
// runtime process and channel are torn down on test cleanup.
func startLifecycleRuntime(t *testing.T, ctx context.Context) *lifecycleRuntime {
	t.Helper()
	bin := buildStreamingEchoBinary(t)

	// A short socket path: macOS caps unix socket paths near 104 bytes, so a
	// deep t.TempDir() path can overflow the sockaddr.
	dir, err := os.MkdirTemp("", "lenny-lc-*")
	if err != nil {
		t.Fatalf("temp socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "lifecycle.sock")

	channel, err := adapter.NewLifecycleChannel(sock)
	if err != nil {
		t.Fatalf("NewLifecycleChannel: %v", err)
	}
	runErr := make(chan error, 1)
	go func() { runErr <- channel.Run(ctx) }()
	t.Cleanup(func() {
		_ = channel.Close()
		<-runErr
	})

	// The adapter manifest streaming-echo reads to find the lifecycle socket.
	manifest := filepath.Join(dir, "adapter-manifest.json")
	manifestJSON, err := json.Marshal(map[string]any{
		"lifecycleChannel": map[string]any{"socket": sock},
	})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(manifest, manifestJSON, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	cmd := exec.CommandContext(ctx, bin)
	cmd.Env = append(os.Environ(), "LENNY_ADAPTER_MANIFEST="+manifest)
	// Keep the runtime's stdin open so its stdin/stdout loop blocks instead
	// of hitting EOF and exiting, which would tear down the lifecycle
	// goroutine before the handshake completes.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("streaming-echo stdin pipe: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start streaming-echo: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() && stderr.Len() > 0 {
			t.Logf("streaming-echo stderr:\n%s", stderr.String())
		}
	})

	// WaitHandshake captures the current per-connection ready channel once,
	// so a single call made before the subprocess has dialled waits on the
	// pre-connection channel forever. Retry in short slices until the
	// runtime has connected and handshaked (or the deadline lapses).
	deadline := time.Now().Add(15 * time.Second)
	for !channel.WaitHandshake(ctx, 200*time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("streaming-echo did not complete the lifecycle handshake; stderr:\n%s", stderr.String())
		}
	}
	return &lifecycleRuntime{channel: channel}
}

// credProviderEntry reads the materialized credential file and returns the
// providers-array entry for provider.
func credProviderEntry(t *testing.T, dir, provider string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, credfile.FileName))
	if err != nil {
		t.Fatalf("read credential file: %v", err)
	}
	var doc struct {
		Providers []map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode credential file: %v", err)
	}
	for _, entry := range doc.Providers {
		if name, _ := entry["provider"].(string); name == provider {
			return entry
		}
	}
	t.Fatalf("credential file has no entry for provider %q", provider)
	return nil
}

// credFileCarriesUpstream reports whether the raw credential file contains
// the given upstream secret (the direct-mode materialized key). It reads the
// raw bytes because materializedConfig is an opaque nested object.
func credFileCarriesUpstream(t *testing.T, dir, secret string) bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, credfile.FileName))
	if err != nil {
		t.Fatalf("read credential file: %v", err)
	}
	return bytes.Contains(data, []byte(secret))
}

// adminRevoke issues a POST to the admin revoke path as a tenant admin of
// acme and returns the raw response.
func adminRevoke(t *testing.T, h http.Handler, path string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"reason":"suspected_exfiltration"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject:  "admin@acme.com",
		TenantID: clTenant,
		Roles:    []pkgauth.Role{pkgauth.RoleTenantAdmin},
	}))
	h.ServeHTTP(rec, req)
	return rec.Result()
}
