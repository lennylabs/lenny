// SPDX-License-Identifier: MIT

package v1alpha1

// Restartable reports the §5.1 derived `restartable` flag of a Runtime: true
// for a runtime that carries `command`, has `executionMode: session` (an
// empty executionMode defaults to session), and does not declare
// `capabilities.preConnect: true`. Gateway pool admission reads the flag,
// through the registry mirror the RuntimeReconciler writes, to decide
// whether a pool may select `recycle.runtimeProcess: restart`, and the pod
// builder reads it from the live CRD before it creates a `restart` pod. Both
// call this one function so the two readers cannot derive different values.
// spec: §5.1 (command), §5.2 (recycle.runtimeProcess).
func (s *RuntimeSpec) Restartable() bool {
	if len(s.Command) == 0 {
		return false
	}
	if s.ExecutionMode != "" && s.ExecutionMode != "session" {
		return false
	}
	return s.Capabilities == nil || !s.Capabilities.PreConnect
}
