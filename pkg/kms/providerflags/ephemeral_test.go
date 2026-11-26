// SPDX-License-Identifier: MIT

package providerflags

import "testing"

// TestEphemeralKEKIsTheLocalProviderWithoutAMasterKeyFile checks which
// provider options make each process draw its own key-encryption key.
//
// spec: §7.1 (Normal Flow), §17.4
func TestEphemeralKEKIsTheLocalProviderWithoutAMasterKeyFile(t *testing.T) {
	cases := []struct {
		opts Options
		want bool
	}{
		{Options{}, true},
		{Options{Provider: "local"}, true},
		{Options{Provider: " LOCAL "}, true},
		{Options{Provider: "local", MasterKeyFile: "/var/lib/lenny/master.key"}, false},
		{Options{Provider: "aws"}, false},
		{Options{Provider: "gcp"}, false},
		{Options{Provider: "azure"}, false},
	}
	for _, tc := range cases {
		if got := tc.opts.EphemeralKEK(); got != tc.want {
			t.Errorf("%+v: EphemeralKEK = %v, want %v", tc.opts, got, tc.want)
		}
	}
}
