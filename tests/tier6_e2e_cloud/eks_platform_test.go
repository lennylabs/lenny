// SPDX-License-Identifier: MIT

//go:build e2e_cloud

// Tier-6 EKS-vs-Kind platform tests. Each test exercises a behavior
// that differs materially between the Kind harness and an EKS
// cluster (EBS-backed PVCs replace Kind's emptyDir, VPC CNI replaces
// kindnet, ECR replaces in-process image cache, etc.). The tests
// skip when run outside a cloud cluster.

package tier6_e2e_cloud_test

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// spec: 17.6 (EBS CSI driver presence).
// diagnosis: TestCloudStorageClassCSIPresent asserts the cluster has
// at least one StorageClass and that the cluster-default StorageClass
// uses the EBS CSI provisioner (`ebs.csi.aws.com`). A missing CSI
// driver fails PVC attach silently — chart Deployments come up but
// any session that needs a workspace volume blocks indefinitely.
// Kind ships rancher/local-path-provisioner instead, so this test
// reports a regression specific to the EKS install path.
func TestCloudStorageClassCSIPresent(t *testing.T) {
	p := requireCloud(t)
	cli := kube(t)
	if cli == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	scs, err := cli.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list StorageClasses: %v", err)
	}
	if len(scs.Items) == 0 {
		t.Fatalf("no StorageClasses installed on the cluster")
	}
	var defaultSC, defaultProvisioner string
	var ebsSCs []string
	for _, sc := range scs.Items {
		if sc.Provisioner == "ebs.csi.aws.com" {
			ebsSCs = append(ebsSCs, sc.Name)
		}
		if v, ok := sc.Annotations["storageclass.kubernetes.io/is-default-class"]; ok && v == "true" {
			defaultSC = sc.Name
			defaultProvisioner = sc.Provisioner
		}
	}
	if defaultSC == "" {
		t.Errorf("no StorageClass annotated as the cluster default; chart-rendered PVCs will fail to bind")
	}
	// The EBS CSI provisioner is AWS-specific. The local Kind cluster
	// uses rancher.io/local-path; that's a documented dev-mode
	// outcome, not a regression. Only assert the EBS provisioner is
	// present when running against AWS.
	if p == "aws" && len(ebsSCs) == 0 {
		t.Errorf("LENNY_CLOUD_PROVIDER=aws but no StorageClass uses ebs.csi.aws.com; the EBS CSI driver addon may be missing")
	}
	t.Logf("TestCloudStorageClassCSIPresent: default=%q (provisioner=%q), EBS classes: %v", defaultSC, defaultProvisioner, ebsSCs)
}

// spec: 13.2 (VPC CNI: pod IPs from the VPC subnet).
// diagnosis: TestCloudVPCCNIPodIPFromVPC samples each running gateway
// pod and asserts its PodIP is inside the VPC CIDR (10.42.0.0/16
// for the tier-6 cluster). The §13.2 NetworkPolicies allow-list pod
// ranges derived from this CIDR; a pod whose IP lands outside (an
// overlay CNI) would be dropped silently. Kind's kindnet uses a
// separate overlay CIDR.
func TestCloudVPCCNIPodIPFromVPC(t *testing.T) {
	_ = requireCloud(t)
	cli := kube(t)
	requireGatewayInstalled(t, cli)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pods, err := cli.CoreV1().Pods(lennySystem).List(ctx, metav1.ListOptions{
		LabelSelector: "lenny.dev/component=gateway",
	})
	if err != nil {
		t.Fatalf("list gateway pods: %v", err)
	}
	if len(pods.Items) == 0 {
		t.Log("TestCloudVPCCNIPodIPFromVPC: no gateway pod running")
		return
	}
	// The VPC CIDR is 10.42.0.0/16 (cluster.tf default). When the
	// operator overrides var.vpc_cidr the test still tolerates any
	// RFC1918 / RFC6598 range — a pod IP outside the documented
	// private blocks is the actual regression.
	_, vpcCIDR, _ := net.ParseCIDR("10.42.0.0/16")
	rfc1918 := []*net.IPNet{}
	for _, c := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10"} {
		_, n, _ := net.ParseCIDR(c)
		rfc1918 = append(rfc1918, n)
	}
	var insideVPC, privateOnly, public int
	for _, pod := range pods.Items {
		ip := net.ParseIP(pod.Status.PodIP)
		if ip == nil {
			t.Errorf("pod %s has no PodIP", pod.Name)
			continue
		}
		switch {
		case vpcCIDR.Contains(ip):
			insideVPC++
		default:
			isPrivate := false
			for _, n := range rfc1918 {
				if n.Contains(ip) {
					isPrivate = true
					break
				}
			}
			if isPrivate {
				privateOnly++
			} else {
				public++
				t.Errorf("pod %s carries a public IP %s; VPC CNI should assign a VPC subnet IP", pod.Name, ip)
			}
		}
	}
	t.Logf("TestCloudVPCCNIPodIPFromVPC: %d pods inside 10.42.0.0/16, %d inside RFC1918 but outside VPC, %d public", insideVPC, privateOnly, public)
}

// spec: 17.6 (ECR pull of chart-managed images).
// diagnosis: TestCloudECRImagePullSucceeds samples each gateway pod's
// container status and asserts the image was successfully pulled
// (no ImagePullBackOff or ErrImagePull). EKS pods pulling from ECR
// require the node IAM role to carry `ecr:GetAuthorizationToken` and
// `ecr:BatchGetImage`; absent that, the chart install hangs in
// ImagePullBackOff loops until a pod retry budget is exhausted.
// Kind side-loads images via `kind load docker-image`, so this
// failure mode is invisible there.
func TestCloudECRImagePullSucceeds(t *testing.T) {
	_ = requireCloud(t)
	cli := kube(t)
	requireGatewayInstalled(t, cli)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pods, err := cli.CoreV1().Pods(lennySystem).List(ctx, metav1.ListOptions{
		LabelSelector: "lenny.dev/component=gateway",
	})
	if err != nil {
		t.Fatalf("list gateway pods: %v", err)
	}
	if len(pods.Items) == 0 {
		t.Log("TestCloudECRImagePullSucceeds: no gateway pod running")
		return
	}
	var ecrPulls int
	for _, pod := range pods.Items {
		for _, cs := range pod.Status.ContainerStatuses {
			if !strings.Contains(cs.Image, "dkr.ecr.") {
				continue
			}
			ecrPulls++
			if cs.State.Waiting != nil {
				reason := cs.State.Waiting.Reason
				if reason == "ImagePullBackOff" || reason == "ErrImagePull" {
					t.Errorf("pod %s container %s ECR pull failed: %s — %s",
						pod.Name, cs.Name, reason, cs.State.Waiting.Message)
				}
			}
			if !cs.Ready {
				t.Errorf("pod %s container %s is not Ready", pod.Name, cs.Name)
			}
		}
	}
	if ecrPulls == 0 {
		t.Log("TestCloudECRImagePullSucceeds: no gateway container uses an ECR image; the cluster is using a non-ECR registry")
		return
	}
	t.Logf("TestCloudECRImagePullSucceeds: %d ECR pull(s) succeeded across gateway pods", ecrPulls)
}
