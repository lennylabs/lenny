// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"log"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// This file wires the gateway's resume machinery to the §8.10
// bottom-up delegation-tree recovery driver (pkg/gateway/treerecovery).
// The orchestrator owns the traversal — depth grouping, level/tree
// budgets, terminal disposition — and calls back into these adapters
// for the per-node reattach, the terminal transition, and the
// orphaned-node predicate.

// sessionNodeReattacher reattaches one orphaned delegation node onto a
// fresh pod via the §7.3 resume path, then transitions it back to
// running. It is the treerecovery.NodeReattacher the bottom-up
// traversal calls per node (leaves first).
//
// spec: §8.10 line 1016 (per-node recovery); §7.3 (resume).
type sessionNodeReattacher struct{ s *Server }

func (a sessionNodeReattacher) ReattachNode(ctx context.Context, node sessionstore.Session) error {
	if _, err := a.s.resumeOnPod(ctx, node); err != nil {
		return err
	}
	// The node lost its pod while non-terminal; resumeOnPod claimed a
	// fresh pod and restored the snapshot, so the row returns to
	// running. A node that settled concurrently (a racing terminal
	// transition) is left at its terminal state.
	_, err := a.s.store.Update(ctx, node.TenantID, node.ID, func(row *sessionstore.Session) error {
		if !session.IsTerminal(row.State) {
			row.State = session.StateRunning
		}
		return nil
	})
	return err
}

// sessionTerminalMarker marks an unrecovered delegation node terminally
// failed (or expired). It is the treerecovery.TerminalMarker the
// traversal calls for a node that exhausts its recovery budget.
//
// Per §8.10 line 1027 a node whose individual `maxResumeWindowSeconds`
// elapsed transitions to `expired`; a node lost to a level or whole-tree
// budget transitions to `failed`. Both are terminal and trigger the
// node's cascade policy from that point (§8.10 line 1025).
type sessionTerminalMarker struct{ s *Server }

func (a sessionTerminalMarker) FailNode(ctx context.Context, node sessionstore.Session, reason string) {
	if reason == "node resume window exceeded" {
		a.s.expireSession(ctx, node.TenantID, node.ID)
		return
	}
	a.s.failSession(ctx, node.TenantID, node.ID)
}

// nodeNeedsRecovery reports whether a non-terminal descendant node has
// lost its pod binding and therefore needs §8.10 reattach. A node still
// present in the pod registry is live and is left untouched, so a root
// that resumed for its own reasons does not tear down descendants still
// running on their pods. When the pod registry is unwired (dev /
// unit-test) no node is recoverable and the recovery degrades to a
// no-op.
//
// spec: §8.10 line 1014 (the tree is tracked independently of pods).
func (s *Server) nodeNeedsRecovery(node sessionstore.Session) bool {
	if s.podRegistry == nil {
		return false
	}
	_, live := s.podRegistry.Get(node.ID)
	return !live
}

// recoverDelegationTree runs the §8.10 bottom-up recovery for the tree
// rooted at rootID. The resume handler calls it after a root reattaches
// so the root's orphaned descendants are brought to a known state
// (recovered, failed, or still running) leaves-first. It is detached
// from the request: the traversal is bounded by maxTreeRecoverySeconds
// (default 600s), far longer than an HTTP response should block, so it
// runs in its own goroutine on a context that outlives the request.
//
// spec: §8.10 lines 1016, 1023.
func (s *Server) recoverDelegationTree(reqCtx context.Context, tenantID, rootID string) {
	if s.treeRecovery == nil || rootID == "" {
		return
	}
	ctx := context.WithoutCancel(reqCtx)
	go func() {
		if _, err := s.treeRecovery.RecoverTree(ctx, tenantID, rootID); err != nil {
			log.Printf("sessionserver: tree recovery for root %s failed: %v", rootID, err)
		}
		if s.treeRecoveryHook != nil {
			s.treeRecoveryHook(rootID)
		}
	}()
}
