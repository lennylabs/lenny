// SPDX-License-Identifier: MIT

// Command lenny-tier-promote runs the §17.8.3 tier-promotion gate
// against the live cluster. The operator invokes it as the pre-check
// (and the post-check) of a tier-promotion procedure; a non-zero exit
// blocks the promotion.
//
// The gate runs the chart-values diff, the deployed-replicas floor,
// the persistent-storage check, the operator's secret-encryption
// attestation, the audit-retention floor, and the §13.1/§13.2
// admission-webhook posture. See docs/runbooks/tier-promotion.md for
// the full operator procedure.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/tierpromotion"
)

func main() {
	from := flag.String("from", "", "source tier (tier1, tier2, or tier3)")
	to := flag.String("to", "", "target tier (tier1, tier2, or tier3)")
	namespace := flag.String("namespace", tierpromotion.DefaultNamespace,
		"release namespace holding the deployments")
	chartTier := flag.String("chart-values-tier", "",
		"capacityPlanning.tier value from the rendered chart (must equal --to)")
	retainDays := flag.Int("audit-retain-days", 0,
		"backups.retention.retainDays value from the rendered chart")
	secretEncryption := flag.Bool("secret-encryption-verified", false,
		"operator attestation that etcd Secret encryption-at-rest is configured (§4.9)")
	postgresExternal := flag.Bool("postgres-external", false,
		"set when Postgres is wired to a managed endpoint (RDS, Cloud SQL, Azure Database)")
	redisExternal := flag.Bool("redis-external", false,
		"set when Redis is wired to a managed endpoint (ElastiCache, Memorystore, Azure Cache)")
	flag.Parse()

	if *from == "" || *to == "" {
		log.Fatal("lenny-tier-promote: --from and --to are required")
	}
	fromT := tierpromotion.Tier(*from)
	toT := tierpromotion.Tier(*to)
	chartT := tierpromotion.Tier(*chartTier)

	cfg, err := ctrl.GetConfig()
	if err != nil {
		log.Fatalf("lenny-tier-promote: resolve cluster config: %v", err)
	}
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	cl, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Fatalf("lenny-tier-promote: build cluster client: %v", err)
	}

	gatherer := &tierpromotion.ClusterGatherer{
		Reader: cl,
		Options: tierpromotion.GatherOptions{
			Namespace:                *namespace,
			ChartValuesTier:          chartT,
			AuditRetainDays:          *retainDays,
			SecretEncryptionVerified: *secretEncryption,
			PostgresExternal:         *postgresExternal,
			RedisExternal:            *redisExternal,
		},
	}
	report, err := tierpromotion.Validate(context.Background(), gatherer, fromT, toT)
	if err != nil {
		log.Fatalf("lenny-tier-promote: validate: %v", err)
	}

	for _, c := range report {
		fmt.Fprintf(os.Stdout, "%-32s %-4s %s\n", c.Name, c.Status, c.Detail)
	}
	if !report.Passed() {
		log.Fatalf("lenny-tier-promote: %d gate(s) failed; promotion blocked", len(report.Failures()))
	}
	log.Print("lenny-tier-promote: all gates passed")
}
