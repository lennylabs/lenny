// SPDX-License-Identifier: MIT

package providerflags

import "strings"

// EphemeralKEK reports whether the options select the local provider with
// no master key file. Its key-encryption key is then drawn at random by
// each process, so a value one process wraps cannot be unwrapped by
// another process or by the same process after a restart. A caller that
// would share wrapped state through a database keeps it in process memory
// instead.
//
// spec: §7.1 (Normal Flow), §17.4
func (o Options) EphemeralKEK() bool {
	prov := strings.ToLower(strings.TrimSpace(o.Provider))
	return (prov == "" || prov == ProviderLocal) && strings.TrimSpace(o.MasterKeyFile) == ""
}
