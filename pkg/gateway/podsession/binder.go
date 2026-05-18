// SPDX-License-Identifier: MIT

// Package podsession is the gateway-side path that places a session on
// a warm pod. Bind claims an idle Sandbox (§4.6.1), resolves the bound
// pod's adapter address, performs the §15.5 version handshake, and
// starts the session on the pod's §4.7 adapter. It joins the pod-claim
// path, the adapter client, and the recorded pod address into the
// single operation the gateway's session-creation handler invokes.
package podsession

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	"github.com/lennylabs/lenny/pkg/agentpodstate"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/gitref"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// Binder places sessions on warm pods.
type Binder struct {
	// Client addresses the cluster: it backs the pod claim and the
	// Sandbox lookup that resolves the pod address.
	Client client.Client
	// Namespace is the agent namespace the pools and Sandboxes live in.
	Namespace string
	// AdapterPort is the TCP port a pod's §4.7 adapter listens on.
	AdapterPort int
	// AcceptedVersions are the adapter protocol versions the gateway
	// speaks, highest preference first (§15.5).
	AcceptedVersions []string
	// DialAdapter opens an adapter client for the pod reachable at addr.
	// Production dials over mTLS; tests substitute an in-memory link.
	DialAdapter func(addr string) (*adapterclient.Client, error)
	// Blobs resolves §4.5 lenny-blob:// upload refs to their content so
	// Bind can stage a plan's uploadFile and uploadArchive sources via
	// the adapter's PrepareWorkspace RPC. Nil when the deployment has no
	// blob store configured; a plan carrying upload sources then fails
	// to bind.
	Blobs blobstore.Store
	// Fallback is the §4.6.1 Postgres-backed agent_pod_state mirror. When
	// the Kubernetes-API claim returns podclaim.ErrNoIdlePod and Fallback
	// is non-nil, connect attempts a fallback claim against the mirror
	// before surfacing the no-idle-pod error. Nil when the deployment has
	// no Postgres configured; connect then returns ErrNoIdlePod directly.
	Fallback agentpodstate.Store
	// FallbackMaxMirrorLagSeconds is the §4.6.1
	// podClaimFallbackMaxMirrorLagSeconds freshness precondition: the
	// fallback runs only when the target pool's mirror lag is at or below
	// this many seconds. Above it the mirror may still show pods already
	// claimed in etcd but not yet mirrored, so connect skips the fallback
	// and returns ErrNoIdlePod. A zero value selects the default of
	// DefaultFallbackMaxMirrorLagSeconds.
	FallbackMaxMirrorLagSeconds float64
}

// DefaultFallbackMaxMirrorLagSeconds is the §4.6.1
// podClaimFallbackMaxMirrorLagSeconds default: the fallback claim runs
// only when the mirror is no more than this many seconds stale.
const DefaultFallbackMaxMirrorLagSeconds = 10

// BindRequest describes a session to place on a warm pod.
type BindRequest struct {
	// Pool is the SandboxWarmPool to claim a pod from.
	Pool string
	// SessionID is the §15.1 session being started.
	SessionID string
	// TenantID is the tenant that owns the session.
	TenantID string
	// Runtime is the runtime name passed to the adapter's StartSession.
	Runtime string
	// Plan is the workspace the adapter materializes before start.
	Plan *adapterv1.WorkspacePlan
	// ExperimentContext is the §8.3 / §10.7 experiment enrollment
	// delivered to the runtime in the adapter manifest. Nil for an
	// unenrolled session.
	ExperimentContext *adapterv1.ExperimentContext
	// TracingContext is the §8.3 opaque tracing-identifier map delivered
	// to the runtime in the adapter manifest. Nil when none is set.
	TracingContext map[string]string
	// SetupPolicy is the §5.1 runtime setupPolicy bounding the setup
	// phase. Nil when the runtime declares no aggregate cap.
	SetupPolicy *adapterv1.SetupPolicy
}

// BindResult reports the pod a session was bound to.
type BindResult struct {
	// SessionID is the session the pod was claimed for.
	SessionID string
	// TenantID is the tenant that owns the session.
	TenantID string
	// SandboxName is the claimed Sandbox.
	SandboxName string
	// PodIP is the bound pod's address.
	PodIP string
	// Adapter is the live connection to the pod's adapter. The caller
	// owns it and closes it when the session ends.
	Adapter *adapterclient.Client
}

