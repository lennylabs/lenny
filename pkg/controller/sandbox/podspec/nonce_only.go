// SPDX-License-Identifier: MIT

package podspec

// nonceOnlyArgs returns the §4.7 nonce-only-mode adapter flag. It renders
// --require-so-peercred=false only when the Sandbox reconciler resolved the
// activation decision to an explicit false (a deploymentModel: sidecar
// runtime carrying requireSoPeercred: false in an acknowledged pool, §4.7).
// A nil or true value renders no flag: the adapter's own default of true
// (require SO_PEERCRED, crash on a failed self-test) then governs, so the
// builder fails closed on the security boundary. spec: §4.7.
func nonceOnlyArgs(in Inputs) []string {
	if in.RequireSoPeercred != nil && !*in.RequireSoPeercred {
		return []string{"--require-so-peercred=false"}
	}
	return nil
}
