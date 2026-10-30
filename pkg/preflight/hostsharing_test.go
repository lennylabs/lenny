// SPDX-License-Identifier: MIT

package preflight_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/preflight"
)

func TestCheckHostSharingPassesForCompliantWorkloads(t *testing.T) {
	d := preflight.CheckHostSharing([]preflight.HostSharingPodSpec{
		{Workload: "Deployment/lenny-gateway"},
		{Workload: "Deployment/lenny-controller"},
	})
	if !d.Passed {
		t.Errorf("compliant workloads were rejected: %s", d.Reason)
	}
}

func TestCheckHostSharingPassesForEmptyInput(t *testing.T) {
	if d := preflight.CheckHostSharing(nil); !d.Passed {
		t.Errorf("an empty workload set was rejected: %s", d.Reason)
	}
}

func TestCheckHostSharingRejectsShareProcessNamespace(t *testing.T) {
	d := preflight.CheckHostSharing([]preflight.HostSharingPodSpec{
		{Workload: "Deployment/lenny-gateway", ShareProcessNamespace: true},
	})
	if d.Passed {
		t.Fatal("a workload with shareProcessNamespace true was accepted")
	}
	if !strings.Contains(d.Reason, "shareProcessNamespace") || !strings.Contains(d.Reason, "lenny-gateway") {
		t.Errorf("reason %q does not name the field and workload", d.Reason)
	}
	if !strings.Contains(d.Reason, "POD_SPEC_HOST_SHARING_FORBIDDEN") {
		t.Errorf("reason %q does not carry the §13.1 error code", d.Reason)
	}
}

func TestCheckHostSharingRejectsHostPID(t *testing.T) {
	d := preflight.CheckHostSharing([]preflight.HostSharingPodSpec{
		{Workload: "DaemonSet/x", HostPID: true},
	})
	if d.Passed || !strings.Contains(d.Reason, "hostPID") {
		t.Errorf("hostPID true was not rejected with a hostPID reason: %+v", d)
	}
}

func TestCheckHostSharingRejectsHostNetwork(t *testing.T) {
	d := preflight.CheckHostSharing([]preflight.HostSharingPodSpec{
		{Workload: "Deployment/x", HostNetwork: true},
	})
	if d.Passed || !strings.Contains(d.Reason, "hostNetwork") {
		t.Errorf("hostNetwork true was not rejected with a hostNetwork reason: %+v", d)
	}
}

func TestCheckHostSharingRejectsHostIPC(t *testing.T) {
	d := preflight.CheckHostSharing([]preflight.HostSharingPodSpec{
		{Workload: "Job/x", HostIPC: true},
	})
	if d.Passed || !strings.Contains(d.Reason, "hostIPC") {
		t.Errorf("hostIPC true was not rejected with a hostIPC reason: %+v", d)
	}
}

func TestCheckHostSharingRejectsTheOffendingWorkloadAmongMany(t *testing.T) {
	d := preflight.CheckHostSharing([]preflight.HostSharingPodSpec{
		{Workload: "Deployment/lenny-gateway"},
		{Workload: "Deployment/lenny-webhook", HostIPC: true},
		{Workload: "Deployment/lenny-controller"},
	})
	if d.Passed {
		t.Fatal("a set containing a non-compliant workload was accepted")
	}
	if !strings.Contains(d.Reason, "lenny-webhook") {
		t.Errorf("reason %q does not name the offending workload", d.Reason)
	}
}
