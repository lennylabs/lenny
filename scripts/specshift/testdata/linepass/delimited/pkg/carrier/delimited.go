// SPDX-License-Identifier: MIT

// Package carrier holds one citation per kind of delimited run written
// behind a last member. Each is copied from a site the deletion
// produced, so a case names the content the rule keeps.
package carrier

// sdkDemoteGraceMarginSeconds is the §6.1 line 67 "+5s" the grace period
// adds to the demotion deadline the adapter reports.
const sdkDemoteGraceMarginSeconds = 5

// partialManifestKey keys a partial manifest by
// (tenantID, sessionID) — the §10.1 line 155 "last successful full
// checkpoint" the resume path falls back to when reassembly of the
// manifest fails.
type partialManifestKey struct{}

// rlsAllSentinel reports the platform-admin sentinel.
//
// spec: §4.2 line 165 (a), (b), (c) — TestRLSPlatformAdminAllSentinel
func rlsAllSentinel() bool { return true }

// orphanedObjects counts the objects the sweep reports.
//
// spec: §4.9 line 248 (orphaned-object counter, which the §4.8 sweep
// rule reads)
func orphanedObjects() int { return 0 }

// narrowed returns the operability tools the subject keeps.
//
// §13.3 line 583(e): the subject's narrowed operability-tool set is the
// intersection of the delegated set and the subject's own.
func narrowed() int { return 1 }
