// SPDX-License-Identifier: MIT

// Package slotlayout derives and materializes the §6.4 per-slot
// directory tree and the §6.1 per-slot credential path.
//
// No pod-global `/workspace/current` path exists: every session is bound
// to a slot on every pod, whatever the pool's
// `sessionPolicy.maxConcurrentSessions`. Each active slot owns an isolated tree
// the adapter creates on its first reference to the identifier the gateway
// minted at claim time, and removes on slot cleanup (spec §6.4):
//
//	/workspace/slots/{sessionId}/current/    this slot's cwd
//	/workspace/slots/{sessionId}/staging/    this slot's upload staging area
//	/sessions/{sessionId}/                   this slot's session files (tmpfs)
//	/artifacts/{sessionId}/                  this slot's logs/outputs/checkpoints
//	/run/lenny/slots/{sessionId}/credentials.json   this slot's credential file
//
// The runtime derives its per-slot cwd from `sessionId` using the same
// `/workspace/slots/{sessionId}/current/` pattern (spec §6.4), so
// this package is the single source of truth both the adapter (tree
// creation, credential write) and the runtime (cwd derivation) agree on.
//
// spec: §6.4; §6.1.
package slotlayout

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Directory modes mirror pkg/adapter/warmlayout: the runtime reads its
// current/sessions/artifacts trees (group/other read+execute), the
// adapter alone stages uploads (adapter-private), and the credential
// directory is traversable by the lenny-cred-readers group but not
// listable so a reader opens the known credentials.json path while other
// slots' directory names stay unenumerable.
const (
	// CurrentMode is the per-slot cwd directory mode: the runtime reads
	// and writes its own `slots/{sessionId}/current` tree. spec: §6.4.
	CurrentMode = 0o755
	// StagingMode is the per-slot upload-staging directory mode: only the
	// adapter UID stages content before FinalizeWorkspace promotes it.
	StagingMode = 0o700
	// SessionsMode is the per-slot /sessions/{sessionId} tmpfs directory mode.
	SessionsMode = 0o755
	// ArtifactsMode is the per-slot /artifacts/{sessionId} directory mode.
	ArtifactsMode = 0o755
	// CredentialsDirMode is the per-slot /run/lenny/slots/{sessionId} directory
	// mode: the adapter owns it; the lenny-cred-readers group may traverse
	// (execute) into it to open the 0440 credentials.json but may not list
	// it. spec: §6.1.
	CredentialsDirMode = 0o710
)

// CredentialsFileName is the per-slot credential file the runtime reads,
// matching the single-slot credfile.FileName. spec: §6.1.
const CredentialsFileName = "credentials.json"

// slotsSegment is the fixed directory level the per-slot trees nest
// under within the workspace and credentials roots (spec §6.4:
// `/workspace/slots/{sessionId}/`; §6.1:
// `/run/lenny/slots/{sessionId}/credentials.json`).
const slotsSegment = "slots"

// Roots are the four base directories the per-slot trees nest under. In
// production they are `/workspace`, `/sessions`, `/artifacts`, and
// `/run/lenny`. Any empty root leaves the corresponding per-slot path
// empty so an adapter wired without (for example) an artifacts mount
// still resolves the rest of the tree.
type Roots struct {
	// Workspace is the base the per-slot `slots/{sessionId}/{current,staging}`
	// tree nests under (production `/workspace`).
	Workspace string
	// Sessions is the base the per-slot `{sessionId}` session tree nests
	// under (production `/sessions`).
	Sessions string
	// Artifacts is the base the per-slot `{sessionId}` artifact tree nests
	// under (production `/artifacts`).
	Artifacts string
	// Credentials is the base the per-slot `slots/{sessionId}/credentials.json`
	// file nests under (production `/run/lenny`).
	Credentials string
}

