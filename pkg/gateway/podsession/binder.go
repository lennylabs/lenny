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
	"fmt"
	"net"
	"strconv"

	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
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
}

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
// pod's adapter address, performs the §15.5 version handshake, and
// starts the session via the adapter's StartSession. On success the
// caller owns the returned live adapter connection. Any failure after
// the claim is returned so the gateway can retry on a fresh pod.
func (b *Binder) Bind(ctx context.Context, req BindRequest) (*BindResult, error) {
	sandboxName, podIP, cl, err := b.connect(ctx, req.Pool, req.SessionID, req.TenantID)
	if err != nil {
		return nil, err
	}
	if err := cl.StartSession(ctx, req.SessionID, req.Runtime, req.Plan, req.ExperimentContext, req.TracingContext, req.SetupPolicy); err != nil {
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
	claim, err := claimer.Claim(ctx, podclaim.ClaimRequest{
		Pool:      pool,
		SessionID: sessionID,
		TenantID:  tenantID,
	})
	if err != nil {
		return "", "", nil, err
	}
	sandboxName = claim.Spec.SandboxRef

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
			"podsession: pod %s adapter speaks no protocol version the gateway accepts", sandboxName)
	}
	return sandboxName, podIP, cl, nil
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
