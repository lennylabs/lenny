// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"

	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// errReplicaCrashed is the error every Kubernetes write of a crashed
// replica's binder returns.
var errReplicaCrashed = errors.New("lifecycleharness: the replica crashed")

// crashableClient is the Kubernetes client of one replica's binder. Once
// the replica crashes, every write it issues fails, as a write from a
// process that no longer runs never reaches the kube-apiserver. A replica's
// goroutines outlive Crash in the harness, so without this a release the
// binder detached before the crash (such as deleting the claim a failed
// resume attempt made) would still land, and a scenario could not leave
// the orphaned claim the controller's claim-orphan GC exists to reclaim
// (§4.6.1 Orphaned SandboxClaim detection, §7.3 Resume driver). Reads pass
// through, because a read changes nothing a scenario asserts.
//
// It also records each SandboxClaim create the kube-apiserver accepts in
// claims, when set, so a scenario can count the pods a race claimed.
type crashableClient struct {
	client.Client
	crashed *atomic.Bool
	claims  *ClaimLog
	// retire moves a pod's Sandbox to draining, as the occupancy
	// projection does when the pod's claim is deleted. Nil projects
	// nothing.
	retire func(ctx context.Context, pod string)
}

// down reports the crash error once the replica crashed.
func (c crashableClient) down() error {
	if c.crashed.Load() {
		return errReplicaCrashed
	}
	return nil
}

// Create refuses the create once the replica crashed.
func (c crashableClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if err := c.down(); err != nil {
		return err
	}
	if err := c.Client.Create(ctx, obj, opts...); err != nil {
		return err
	}
	if claim, ok := obj.(*lennyv1.SandboxClaim); ok {
		c.claims.record(claim.Spec.SandboxRef)
	}
	return nil
}

// Update refuses the update once the replica crashed.
func (c crashableClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if err := c.down(); err != nil {
		return err
	}
	return c.Client.Update(ctx, obj, opts...)
}

// Patch refuses the patch once the replica crashed.
func (c crashableClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if err := c.down(); err != nil {
		return err
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

// Delete refuses the delete once the replica crashed.
func (c crashableClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if err := c.down(); err != nil {
		return err
	}
	if err := c.Client.Delete(ctx, obj, opts...); err != nil {
		return err
	}
	if claim, ok := obj.(*lennyv1.SandboxClaim); ok && c.retire != nil {
		c.projectRetirement(ctx, claim)
	}
	return nil
}

// projectRetirement stands in for the WarmPoolController's occupancy
// projection, which the harness does not run: a claim the gateway deletes
// retires its pod, so the pod's Sandbox moves to draining and no later claim
// takes it. The claim names its pod in spec.sandboxRef, or, for a delete by
// name, through the deterministic claim-<pod> name. A Sandbox already gone is
// left alone.
// spec: §4.6.1 (occupancy projection on claim DELETE), §6.2 (claimed →
// draining)
func (c crashableClient) projectRetirement(ctx context.Context, claim *lennyv1.SandboxClaim) {
	pod := claim.Spec.SandboxRef
	if pod == "" {
		pod = strings.TrimPrefix(claim.Name, "claim-")
	}
	c.retire(ctx, pod)
}

// DeleteAllOf refuses the delete once the replica crashed.
func (c crashableClient) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	if err := c.down(); err != nil {
		return err
	}
	return c.Client.DeleteAllOf(ctx, obj, opts...)
}

// Status returns a status writer that refuses writes once the replica
// crashed.
func (c crashableClient) Status() client.SubResourceWriter {
	return crashableSubResource{SubResourceWriter: c.Client.Status(), c: c}
}

// SubResource returns a subresource client whose writes are refused once
// the replica crashed.
func (c crashableClient) SubResource(name string) client.SubResourceClient {
	inner := c.Client.SubResource(name)
	return crashableSubResourceClient{SubResourceClient: inner, w: crashableSubResource{SubResourceWriter: inner, c: c}}
}

// crashableSubResource is a subresource writer of a crashable client.
type crashableSubResource struct {
	client.SubResourceWriter
	c crashableClient
}

// Create refuses the subresource create once the replica crashed.
func (s crashableSubResource) Create(ctx context.Context, obj, sub client.Object, opts ...client.SubResourceCreateOption) error {
	if err := s.c.down(); err != nil {
		return err
	}
	return s.SubResourceWriter.Create(ctx, obj, sub, opts...)
}

// Update refuses the subresource update once the replica crashed.
func (s crashableSubResource) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	if err := s.c.down(); err != nil {
		return err
	}
	return s.SubResourceWriter.Update(ctx, obj, opts...)
}

// Patch refuses the subresource patch once the replica crashed.
func (s crashableSubResource) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	if err := s.c.down(); err != nil {
		return err
	}
	return s.SubResourceWriter.Patch(ctx, obj, patch, opts...)
}

// crashableSubResourceClient is a subresource client of a crashable
// client: its reads pass through and its writes go through w.
type crashableSubResourceClient struct {
	client.SubResourceClient
	w crashableSubResource
}

// Create refuses the subresource create once the replica crashed.
func (s crashableSubResourceClient) Create(ctx context.Context, obj, sub client.Object, opts ...client.SubResourceCreateOption) error {
	return s.w.Create(ctx, obj, sub, opts...)
}

// Update refuses the subresource update once the replica crashed.
func (s crashableSubResourceClient) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return s.w.Update(ctx, obj, opts...)
}

// Patch refuses the subresource patch once the replica crashed.
func (s crashableSubResourceClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	return s.w.Patch(ctx, obj, patch, opts...)
}
