// SPDX-License-Identifier: MIT

// Package leaderjobs holds the session core's gateway-singleton jobs that
// act on a package GW-R2 confines, such as the continuous T4 KMS probe of
// §12.5. The replica's leader gate runs the set as one job while the
// replica holds the gateway leader lease, so the gate holds a value of the
// session core and never a value of a confined package.
//
// spec: GW
package leaderjobs
