// SPDX-License-Identifier: MIT

package replication

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sync"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/replication"
)

// Driver is the §25.11 MinIO-facing seam of the ArtifactStore
// replication subsystem. The Controller drives it; the orchestration
// logic — the residency preflight, the suspend/resume state machine —
// is tested against FakeDriver without a MinIO cluster.
//
// ConfigureReplication establishes continuous replication on the source
// bucket. SuspendReplication and ResumeReplication are the §25.11 mc
// replicate disable / enable equivalents. ProbeJurisdiction issues the
// §25.11 s3:GetBucketTagging probe against the destination and returns
// the lenny.dev/jurisdiction-region tag. ResolveEndpointIPs resolves
// the destination hostname for the §25.11 DNS-rebinding guard.
type Driver interface {
	// ConfigureReplication establishes the §25.11 continuous replication
	// rule on the source bucket: replicate object PUT, object DELETE, and
	// (when versioning is enabled) version history to the destination.
	ConfigureReplication(ctx context.Context, rc RegionConfig) error
	// SuspendReplication places the source bucket's replication into a
	// suspended state (the §25.11 mc replicate disable equivalent).
	SuspendReplication(ctx context.Context, rc RegionConfig) error
	// ResumeReplication re-enables suspended replication.
	ResumeReplication(ctx context.Context, rc RegionConfig) error
	// ProbeJurisdiction reads the destination bucket's
	// lenny.dev/jurisdiction-region tag (the §25.11 s3:GetBucketTagging
	// probe). It returns ("", false, nil) when the tag is absent and an
	// error when the probe itself fails.
	ProbeJurisdiction(ctx context.Context, t Target) (tag string, present bool, err error)
	// ResolveEndpointIPs resolves the destination endpoint hostname to
	// its IP addresses, for the §25.11 DNS-rebinding guard.
	ResolveEndpointIPs(ctx context.Context, endpoint string) ([]net.IP, error)
}

// MinIODriver is the production §25.11 Driver. It configures
// replication on a self-managed MinIO cluster via the minio-go bucket-
// replication API and probes a destination bucket's jurisdiction tag
// via s3:GetBucketTagging. For a cloud object store the equivalent
// provider-native replication is configured on the provider at install
// time; MinIODriver covers the self-managed MinIO profile §25.11 names
// as the default.
type MinIODriver struct {
	// source is the MinIO client for the source cluster (the cluster
	// holding the ArtifactStore bucket).
	source *minio.Client
	// destClientFor builds a MinIO client for a destination Target. It
	// is a field so a deployment can resolve the destination credentials
	// from the AccessCredentialSecret however it provisions secrets.
	destClientFor func(Target) (*minio.Client, error)
	// replicationRoleARN is the replication Role ARN the source MinIO
	// cluster requires on a replication rule (the arn:minio:replication
	// or arn:aws:iam form).
	replicationRoleARN string
}

var _ Driver = (*MinIODriver)(nil)

// MinIODriverConfig configures a MinIODriver.
type MinIODriverConfig struct {
	// Source is the MinIO client for the source cluster. Required.
	Source *minio.Client
	// DestClientFor builds a MinIO client for a destination Target.
	// Required: it resolves the destination's AccessCredentialSecret.
	DestClientFor func(Target) (*minio.Client, error)
	// ReplicationRoleARN is the replication Role ARN the source cluster
	// requires on a replication rule.
	ReplicationRoleARN string
}

// NewMinIODriver builds a §25.11 MinIODriver. It returns an error when
// a required dependency is missing.
func NewMinIODriver(cfg MinIODriverConfig) (*MinIODriver, error) {
	if cfg.Source == nil {
		return nil, errors.New("replication: MinIODriver requires a source client")
	}
	if cfg.DestClientFor == nil {
		return nil, errors.New("replication: MinIODriver requires a destination client builder")
	}
	return &MinIODriver{
		source:             cfg.Source,
		destClientFor:      cfg.DestClientFor,
		replicationRoleARN: cfg.ReplicationRoleARN,
	}, nil
}

// replicationRuleID is the stable rule ID Lenny's replication rule
// carries on a source bucket, so ConfigureReplication is idempotent and
// Suspend/Resume can locate the rule.
const replicationRuleID = "lenny-artifact-backup"