// SlotPaths is the fully-resolved per-slot tree for one slot. An empty
// field means the corresponding Roots entry was empty.
type SlotPaths struct {
	// SlotID is the validated slot identifier the paths derive from.
	SlotID string
	// Current is `/workspace/slots/{sessionId}/current` — the slot's cwd.
	Current string
	// Staging is `/workspace/slots/{sessionId}/staging` — the slot's upload
	// staging area.
	Staging string
	// Sessions is `/sessions/{sessionId}` — the slot's session-file tmpfs.
	Sessions string
	// Artifacts is `/artifacts/{sessionId}` — the slot's artifact tree.
	Artifacts string
	// CredentialsDir is `/run/lenny/slots/{sessionId}` — the slot's credential
	// directory.
	CredentialsDir string
	// CredentialsFile is `/run/lenny/slots/{sessionId}/credentials.json` — the
	// slot's credential file.
	CredentialsFile string
}

// ValidateSlotID rejects a slot id that is not a safe single path
// segment. The slot id arrives over the wire (the gateway sets it to the
// session id), so it crosses a trust boundary into filesystem paths;
// rejecting separators, dot segments, and NUL prevents a malformed or
// hostile id from escaping the per-slot tree via path traversal.
//
// spec: §6.4.
func ValidateSlotID(slotID string) error {
	if slotID == "" {
		return fmt.Errorf("slotlayout: slot id is empty")
	}
	if slotID == "." || slotID == ".." {
		return fmt.Errorf("slotlayout: slot id %q is a path-traversal segment", slotID)
	}
	if strings.ContainsAny(slotID, "/\\\x00") {
		return fmt.Errorf("slotlayout: slot id %q contains a path separator or NUL", slotID)
	}
	// filepath.Clean of a safe segment is the segment itself; anything
	// else (a stray "..", a leading separator the platform normalizes)
	// indicates an unsafe id.
	if filepath.Clean(slotID) != slotID {
		return fmt.Errorf("slotlayout: slot id %q is not a clean path segment", slotID)
	}
	return nil
}

// Resolve derives the per-slot tree for slotID under roots. It validates
// the slot id first so every returned path is contained within its root.
func Resolve(roots Roots, slotID string) (SlotPaths, error) {
	if err := ValidateSlotID(slotID); err != nil {
		return SlotPaths{}, err
	}
	p := SlotPaths{SlotID: slotID}
	if roots.Workspace != "" {
		slotRoot := filepath.Join(roots.Workspace, slotsSegment, slotID)
		p.Current = filepath.Join(slotRoot, "current")
		p.Staging = filepath.Join(slotRoot, "staging")
	}
	if roots.Sessions != "" {
		p.Sessions = filepath.Join(roots.Sessions, slotID)
	}
	if roots.Artifacts != "" {
		p.Artifacts = filepath.Join(roots.Artifacts, slotID)
	}
	if roots.Credentials != "" {
		p.CredentialsDir = filepath.Join(roots.Credentials, slotsSegment, slotID)
		p.CredentialsFile = filepath.Join(p.CredentialsDir, CredentialsFileName)
	}
	return p, nil
}

// SlotsDir returns the `slots` container under base, the directory whose
// children are the per-slot trees. An empty base yields the empty string.
// The whole-pod scrub enumerates it to reach a leaked slot's residue,
// whose registry entry is already gone. spec: §5.2; §6.4.
func SlotsDir(base string) string {
	if base == "" {
		return ""
	}
	return filepath.Join(base, slotsSegment)
}

// SessionCurrentDir returns the session's §6.4 cwd,
// `<base>/slots/{sessionId}/current`, under the workspace base the
// adapter reports on the §15.5 handshake. It is the one derivation the
// gateway performs from the reported base and a session identifier. An
// empty base, an empty identifier, or an identifier that is not a safe
// path segment yields the empty string, which every caller treats the way
// it treats an unreported base. spec: §6.4; §7.3.
func SessionCurrentDir(base, sessionID string) string {
	if base == "" {
		return ""
	}
	paths, err := Resolve(Roots{Workspace: base}, sessionID)
	if err != nil {
		return ""
	}
	return paths.Current
}

// slotRoot returns `/workspace/slots/{sessionId}` for the workspace-side
// removal, the parent of both current/ and staging/.
func (p SlotPaths) slotRoot() string {
	if p.Current == "" {
		return ""
	}
	// Current is `<root>/slots/<sessionId>/current`; its parent is the slot
	// root removed wholesale on cleanup.
	return filepath.Dir(p.Current)
}
