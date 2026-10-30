// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"errors"

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
// to capture from the handshake. It carries the §15.5 NegotiateVersion
// response fields the gateway threads onto BindResult so downstream
// users (session-row persistence, Resume-time assertion) can see them.
type negotiated struct {
	// WorkspaceBase is the §6.4 base the adapter nests every session's
	// per-slot tree under. A session's §7.3 cwd is derived from it and the
	// session identifier. Empty when the adapter reports none. F-7.3.15.
	WorkspaceBase string
}

// claimPod claims an idle pod from the pool for the request's session,
// trying the §4.6.1 Postgres-backed fallback claim when the Kubernetes-API
// claim finds no idle pod, and returns the claimed Sandbox name. It opens no
// adapter connection: the post-claim hook runs between it and handshake, so a
// caller can record the claim before any RPC reaches the pod. A failure here
// leaves no claim behind for the caller to release. req carries the pool,
// session, tenant, and the resolved pool's ReusesPods and
// RequiresProcessAcknowledgment.
//
// spec: §4.6.1 (Warm Pool Controller (Pod Lifecycle)); §7.3 (Retry and
// Resume).
func (b *Binder) claimPod(ctx context.Context, req podclaim.ClaimRequest) (string, error) {
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
		return b.fallbackClaim(ctx, req, refused)
	}
	if err != nil {
		return "", err
	}
	return claim.Spec.SandboxRef, nil
}

// runOnClaimed invokes a binder request's post-claim hook. A nil hook is a
// no-op, which is the create and start paths. The hook's error is returned as
// the hook returned it: it is never wrapped in a *SlotBindError and never
// matches an exhaustion sentinel, so runWithQueue and the §5.2 slot retry
// policy stop on it rather than retrying it.
//
// spec: §7.3 (Retry and Resume).
func runOnClaimed(ctx context.Context, onClaimed func(context.Context) error) error {
	if onClaimed == nil {
		return nil
	}
	return onClaimed(ctx)
}

// handshake resolves the claimed Sandbox, dials its adapter, and runs the
// §15.5 version handshake, then records the §6.3 warm-pool claim. It closes
// any client it opened on its own failure. On success the caller owns cl and
// must close it once the session ends or on any later failure, and owns the
// returned Sandbox object. The negotiated return value carries the
// handshake-reported metadata the caller threads onto BindResult.
//
// spec: §15.5 (version handshake); §6.3 (warm-pool claim counter).
func (b *Binder) handshake(ctx context.Context, sandboxName, pool string) (*lennyv1.Sandbox, *adapterclient.Client, negotiated, error) {
	sb, cl, neg, err := b.dialAndNegotiate(ctx, sandboxName)
	if err != nil {
		return nil, nil, negotiated{}, err
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
	return sb, cl, neg, nil
}
