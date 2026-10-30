// SPDX-License-Identifier: MIT

// Package carrier holds two ranges whose endpoints fall in two
// sections, so one run has more than one site to report in one file.
package carrier

// renew renews the lease.
//
// spec: §4.6 lines 10-13
func renew() string { return "renew" }

// plan reads the workspace plan.
//
// spec: §4.8 lines 14-18
func plan() string { return "plan" }
