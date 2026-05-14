// SPDX-License-Identifier: MIT

package podsecurity

import (
	"testing"
)

// FuzzValidateAgentPod exercises the §13.1 pod-spec admission
// validator on randomized inputs. Invariant: never panics, no matter
// how malformed the SecurityContext is. A panic here would let an
// adversarial pod admission request crash the gateway-side validator.
//
// The seed corpus mixes a valid baseline with the known violations:
// host-sharing flags, missing fsGroup, missing supplemental group,
// privileged container, etc.
func FuzzValidateAgentPod(f *testing.F) {
	f.Add(false, false, false, false, true, int64(2000), int64(2001))
	f.Add(true, false, false, false, true, int64(2000), int64(2001))  // hostShare
	f.Add(false, true, false, false, true, int64(2000), int64(2001))  // hostPID
	f.Add(false, false, false, false, false, int64(0), int64(2001))   // missing fsGroup
	f.Add(false, false, false, false, true, int64(2000), int64(3000)) // wrong cred-readers GID

	f.Fuzz(func(t *testing.T,
		shareProcessNamespace, hostPID, hostNetwork, hostIPC, includeCredReadersGID bool,
		fsGroup, credReadersGID int64,
	) {
		spec := PodSpec{
			ShareProcessNamespace: shareProcessNamespace,
			HostPID:               hostPID,
			HostNetwork:           hostNetwork,
			HostIPC:               hostIPC,
		}
		if fsGroup != 0 {
			spec.FSGroup = Ptr(fsGroup)
		}
		if includeCredReadersGID {
			spec.SupplementalGroups = []int64{credReadersGID}
		}
		_ = ValidateAgentPod(spec, credReadersGID)
	})
}