// ResumeRequest describes a session to restore onto a fresh warm pod.
type ResumeRequest struct {
	// Pool is the SandboxWarmPool to claim a pod from.
	Pool string
	// SessionID is the §7.1 session being resumed.
	SessionID string
	// TenantID is the tenant that owns the session.
	TenantID string
	// Runtime is the runtime name passed to the adapter's Resume.
	Runtime string
	// CheckpointID is the §4.4 checkpoint the workspace is restored
	// from.
	CheckpointID string
	// ExperimentContext and TracingContext are re-delivered to the
	// restored runtime in the adapter manifest. Nil when unset.
	ExperimentContext *adapterv1.ExperimentContext
	TracingContext    map[string]string
}

// Bind claims an idle pod for the request's session, resolves the
// pod's adapter address, performs the §15.5 version handshake, and runs
// the §4.7 session-assignment sequence on the pod's adapter:
// PrepareWorkspace stages uploaded files and cloned repositories,
// FinalizeWorkspace materializes the workspace, RunSetup runs the
// plan's setup commands, and StartSession starts the runtime. On
// success the caller owns the
// returned live adapter connection. Any failure after the claim is
// returned so the gateway can retry on a fresh pod.
func (b *Binder) Bind(ctx context.Context, req BindRequest) (*BindResult, error) {
	sandboxName, podIP, cl, err := b.connect(ctx, req.Pool, req.SessionID, req.TenantID)
	if err != nil {
		return nil, err
	}
	if err := b.stageWorkspace(ctx, cl, req.SessionID, req.Plan); err != nil {
		cl.Close()
		return nil, fmt.Errorf("podsession: stage workspace on pod %s: %w", sandboxName, err)
	}
	if err := cl.FinalizeWorkspace(ctx, req.SessionID, req.Plan); err != nil {
		cl.Close()
		return nil, fmt.Errorf("podsession: finalize workspace on pod %s: %w", sandboxName, err)
	}
	if err := cl.RunSetup(ctx, req.SessionID, req.Plan.GetSetupCommands(), req.SetupPolicy); err != nil {
		cl.Close()
		return nil, fmt.Errorf("podsession: run setup on pod %s: %w", sandboxName, err)
	}
	if err := cl.StartSession(ctx, req.SessionID, req.Runtime, req.ExperimentContext, req.TracingContext); err != nil {
		cl.Close()
		return nil, fmt.Errorf("podsession: start session on pod %s: %w", sandboxName, err)
	}
	return &BindResult{
		SessionID:   req.SessionID,
		TenantID:    req.TenantID,
		SandboxName: sandboxName,
		PodIP:       podIP,
		Adapter:     cl,
	}, nil
}

// stageWorkspace prepares the pod's staging area for the plan's
// non-filesystem-native sources, ahead of FinalizeWorkspace. It fetches
// the blob content of every uploadFile and uploadArchive source from
// the §4.5 blob store, clones every gitClone source on the gateway's
// network path and archives the tree, and streams all of it to the pod
// via PrepareWorkspace. It is a no-op when the plan has no such
// sources. A plan that carries upload sources but binds through a
// Binder with no blob store fails rather than materializing an
// incomplete workspace.
func (b *Binder) stageWorkspace(ctx context.Context, cl *adapterclient.Client, sessionID string, plan *adapterv1.WorkspacePlan) error {
	uploads := make(map[string][]byte)

	if refs := uploadRefs(plan); len(refs) > 0 {
		if b.Blobs == nil {
			return fmt.Errorf("plan has %d upload source(s) but the binder has no blob store", len(refs))
		}
		for _, ref := range refs {
			uri, err := blobstore.ParseURI(ref)
			if err != nil {
				return fmt.Errorf("parse upload ref %q: %w", ref, err)
			}
			_, rc, err := b.Blobs.Get(uri)
			if err != nil {
				return fmt.Errorf("fetch upload %q: %w", ref, err)
			}
			content, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				return fmt.Errorf("read upload %q: %w", ref, err)
			}
			uploads[ref] = content
		}
	}

	for _, src := range plan.GetSources() {
		if src.GetType() != "gitClone" {
			continue
		}
		// §14: an authenticated clone needs the §4.9 VCS credential-lease
		// token, which is not yet wired. Public clones proceed.
		if mode := src.GetAuth().GetMode(); mode != "" {
			return fmt.Errorf("gitClone of %q uses auth.mode=%q; the §4.9 VCS credential-lease path is not yet wired",
				src.GetUrl(), mode)
		}
		archive, err := gitref.CloneArchive(ctx, src.GetUrl(), src.GetResolvedCommitSha(),
			gitref.CloneOptions{Depth: int(src.GetDepth()), Submodules: src.GetSubmodules()})
		if err != nil {
			return fmt.Errorf("clone %q: %w", src.GetUrl(), err)
		}
		uploads[workspace.GitCloneStagingRef(src)] = archive
	}

	if len(uploads) == 0 {
		return nil
	}
	_, err := cl.PrepareWorkspace(ctx, sessionID, uploads)
	return err
}

