// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
)

// reconnect re-establishes the §4.7 adapter connection to a pod claimed at
// /create from its persisted binding (§4.6): it resolves the AgentPod by the
// req.AgentPodName recorded on the session row, dials the adapter, and re-runs
// the §15.5 version handshake. Prepare and Launch each call it so no phase
// depends on a connection held open by the phase before it. The caller owns
// cl and closes it on completion or reclaim. spec: §4.6 (proposal),
// §15.5 (version handshake).
func (b *Binder) reconnect(ctx context.Context, req BindRequest) (*lennyv1.AgentPod, *adapterclient.Client, negotiated, error) {
	if req.AgentPodName == "" {
		// Fail closed: a Prepare/Launch with no persisted binding cannot
		// reconnect to the claimed pod, so reject rather than re-claiming a
		// fresh one and orphaning the pod claimed at /create.
		return nil, nil, negotiated{}, fmt.Errorf("podsession: reconnect for session %s has no claimed sandbox binding", req.SessionID)
	}
	return b.dialAndNegotiate(ctx, req.AgentPodName)
}

// dialAndNegotiate resolves the named AgentPod, dials its adapter, and runs the
// §15.5 version handshake, closing the client it opened on a handshake
// failure. It is the connection step shared by the post-claim handshake and
// the reconnect from a persisted binding. spec: §15.5 (version handshake).
func (b *Binder) dialAndNegotiate(ctx context.Context, agentPodName string) (*lennyv1.AgentPod, *adapterclient.Client, negotiated, error) {
	sb, cl, err := b.dialAgentPod(ctx, agentPodName)
	if err != nil {
		return nil, nil, negotiated{}, err
	}
	resp, err := cl.NegotiateVersion(ctx, b.AcceptedVersions)
	if err != nil {
		cl.Close()
		return nil, nil, negotiated{}, fmt.Errorf("podsession: negotiate version with %s: %w", agentPodName, err)
	}
	if resp.GetIncompatible() {
		cl.Close()
		return nil, nil, negotiated{}, fmt.Errorf(
			"podsession: pod %s adapter speaks no protocol version the gateway accepts", agentPodName,
		)
	}
	return sb, cl, negotiated{WorkspaceBase: resp.GetWorkspaceBase()}, nil
}

// dialAgentPod resolves the AgentPod recorded on a session's persisted binding
// (§4.6) and opens the §4.7 adapter connection to it. It runs no handshake and
// no operational RPC: reconnect layers the §15.5 version handshake on top for
// the resume path, and ReadoptConnect returns the bare connection so the
// coordinator-handoff caller can send CoordinatorFence as its first RPC. The
// caller owns cl and closes it on completion or on failure.
func (b *Binder) dialAgentPod(ctx context.Context, agentPodName string) (*lennyv1.AgentPod, *adapterclient.Client, error) {
	sb, err := b.resolveAgentPod(ctx, agentPodName)
	if err != nil {
		return nil, nil, err
	}
	addr := net.JoinHostPort(sb.Status.PodIP, strconv.Itoa(b.AdapterPort))
	cl, err := b.DialAdapter(addr)
	if err != nil {
		return nil, nil, fmt.Errorf("podsession: dial adapter at %s: %w", addr, err)
	}
	return sb, cl, nil
}

// ReadoptConnect re-opens the §4.7 adapter connection to a still-running pod on
// a coordinator handoff, deliberately WITHOUT the §15.5 version handshake that
// reconnect runs. It resolves the AgentPod by the name persisted on the session
// binding, dials the adapter, and returns the live client so the caller can
// send CoordinatorFence as the first RPC over it.
//
// A crash-takeover pod is already in its §10.1 hold state (it lost its prior
// coordinator's connection), and a hold-state pod rejects every inbound RPC
// except CoordinatorFence — the version handshake included. reconnect issues
// NegotiateVersion before any fence, so it cannot re-adopt a hold-state pod;
// ReadoptConnect fences first and negotiates the version only after the fence
// acknowledges. It opens no Attach content stream, so an idle taken-over
// session holds only the gRPC channel and not the §4.7 single content-consumer
// slot. On an empty binding it fails closed rather than dialing nothing. The
// caller owns cl and closes it on a fence failure or at teardown.
//
// spec: §10.1 (CoordinatorFence is the first RPC to a hold-state pod; the pod
// rejects every other inbound RPC), §15.5 (version handshake runs after the
// fence, not before).
func (b *Binder) ReadoptConnect(ctx context.Context, agentPodName string) (*lennyv1.AgentPod, *adapterclient.Client, error) {
	if agentPodName == "" {
		// Fail closed: a handoff with no persisted binding cannot name the
		// pod to re-adopt, so reject rather than dialing an empty address.
		return nil, nil, fmt.Errorf("podsession: readopt has no claimed sandbox binding")
	}
	return b.dialAgentPod(ctx, agentPodName)
}

// resolveAgentPod reads the claimed AgentPod and verifies it carries a pod
// address. The AgentPod reconciler records status.podIP once the pod is
// running, so a pod that was idle when claimed carries an address. The full
// object is returned so the caller can read its coarse §6.2 phase and pod
// address without a re-Get.
func (b *Binder) resolveAgentPod(ctx context.Context, agentPodName string) (*lennyv1.AgentPod, error) {
	var sb lennyv1.AgentPod
	if err := b.Client.Get(ctx, client.ObjectKey{Namespace: b.Namespace, Name: agentPodName}, &sb); err != nil {
		return nil, fmt.Errorf("podsession: get sandbox %s: %w", agentPodName, err)
	}
	if sb.Status.PodIP == "" {
		return nil, fmt.Errorf("podsession: sandbox %s has no pod IP", agentPodName)
	}
	return &sb, nil
}
