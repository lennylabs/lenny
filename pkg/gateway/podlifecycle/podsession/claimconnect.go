// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// HoldCanceller cancels a §6.2 reserved-hold expiry timer this replica holds
// for the pod's claim. *recycle.HoldCoordinator satisfies it through Cancel;
// the interface is defined at this consumer so podsession does not import the
// recycle package. spec: §6.2 (within-hold rebind cancels the local timer).
type HoldCanceller interface {
	Cancel(podID string)
}

// negotiated bundles the handshake-reported metadata the caller needs
// to capture from connect. It carries the §15.5 NegotiateVersion
// response fields the gateway threads onto BindResult so downstream
// users (session-row persistence, Resume-time assertion) can see them.
type negotiated struct {
	// WorkspaceBase is the §6.4 base the adapter nests every session's
	// per-slot tree under. A session's §7.3 cwd is derived from it and the
	// session identifier. Empty when the adapter reports none. F-7.3.15.
	WorkspaceBase string
}

// connect claims an idle pod from the pool, resolves the claimed Sandbox,
// dials its adapter, and runs the §15.5 version handshake. On success the
// caller owns cl and must close it once the session ends or on any later
// failure, and owns the returned Sandbox object for chaining §6.2 phase
// transitions. The shared claim-and-handshake path of Bind and Resume.
// The negotiated return value carries the handshake-reported metadata
// (workspace root, etc.) the caller threads onto BindResult. req carries the
// pool, session, tenant, and the resolved pool's KeepsRuntime.
func (b *Binder) connect(ctx context.Context, req podclaim.ClaimRequest) (sb *lennyv1.Sandbox, cl *adapterclient.Client, neg negotiated, err error) {
	pool := req.Pool
	claimer := &podclaim.Claimer{
		Client:    b.Client,
		Namespace: b.Namespace,
		Now:       b.Now,
		// On a §4.6.1 acquisition-path rebind, cancel the holding replica's local
		// hold-TTL timer so it does not issue a wasted no-op expiry DELETE.
		OnRebind: func(podID string) {
			if b.HoldCanceller != nil {
				b.HoldCanceller.Cancel(podID)
			}
		},
	}
	var sandboxName string
	claim, err := claimer.Claim(ctx, req)
	if errors.Is(err, podclaim.ErrNoIdlePod) {
		// The Kubernetes-API claim found no idle pod. Attempt the §4.6.1
		// Postgres-backed fallback claim before surfacing the error, skipping
		// the pods the idle scan refused on the tenant pin (§5.2). A failed
		// pin read is not ErrNoIdlePod and starts no fallback.
		var noIdle *podclaim.NoIdlePodError
		var refused []string
		if errors.As(err, &noIdle) {
			refused = noIdle.Refused
		}
		sandboxName, err = b.fallbackClaim(ctx, req, refused)
		if err != nil {
			return nil, nil, negotiated{}, err
		}
	} else if err != nil {
		return nil, nil, negotiated{}, err
	} else {
		sandboxName = claim.Spec.SandboxRef
	}

	sb, err = b.resolveSandbox(ctx, sandboxName)
	if err != nil {
		return nil, nil, negotiated{}, err
	}

	addr := net.JoinHostPort(sb.Status.PodIP, strconv.Itoa(b.AdapterPort))
	cl, err = b.DialAdapter(addr)
	if err != nil {
		return nil, nil, negotiated{}, fmt.Errorf("podsession: dial adapter at %s: %w", addr, err)
	}

	resp, err := cl.NegotiateVersion(ctx, b.AcceptedVersions)
	if err != nil {
		cl.Close()
		return nil, nil, negotiated{}, fmt.Errorf("podsession: negotiate version with %s: %w", sandboxName, err)
	}
	if resp.GetIncompatible() {
		cl.Close()
		return nil, nil, negotiated{}, fmt.Errorf(
			"podsession: pod %s adapter speaks no protocol version the gateway accepts", sandboxName,
		)
	}
	// spec: §6.3, §16.1 — record the warm-pool claim
	// now that the idle→claimed transition has succeeded and the
	// adapter handshake has confirmed the pod is usable. Labels are
	// {pool, runtime_class}; the runtime_class is mapped from the
	// pod's §5.3 isolation profile so the §6.3 demotion-rate
	// denominator is per runtime class. An unrecognized profile would
	// mislabel the series; skip rather than emit an empty
	// runtime_class.
	if b.ClaimAccepted != nil {
		if rc, ok := isolation.RuntimeClassName(isolation.Profile(sb.Spec.IsolationProfile)); ok {
			b.ClaimAccepted(pool, rc)
		}
	}
	neg = negotiated{WorkspaceBase: resp.GetWorkspaceBase()}
	return sb, cl, neg, nil
}