// ConfigureReplication implements Driver. It adds (or replaces) the
// §25.11 replication rule on the source bucket: enabled, full-bucket
// prefix, replicating versioned deletes and delete markers so an
// erasure delete propagates to the destination.
func (d *MinIODriver) ConfigureReplication(ctx context.Context, rc RegionConfig) error {
	cfg, err := d.source.GetBucketReplication(ctx, rc.SourceBucket)
	if err != nil {
		return fmt.Errorf("replication: read source replication for %s: %w", rc.SourceBucket, err)
	}
	opts := replication.Options{
		Op:                      replication.AddOption,
		ID:                      replicationRuleID,
		RuleStatus:              "enable",
		Priority:                "1",
		DestBucket:              arnForBucket(rc.Target.Bucket),
		ReplicateDeletes:        "enable",
		ReplicateDeleteMarkers:  "enable",
		ExistingObjectReplicate: "enable",
	}
	if d.replicationRoleARN != "" {
		opts.RoleArn = d.replicationRoleARN
	}
	// AddRule replaces a rule of the same ID, so a re-render is
	// idempotent.
	if err := cfg.AddRule(opts); err != nil {
		return fmt.Errorf("replication: build rule for %s: %w", rc.SourceBucket, err)
	}
	if err := d.source.SetBucketReplication(ctx, rc.SourceBucket, cfg); err != nil {
		return fmt.Errorf("replication: set replication for %s: %w", rc.SourceBucket, err)
	}
	return nil
}

// SuspendReplication implements Driver: it switches the Lenny
// replication rule to disabled, the §25.11 mc replicate disable
// equivalent.
func (d *MinIODriver) SuspendReplication(ctx context.Context, rc RegionConfig) error {
	return d.setRuleStatus(ctx, rc, "disable")
}

// ResumeReplication implements Driver: it switches the Lenny
// replication rule back to enabled.
func (d *MinIODriver) ResumeReplication(ctx context.Context, rc RegionConfig) error {
	return d.setRuleStatus(ctx, rc, "enable")
}

// setRuleStatus edits the Lenny replication rule's status on the source
// bucket.
func (d *MinIODriver) setRuleStatus(ctx context.Context, rc RegionConfig, status string) error {
	cfg, err := d.source.GetBucketReplication(ctx, rc.SourceBucket)
	if err != nil {
		return fmt.Errorf("replication: read source replication for %s: %w", rc.SourceBucket, err)
	}
	if err := cfg.EditRule(replication.Options{
		Op:         replication.SetOption,
		ID:         replicationRuleID,
		RuleStatus: status,
	}); err != nil {
		return fmt.Errorf("replication: edit rule status for %s: %w", rc.SourceBucket, err)
	}
	if err := d.source.SetBucketReplication(ctx, rc.SourceBucket, cfg); err != nil {
		return fmt.Errorf("replication: set replication for %s: %w", rc.SourceBucket, err)
	}
	return nil
}

// ProbeJurisdiction implements Driver: it reads the destination
// bucket's lenny.dev/jurisdiction-region tag via s3:GetBucketTagging.
func (d *MinIODriver) ProbeJurisdiction(ctx context.Context, t Target) (string, bool, error) {
	dest, err := d.destClientFor(t)
	if err != nil {
		return "", false, fmt.Errorf("replication: build destination client: %w", err)
	}
	tagSet, err := dest.GetBucketTagging(ctx, t.Bucket)
	if err != nil {
		return "", false, fmt.Errorf("replication: probe destination tagging for %s: %w", t.Bucket, err)
	}
	for k, v := range tagSet.ToMap() {
		if k == jurisdictionTagKey {
			return v, true, nil
		}
	}
	return "", false, nil
}

// ResolveEndpointIPs implements Driver: it resolves the destination
// endpoint hostname to its IP addresses.
func (d *MinIODriver) ResolveEndpointIPs(ctx context.Context, endpoint string) ([]net.IP, error) {
	host := endpointHost(endpoint)
	if host == "" {
		return nil, fmt.Errorf("replication: cannot parse host from endpoint %q", endpoint)
	}
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("replication: resolve %s: %w", host, err)
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}
	return ips, nil
}

// arnForBucket renders a destination-bucket ARN minio-go's replication
// API expects.
func arnForBucket(bucket string) string {
	return "arn:aws:s3:::" + bucket
}

