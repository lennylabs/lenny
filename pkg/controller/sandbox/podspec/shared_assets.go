// SPDX-License-Identifier: MIT

package podspec

// sharedAssetsArgs returns the §6.4 adapter flags for the
// /workspace/shared read-only asset tree. The --shared-assets-dir flag
// is always set so the adapter ensures the directory at warm time (it is
// mounted read-write on the adapter container, read-only on the runtime
// container). --shared-assets carries the inline asset set only when the
// Runtime declares any; an empty set leaves the directory mounted but
// empty. spec: §6.4 — F-6.4.3.
func sharedAssetsArgs(in Inputs) []string {
	args := []string{"--shared-assets-dir=" + sharedMount}
	if in.SharedAssetsArg != "" {
		args = append(args, "--shared-assets="+in.SharedAssetsArg)
	}
	return args
}
