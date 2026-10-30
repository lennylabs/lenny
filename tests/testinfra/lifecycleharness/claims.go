// SPDX-License-Identifier: MIT

package lifecycleharness

import "sync"

// ClaimLog records the Sandbox each SandboxClaim a replica's binder created
// names, in creation order. A claim that a later release deletes stays in
// the log, so a scenario that asserts how many pods a race claimed counts
// the claims an uncommitted restore retired as well as the claims still
// bound. Only a create the kube-apiserver accepted is recorded; a create
// that lost the per-pod uniqueness check claimed nothing.
//
// spec: §4.6.1 (Warm Pool Controller (Pod Lifecycle)), §7.3 (Resume driver)
type ClaimLog struct {
	mu   sync.Mutex
	pods []string
}

// record appends one accepted claim create.
func (l *ClaimLog) record(pod string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.pods = append(l.pods, pod)
	l.mu.Unlock()
}

// Len returns how many claims the binders created.
func (l *ClaimLog) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.pods)
}

// Since returns the pod each claim created after the first n names.
func (l *ClaimLog) Since(n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n >= len(l.pods) {
		return nil
	}
	return append([]string(nil), l.pods[n:]...)
}