// endpointHost extracts the host from a replication endpoint, which may
// be a bare host:port or a full URL.
func endpointHost(endpoint string) string {
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		return u.Hostname()
	}
	host, _, err := net.SplitHostPort(endpoint)
	if err == nil {
		return host
	}
	return endpoint
}

// FakeDriver is an in-memory §25.11 Driver for tests. It records the
// replication state per region and serves a configurable jurisdiction
// tag and resolved IP set, so the residency preflight and the
// suspend/resume state machine are exercised without a MinIO cluster.
type FakeDriver struct {
	mu sync.Mutex
	// configured records whether ConfigureReplication ran for a region.
	configured map[string]bool
	// suspended records whether a region's replication is suspended.
	suspended map[string]bool
	// jurisdictionTag is the tag ProbeJurisdiction returns for a
	// destination bucket, keyed by bucket name.
	jurisdictionTag map[string]string
	// tagAbsent marks a destination bucket as having no jurisdiction
	// tag.
	tagAbsent map[string]bool
	// probeErr, when set for a bucket, makes ProbeJurisdiction fail.
	probeErr map[string]error
	// resolvedIPs is the IP set ResolveEndpointIPs returns for an
	// endpoint.
	resolvedIPs map[string][]net.IP
}

var _ Driver = (*FakeDriver)(nil)

// NewFakeDriver returns an empty FakeDriver.
func NewFakeDriver() *FakeDriver {
	return &FakeDriver{
		configured:      map[string]bool{},
		suspended:       map[string]bool{},
		jurisdictionTag: map[string]string{},
		tagAbsent:       map[string]bool{},
		probeErr:        map[string]error{},
		resolvedIPs:     map[string][]net.IP{},
	}
}

// ConfigureReplication implements Driver.
func (f *FakeDriver) ConfigureReplication(_ context.Context, rc RegionConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.configured[rc.Region] = true
	f.suspended[rc.Region] = false
	return nil
}

// SuspendReplication implements Driver.
func (f *FakeDriver) SuspendReplication(_ context.Context, rc RegionConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.suspended[rc.Region] = true
	return nil
}

// ResumeReplication implements Driver.
func (f *FakeDriver) ResumeReplication(_ context.Context, rc RegionConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.suspended[rc.Region] = false
	return nil
}

// ProbeJurisdiction implements Driver.
func (f *FakeDriver) ProbeJurisdiction(_ context.Context, t Target) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.probeErr[t.Bucket]; err != nil {
		return "", false, err
	}
	if f.tagAbsent[t.Bucket] {
		return "", false, nil
	}
	tag, ok := f.jurisdictionTag[t.Bucket]
	return tag, ok, nil
}

// ResolveEndpointIPs implements Driver.
func (f *FakeDriver) ResolveEndpointIPs(_ context.Context, endpoint string) ([]net.IP, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ips, ok := f.resolvedIPs[endpointHost(endpoint)]
	if !ok {
		// Default to a loopback address so a test that does not exercise
		// the CIDR guard still resolves.
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	return ips, nil
}

// SetJurisdictionTag sets the tag FakeDriver's ProbeJurisdiction
// returns for a destination bucket.
func (f *FakeDriver) SetJurisdictionTag(bucket, tag string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jurisdictionTag[bucket] = tag
	delete(f.tagAbsent, bucket)
}

// SetTagAbsent marks a destination bucket as having no jurisdiction
// tag.
func (f *FakeDriver) SetTagAbsent(bucket string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tagAbsent[bucket] = true
	delete(f.jurisdictionTag, bucket)
}

// SetProbeError makes FakeDriver's ProbeJurisdiction fail for a
// destination bucket.
func (f *FakeDriver) SetProbeError(bucket string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probeErr[bucket] = err
}

// SetResolvedIPs sets the IP set FakeDriver's ResolveEndpointIPs
// returns for an endpoint host.
func (f *FakeDriver) SetResolvedIPs(endpoint string, ips ...net.IP) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolvedIPs[endpointHost(endpoint)] = ips
}

// IsSuspended reports whether a region's replication is suspended in
// the FakeDriver.
func (f *FakeDriver) IsSuspended(region string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.suspended[region]
}

// IsConfigured reports whether ConfigureReplication ran for a region.
func (f *FakeDriver) IsConfigured(region string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.configured[region]
}