// uploadRefs collects the distinct uploadRef values of the plan's
// uploadFile and uploadArchive sources, in first-seen order.
func uploadRefs(plan *adapterv1.WorkspacePlan) []string {
	seen := make(map[string]bool)
	var refs []string
	for _, src := range plan.GetSources() {
		switch src.GetType() {
		case "uploadFile", "uploadArchive":
			if ref := src.GetUploadRef(); ref != "" && !seen[ref] {
				seen[ref] = true
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

// Resume claims an idle pod for the request's session and restores the
// session's workspace onto it from the named §4.4 checkpoint via the
// adapter's Resume RPC. It is the §7.1 resume counterpart of Bind: used
// when a suspended session's original pod was released and the session
// must be rebuilt on a replacement pod. Any failure after the claim is
// returned so the gateway can retry on a fresh pod.
func (b *Binder) Resume(ctx context.Context, req ResumeRequest) (*BindResult, error) {
	sandboxName, podIP, cl, err := b.connect(ctx, req.Pool, req.SessionID, req.TenantID)
	if err != nil {
		return nil, err
	}
	if _, err := cl.Resume(ctx, req.SessionID, req.Runtime, req.CheckpointID, req.ExperimentContext, req.TracingContext); err != nil {
		cl.Close()
		return nil, fmt.Errorf("podsession: resume session on pod %s: %w", sandboxName, err)
	}
	return &BindResult{
		SessionID:   req.SessionID,
		TenantID:    req.TenantID,
		SandboxName: sandboxName,
		PodIP:       podIP,
		Adapter:     cl,
	}, nil
}

// connect claims an idle pod from the pool, resolves the pod's adapter
// address, dials it, and runs the §15.5 version handshake. On success
// the caller owns cl and must close it once the session ends or on any
// later failure. The shared claim-and-handshake path of Bind and Resume.
func (b *Binder) connect(ctx context.Context, pool, sessionID, tenantID string) (sandboxName, podIP string, cl *adapterclient.Client, err error) {
	claimer := &podclaim.Claimer{Client: b.Client, Namespace: b.Namespace}
	req := podclaim.ClaimRequest{
		Pool:      pool,
		SessionID: sessionID,
		TenantID:  tenantID,
	}
	claim, err := claimer.Claim(ctx, req)
	if errors.Is(err, podclaim.ErrNoIdlePod) {
		// The Kubernetes-API claim found no idle pod. Attempt the §4.6.1
		// Postgres-backed fallback claim before surfacing the error.
		sandboxName, err = b.fallbackClaim(ctx, req)
		if err != nil {
			return "", "", nil, err
		}
	} else if err != nil {
		return "", "", nil, err
	} else {
		sandboxName = claim.Spec.SandboxRef
	}

	podIP, err = b.resolvePodIP(ctx, sandboxName)
	if err != nil {
		return "", "", nil, err
	}

	addr := net.JoinHostPort(podIP, strconv.Itoa(b.AdapterPort))
	cl, err = b.DialAdapter(addr)
	if err != nil {
		return "", "", nil, fmt.Errorf("podsession: dial adapter at %s: %w", addr, err)
	}

	resp, err := cl.NegotiateVersion(ctx, b.AcceptedVersions)
	if err != nil {
		cl.Close()
		return "", "", nil, fmt.Errorf("podsession: negotiate version with %s: %w", sandboxName, err)
	}
	if resp.GetIncompatible() {
		cl.Close()
		return "", "", nil, fmt.Errorf(
			"podsession: pod %s adapter speaks no protocol version the gateway accepts", sandboxName,
		)
	}
	return sandboxName, podIP, cl, nil
}

// fallbackClaim runs the §4.6.1 Postgres-backed fallback claim after
// the Kubernetes-API claim returned podclaim.ErrNoIdlePod. It returns
// the claimed Sandbox name, or podclaim.ErrNoIdlePod when the fallback
// is disabled, the mirror is too stale to trust, or the mirror also
// has no idle pod (the warm pool is genuinely exhausted, which the
// caller surfaces as WARM_POOL_EXHAUSTED).
//
// The fallback claims an agent_pod_state row, then reproduces the
// authoritative side of a claim: it creates the binding SandboxClaim
// CRD (so the lenny-sandboxclaim-guard webhook's CREATE-time check
// still guards against a double-claim) and best-effort flips the
// Sandbox CRD phase idle → claimed, tolerating a conflict the same way
// podclaim.Claimer.Claim does.
func (b *Binder) fallbackClaim(ctx context.Context, req podclaim.ClaimRequest) (string, error) {
	if b.Fallback == nil {
		// No Postgres mirror is configured; the no-idle-pod result stands.
		return "", podclaim.ErrNoIdlePod
	}

	// Freshness precondition: above podClaimFallbackMaxMirrorLagSeconds
	// the mirror may still show pods already claimed in etcd but not yet
	// mirrored, so a fallback claim would race the Kubernetes-API claim.
	maxLag := b.FallbackMaxMirrorLagSeconds
	if maxLag == 0 {
		maxLag = DefaultFallbackMaxMirrorLagSeconds
	}
	lag, err := b.Fallback.MirrorLagSeconds(ctx, req.Pool)
	if err != nil {
		return "", fmt.Errorf("podsession: read mirror lag for pool %s: %w", req.Pool, err)
	}
	if lag > maxLag {
		// The mirror is too stale to trust; defer to the no-idle-pod
		// result rather than risk claiming an already-claimed pod.
		return "", podclaim.ErrNoIdlePod
	}

	pod, claimed, err := b.Fallback.ClaimIdle(ctx, req.Pool, req.SessionID, req.TenantID)
	if err != nil {
		return "", fmt.Errorf("podsession: fallback claim from pool %s: %w", req.Pool, err)
	}
	if !claimed {
		// The mirror also has no idle pod: the warm pool is exhausted.
		return "", podclaim.ErrNoIdlePod
	}

	// Reproduce the authoritative side of a claim. Create the binding
	// SandboxClaim first: the lenny-sandboxclaim-guard webhook rejects
	// the CREATE if the pod is already claimed, which backstops the
	// §4.6.1 single-claim invariant for the fallback path.
	if _, err := podclaim.CreateClaim(ctx, b.Client, b.Namespace, pod.PodID, req); err != nil {
		return "", err
	}

	// Best-effort flip the Sandbox CRD phase idle → claimed, the
	// authoritative state. A conflict means a competing writer already
	// advanced the pod; the SandboxClaim above still binds this session,
	// so the claim holds and the conflict is tolerated.
	var sb lennyv1.Sandbox
	if err := b.Client.Get(ctx, client.ObjectKey{Namespace: b.Namespace, Name: pod.PodID}, &sb); err != nil {
		return "", fmt.Errorf("podsession: get sandbox %s for fallback claim: %w", pod.PodID, err)
	}
	if sb.Status.Phase == string(state.Idle) {
		sb.Status.Phase = string(state.Claimed)
		if err := b.Client.Status().Update(ctx, &sb); err != nil && !apierrors.IsConflict(err) {
			return "", fmt.Errorf("podsession: claim sandbox %s in fallback: %w", pod.PodID, err)
		}
	}
	return pod.PodID, nil
}

// Release tears down a session that Bind placed on a pod: it shuts the
// pod's runtime down through the adapter, closes the adapter
// connection, and transitions the Sandbox to draining so the Sandbox
// reconciler reclaims the pod (§6.2 claimed → draining → terminated).
// The adapter Shutdown is best-effort — draining the Sandbox reclaims
// the pod even if the runtime did not exit cleanly — so Release returns
// only an error from the Sandbox transition.
func (b *Binder) Release(ctx context.Context, result *BindResult) error {
	if result.Adapter != nil {
		_, _ = result.Adapter.Shutdown(ctx, result.SessionID)
		result.Adapter.Close()
	}

	var sb lennyv1.Sandbox
	if err := b.Client.Get(ctx, client.ObjectKey{Namespace: b.Namespace, Name: result.SandboxName}, &sb); err != nil {
		return fmt.Errorf("podsession: get sandbox %s: %w", result.SandboxName, err)
	}
	sb.Status.Phase = string(state.Draining)
	if err := b.Client.Status().Update(ctx, &sb); err != nil {
		return fmt.Errorf("podsession: drain sandbox %s: %w", result.SandboxName, err)
	}
	return nil
}

// resolvePodIP reads the claimed Sandbox and returns its pod address.
// The Sandbox reconciler records status.podIP once the pod is running,
// so a pod that was idle when claimed carries an address.
func (b *Binder) resolvePodIP(ctx context.Context, sandboxName string) (string, error) {
	var sb lennyv1.Sandbox
	if err := b.Client.Get(ctx, client.ObjectKey{Namespace: b.Namespace, Name: sandboxName}, &sb); err != nil {
		return "", fmt.Errorf("podsession: get sandbox %s: %w", sandboxName, err)
	}
	if sb.Status.PodIP == "" {
		return "", fmt.Errorf("podsession: sandbox %s has no pod IP", sandboxName)
	}
	return sb.Status.PodIP, nil
}
