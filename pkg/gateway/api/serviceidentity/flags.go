// SPDX-License-Identifier: MIT

package serviceidentity

import (
	"flag"
	"os"
)

// Flags holds the flag only the service identity check reads.
type Flags struct {
	// PlatformAdminServiceAccounts is the --platform-admin-service-accounts flag.
	PlatformAdminServiceAccounts *string
}

// RegisterFlags registers these flags on fs and returns their values, which
// fs fills when it parses. Each flag's default is read from its environment
// variable, as the composition root read it before the flags moved here.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.PlatformAdminServiceAccounts = fs.String("platform-admin-service-accounts", os.Getenv("LENNY_PLATFORM_ADMIN_SERVICE_ACCOUNTS"),
		"§25.4 comma-separated fully-qualified ServiceAccount usernames (system:serviceaccount:<namespace>:<name>) granted the §10.2 platform-admin role on the admin API. The chart lists lenny-ops-sa here so the §25.5 Redis-down gateway-buffer fan-out reaches the buffer endpoint. An account absent from this list is not admitted, whatever token it presents; empty grants no service account anything.")
	return f
}
