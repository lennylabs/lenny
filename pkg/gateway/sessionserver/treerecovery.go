// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/delegation/recovery"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// This file wires the gateway's resume machinery to the §8.10
// bottom-up delegation-tree recovery driver (pkg/gateway/treerecovery).
// The orchestrator owns the traversal — depth grouping, level/tree
// budgets, terminal disposition — and calls back into these adapters
// for the per-node reattach, the terminal transition, and the
// orphaned-node predicate.

// treeRecoveryPollInterval is how often ReattachNode reads a node's row from
// the primary while it waits for the node's restore to finish.
const treeRecoveryPollInterval = time.Second

// sessionNodeReattacher drives and waits for the restore of one
// resume_pending delegation node. It is the treerecovery.NodeReattacher the
// bottom-up traversal calls per node, leaves-first. It claims no pod and
// writes no row: the node's restore runs through the resume driver on the
// replica that coordinates the node.
//
// The states the traversal does not select are routed elsewhere: a node that
// holds its pod (running, input_required, or held-pod suspended) is
// re-adopted onto that pod by the coordination Sweeper when its coordinator
// dies; a node whose pod failed reaches resume_pending through its failure
// edge, where this traversal or its coordinating replica's watchdog drives
// it; and a podless suspended node is restored by its next message or
// resume.
//
// spec: §8.10 (Delegation Tree Recovery); §7.3 (Resume driver).
type sessionNodeReattacher struct{ s *Server }

// StartNode drives the node's restore through the resume driver, which
// applies the in-flight check, the coordination-lease gate, and the
// active-age check. spec: §8.10; §7.3 (Resume driver).
func (a sessionNodeReattacher) StartNode(ctx context.Context, node sessionstore.Session) {
	a.s.DriveResume(ctx, node)
}

// ReattachNode waits for the node's restore: it reads the row from the
// primary once per poll interval until the row is in neither resume_pending
// nor resuming, or the context, which carries the node's recovery deadline,
// ends. It returns an error when the restore ended in awaiting_client_action
// or a terminal state, and nil for any other state, because the restore
// committed: a restored node can leave running before the next poll, to
// input_required or suspended, and neither is a failed restore. A read error
// is logged and the next poll retries. A node coordinated by a peer waits
// for that replica's driver.
//
// spec: §8.10 (Delegation Tree Recovery).
func (a sessionNodeReattacher) ReattachNode(ctx context.Context, node sessionstore.Session) error {
	interval := a.s.treePollInterval
	if interval <= 0 {
		interval = treeRecoveryPollInterval
	}
	for {
		row, err := a.s.readPrimary(ctx, node.TenantID, node.ID)
		switch {
		case err != nil:
			if ctx.Err() == nil {
				log.Printf("sessionserver: tree recovery poll of node %s: %v", node.ID, err)
			}
		case row.State == session.StateAwaitingClientAction || session.IsTerminal(row.State):
			return fmt.Errorf("sessionserver: tree recovery of node %s ended in %s", node.ID, row.State)
		case row.State != session.StateResumePending && row.State != session.StateResuming:
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("sessionserver: tree recovery wait for node %s: %w", node.ID, ctx.Err())
		case <-time.After(interval):
		}
	}
}

// sessionTerminalMarker marks an unrecovered delegation node terminally
// failed (or expired). It is the treerecovery.TerminalMarker the
// traversal calls for a node that exhausts its recovery budget.
//
// Per §8.10 a node whose individual `maxResumeWindowSeconds`
// elapsed transitions to `expired`; a node lost to a level or whole-tree
// budget transitions to `failed`. Both are terminal and trigger the
// node's cascade policy from that point (§8.10).
type sessionTerminalMarker struct{ s *Server }

func (a sessionTerminalMarker) FailNode(ctx context.Context, node sessionstore.Session, reason string) {
	if reason == recovery.ReasonResumeWindow {
		a.s.expireSession(ctx, node.TenantID, node.ID)
		return
	}
	a.s.failSession(ctx, node.TenantID, node.ID)
}

// nodeNeedsRecovery reports whether a delegation node is selected for §8.10
// recovery: exactly the resume_pending nodes. A node is selected by its row
// state rather than by its absence from this replica's pod registry, because
// the registry holds only this replica's bindings and a node bound on a peer
// is not orphaned. Tree recovery therefore never drives, fails, or expires a
// node that is running, suspended, awaiting_client_action, or bound on a
// peer replica.
//
// spec: §8.10 (Delegation Tree Recovery).
func (s *Server) nodeNeedsRecovery(node sessionstore.Session) bool {
	return node.State == session.StateResumePending
}

// recoverDelegationTree runs the §8.10 bottom-up recovery for the tree
// rooted at rootID. The resume driver's commit tail calls it after a node of
// the tree resumed, so the tree's resume_pending descendants are driven and
// brought to a known state (recovered, failed, or still running)
// leaves-first. The traversal is bounded by maxTreeRecoverySeconds (default
// 600s), so it runs in its own goroutine on the server-lifetime context,
// which shutdown cancels.
//
// spec: §8.10.
func (s *Server) recoverDelegationTree(_ context.Context, tenantID, rootID string) {
	if s.treeRecovery == nil || rootID == "" {
		return
	}
	ctx := s.lifetime()
	go func() {
		if _, err := s.treeRecovery.RecoverTree(ctx, tenantID, rootID); err != nil {
			log.Printf("sessionserver: tree recovery for root %s failed: %v", rootID, err)
		}
		if s.treeRecoveryHook != nil {
			s.treeRecoveryHook(rootID)
		}
	}()
}
