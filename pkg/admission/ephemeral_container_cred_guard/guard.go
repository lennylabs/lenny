// SPDX-License-Identifier: MIT

// Package ephemeral_container_cred_guard implements the pure-decision
// logic of the lenny-ephemeral-container-cred-guard
// ValidatingAdmissionWebhook per spec §13.1. The webhook is deployed
// fail-closed in front of the pods/ephemeralcontainers subresource in
// every agent namespace.
//
// An actor with `update` on pods/ephemeralcontainers could otherwise
// attach a debug container that runs as the pod's agent UID or joins
// the lenny-cred-readers group and reads /run/lenny/credentials.json.
// The guard rejects any ephemeral container that could reach the
// credential file, applying the four §13.1 conditions:
//
//	(i)   runAsUser is the pod's adapter UID or agent UID.
//	(ii)  runAsGroup or supplementalGroups includes the
//	      lenny-cred-readers GID.
//	(iii) runAsUser, runAsGroup, or supplementalGroups is absent —
//	      an absent value inherits pod-level defaults, including the
//	      fsGroup that grants credential-file read access.
//	(iv)  a volume mount references the credential volume by name or
//	      mounts a path at or under /run/lenny.
//
// The decision logic is split from the webhook HTTP/AdmissionReview
// transport so it can be unit-tested without the controller-runtime
// stack.
package ephemeral_container_cred_guard

import (
	"fmt"
	"strings"
)

// RejectionCode is the §15.1 error code stamped on every rejection.
const RejectionCode = "EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN"

// credPathPrefix is the pod directory carrying the §4.7 credential
// file. A mount at this path or below reaches /run/lenny/credentials.json.
const credPathPrefix = "/run/lenny"

// VolumeMount is the security-relevant projection of one of an
// ephemeral container's volume mounts.
type VolumeMount struct {
	// Name is the pod volume the mount references.
	Name string
	// MountPath is the container path the volume is mounted at.
	MountPath string
}

// Container is the security-relevant projection of one ephemeral
// container the webhook evaluates. A nil RunAsUser, nil RunAsGroup, or
// nil SupplementalGroups means the field was absent on the ephemeral
// container's securityContext.
type Container struct {
	// Name identifies the container in rejection messages.
	Name string
	// RunAsUser is the ephemeral container's securityContext.runAsUser.
	RunAsUser *int64
	// RunAsGroup is the ephemeral container's securityContext.runAsGroup.
	RunAsGroup *int64
	// SupplementalGroups is the ephemeral container's
	// securityContext.supplementalGroups.
	SupplementalGroups []int64
	// VolumeMounts are the ephemeral container's volume mounts.
	VolumeMounts []VolumeMount
}

// Request is the input to Decide.
type Request struct {
	// NewContainers are the ephemeral containers this admission request
	// adds to the pod (present in the new pod, absent from the old).
	NewContainers []Container
	// AdapterUID is the pod's §4.7 adapter container UID.
	AdapterUID int64
	// AgentUID is the pod's agent container UID.
	AgentUID int64
	// CredGID is the lenny-cred-readers GID, the credential-file read
	// boundary inside the pod.
	CredGID int64
	// CredVolumeName is the name of the pod volume carrying the §4.7
	// credential tmpfs. Empty disables the by-name match of condition
	// (iv), leaving the by-path match in force.
	CredVolumeName string
}

// Decision is the admission outcome.
type Decision struct {
	// Allowed is true when every new ephemeral container passes the
	// four §13.1 conditions.
	Allowed bool
	// Reason is the rejection message relayed to the offending client
	// when Allowed is false; empty when Allowed is true.
	Reason string
	// Code is the HTTP status the webhook surfaces: 200 on allow, 403
	// on rejection.
	Code int
}

// Decide applies the four §13.1 conditions to every ephemeral
// container the request adds. The first violating container rejects
// the whole request, fail-closed.
func Decide(r Request) Decision {
	for _, c := range r.NewContainers {
		if msg := violation(c, r); msg != "" {
			return Decision{Allowed: false, Reason: RejectionCode + ": " + msg, Code: 403}
		}
	}
	return Decision{Allowed: true, Code: 200}
}

// violation returns the rejection message for an ephemeral container
// that breaches a §13.1 condition, or "" when the container is safe.
func violation(c Container, r Request) string {
	// Condition (iii): an absent securityContext field inherits
	// pod-level defaults, including the fsGroup that grants
	// credential-file read access.
	if c.RunAsUser == nil || c.RunAsGroup == nil || c.SupplementalGroups == nil {
		return fmt.Sprintf("ephemeral container %q must set runAsUser, runAsGroup, and supplementalGroups explicitly; an absent value inherits the pod credential-group defaults", c.Name)
	}
	// Condition (i): runAsUser is a credential-reading UID.
	if *c.RunAsUser == r.AdapterUID || *c.RunAsUser == r.AgentUID {
		return fmt.Sprintf("ephemeral container %q runAsUser %d is the pod adapter or agent UID; a credential-reading UID is forbidden", c.Name, *c.RunAsUser)
	}
	// Condition (ii): runAsGroup or supplementalGroups is the
	// lenny-cred-readers GID.
	if *c.RunAsGroup == r.CredGID {
		return fmt.Sprintf("ephemeral container %q runAsGroup %d is the lenny-cred-readers GID; the credential-reading group is forbidden", c.Name, r.CredGID)
	}
	for _, g := range c.SupplementalGroups {
		if g == r.CredGID {
			return fmt.Sprintf("ephemeral container %q supplementalGroups includes the lenny-cred-readers GID %d; the credential-reading group is forbidden", c.Name, r.CredGID)
		}
	}
	// Condition (iv): a volume mount reaches the credential file.
	for _, m := range c.VolumeMounts {
		if r.CredVolumeName != "" && m.Name == r.CredVolumeName {
			return fmt.Sprintf("ephemeral container %q mounts the credential volume %q; mounting the credential volume is forbidden", c.Name, m.Name)
		}
		if m.MountPath == credPathPrefix || strings.HasPrefix(m.MountPath, credPathPrefix+"/") {
			return fmt.Sprintf("ephemeral container %q mounts a volume at %q, under the credential path %s; mounting the credential path is forbidden", c.Name, m.MountPath, credPathPrefix)
		}
	}
	return ""
}
