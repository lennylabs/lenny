// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/rest"

	"github.com/lennylabs/lenny/pkg/ops/opsservice"
)

// errNoKubeconfig stands in for the controller-runtime loader's error when
// neither an in-cluster config nor a kubeconfig file exists.
var errNoKubeconfig = errors.New("could not locate a kubeconfig")

// kubeConfigCase is one REST-config outcome buildKubeClients can meet.
type kubeConfigCase struct {
	name string
	load func() (*rest.Config, error)
	// wantClients is true when the config builds the clientset.
	wantClients bool
}

func kubeConfigCases() []kubeConfigCase {
	return []kubeConfigCase{
		{"no_kubeconfig", func() (*rest.Config, error) { return nil, errNoKubeconfig }, false},
		// QPS without Burst and without a RateLimiter is rejected by
		// kubernetes.NewForConfig, the clientset-build-failure branch.
		{"clientset_build_fails", func() (*rest.Config, error) {
			return &rest.Config{Host: "http://127.0.0.1:1", QPS: 5}, nil
		}, false},
		// A well-formed config builds the clients without contacting the
		// API server, which is never reached in this test.
		{"config_present", func() (*rest.Config, error) {
			return &rest.Config{Host: "http://127.0.0.1:1"}, nil
		}, true},
	}
}

// opsDepsFlags returns the flag set buildDependencies and
// buildEventStreamAndWebhooks read, with the shared HMAC key file at keyFile
// ("" leaves the key unset).
func opsDepsFlags(keyFile string) *opsFlags {
	f := eventStreamWiringFlags("")
	f.postgresDSN = strPtr("")
	f.redisURL = strPtr("")
	f.redisClusterAddrs = strPtr("")
	f.redisSentinelAddrs = strPtr("")
	f.backupMinIOEndpoint = strPtr("")
	f.prometheusURL = strPtr("")
	f.runbookDir = strPtr("")
	f.leaderLeaseDuration = intPtr(0)
	f.leaderRenewDeadline = intPtr(0)
	f.leaderRetryPeriod = intPtr(0)
	f.bearerTrustHMACKeyFile = strPtr(keyFile)
	return f
}

// TestBuildKubeClientsReturnsUntypedNilWithoutACluster_spec_25_4 pins that
// every client buildKubeClients does not build is an untyped nil interface.
// A no-kubeconfig start and a clientset build failure return three nil
// interfaces that compare equal to nil; a buildable config returns all three
// clients.
//
// spec: 25.4 (The lenny-ops Service)
func TestBuildKubeClientsReturnsUntypedNilWithoutACluster_spec_25_4(t *testing.T) {
	for _, tc := range kubeConfigCases() {
		t.Run(tc.name, func(t *testing.T) {
			cs, dyn, apiext := buildKubeClients(tc.load)
			if got := cs != nil; got != tc.wantClients {
				t.Errorf("clientset present = %v, want %v (a typed nil reads as present)", got, tc.wantClients)
			}
			if got := dyn != nil; got != tc.wantClients {
				t.Errorf("dynamic client present = %v, want %v", got, tc.wantClients)
			}
			if got := apiext != nil; got != tc.wantClients {
				t.Errorf("apiextensions client present = %v, want %v", got, tc.wantClients)
			}
		})
	}
}

// TestOpsStartsWithoutKubeconfigWithAndWithoutSharedKey_spec_25_4 runs the
// lenny-ops composition root's dependency and event-stream build steps for
// every REST-config outcome, with and without the shared HMAC key the §25.5
// subscription_cache_invalidate RPC derives its token from. No combination
// panics. Without a cluster the clientset is an untyped nil, the K8s probe is
// not registered, and the leader elector is the noop follower; the invalidate
// token follows the key alone.
//
// Before the fix the no-kubeconfig case with a key panicked in
// buildWebhookDelivery, because the nil *kubernetes.Clientset stored in the
// kubernetes.Interface field passed its nil check.
//
// spec: 25.4 (The lenny-ops Service), 25.5 (Operational Event Stream)
// diagnosis: exercises the real composition-root steps; a panic or a
// present clientset without a cluster means a nil concrete clientset reached
// an interface field again.
func TestOpsStartsWithoutKubeconfigWithAndWithoutSharedKey_spec_25_4(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "hmac.key")
	if err := os.WriteFile(keyFile, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	for _, kc := range kubeConfigCases() {
		for _, key := range []struct {
			name string
			file string
		}{{"with_shared_key", keyFile}, {"without_shared_key", ""}} {
			t.Run(kc.name+"/"+key.name, func(t *testing.T) {
				prev := prometheus.DefaultRegisterer
				prometheus.DefaultRegisterer = prometheus.NewRegistry()
				t.Cleanup(func() { prometheus.DefaultRegisterer = prev })

				w := &opsWiring{f: opsDepsFlags(key.file)}
				w.kubeConfig = kc.load
				w.replicaID = "ops-a"
				w.ctx, w.stop = context.WithCancel(context.Background())
				t.Cleanup(w.stop)

				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("lenny-ops startup panicked: %v", r)
					}
				}()
				w.buildDependencies()
				w.buildEventStreamAndWebhooks()
				t.Cleanup(w.subscriptionCache.Stop)

				if got := w.clientset != nil; got != kc.wantClients {
					t.Errorf("clientset present = %v, want %v", got, kc.wantClients)
				}
				if _, ok := w.probes[opsservice.ProbeK8sAPI]; ok != kc.wantClients {
					t.Errorf("K8s probe registered = %v, want %v", ok, kc.wantClients)
				}
				if _, noop := w.elector.(noopElector); noop == kc.wantClients {
					t.Errorf("noop elector = %v, want %v", noop, !kc.wantClients)
				}
				if got := w.delivery.InvalidateToken != ""; got != (key.file != "") {
					t.Errorf("invalidate token present = %v, want %v", got, key.file != "")
				}
			})
		}
	}
}

// TestNilClientsetGuardedSites_spec_25_4 pins the other consumers of the
// lenny-ops clientset against a nil clientset interface: the webhook
// delivery build with a shared key, the OpsRoll ConfigMaps getter, the
// version aggregator, and the doctor service. Each degrades to its
// cluster-less behavior instead of calling through a nil clientset.
//
// spec: 25.4 (The lenny-ops Service), 25.5 (Operational Event Stream), 25.8
// (Version Aggregation)
func TestNilClientsetGuardedSites_spec_25_4(t *testing.T) {
	cs, _, apiext := buildKubeClients(func() (*rest.Config, error) { return nil, errNoKubeconfig })

	t.Run("webhook_delivery_with_shared_key", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		d := buildWebhookDelivery(ctx, webhookDeliveryDeps{Clientset: cs, SharedKey: []byte("k")})
		t.Cleanup(d.Cache.Stop)
		if d.InvalidateToken == "" {
			t.Error("invalidate token empty with a shared key")
		}
		if d.Subscriptions.OnChange == nil {
			t.Fatal("OnChange hook not installed")
		}
		// A CRUD change must not reach a broadcaster built over a nil
		// clientset.
		d.Subscriptions.OnChange(ctx, "sub-1")
	})
	t.Run("opsroll_configmaps_getter", func(t *testing.T) {
		if g := configMapsGetter(cs); g != nil {
			t.Errorf("configMapsGetter(nil clientset) = %v, want nil", g)
		}
	})
	t.Run("version_aggregator", func(t *testing.T) {
		agg := buildVersionAggregator("v1.0.0", "", nil, nil, cs, apiext, "lenny-system", "lenny")
		rep := agg.Aggregate(context.Background())
		for _, c := range rep.Components {
			if c.Name != "ops" {
				t.Errorf("component %q reported without a cluster or a gateway", c.Name)
			}
		}
	})
	t.Run("doctor_service", func(t *testing.T) {
		if svc := buildDoctorService(doctorDeps{Clientset: cs}); svc != nil {
			t.Errorf("doctor service built without a clientset: %v", svc)
		}
	})
}
