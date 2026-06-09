// SPDX-License-Identifier: MIT

package legalholdescrow

import (
	"context"
	"errors"
	"testing"
	"time"
)

// spec: §12.8 sub-step 2, line 883 — region resolution.
func TestConfigResolve_spec_12_8(t *testing.T) {
	cfg := Config{
		Default: &RegionEscrow{Bucket: "escrow-default", Endpoint: "minio:9000"},
		Regions: map[string]RegionEscrow{
			"eu": {Bucket: "escrow-eu", Endpoint: "eu-minio:9000"},
		},
	}
	t.Run("unscoped resolves to single-region default", func(t *testing.T) {
		region, esc, err := cfg.Resolve("")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if region != DefaultRegion {
			t.Fatalf("region = %q, want %q", region, DefaultRegion)
		}
		if esc.Bucket != "escrow-default" {
			t.Fatalf("bucket = %q", esc.Bucket)
		}
	})
	t.Run("scoped tenant resolves to its region", func(t *testing.T) {
		region, esc, err := cfg.Resolve("eu")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if region != "eu" || esc.Bucket != "escrow-eu" {
			t.Fatalf("got region=%q bucket=%q", region, esc.Bucket)
		}
	})
	t.Run("scoped tenant with unconfigured region fails closed", func(t *testing.T) {
		// §12.8 line 883: no fallback to the default for a residency-scoped
		// tenant — routing EU evidence to a US default bucket is the
		// cross-border transfer the spec forbids.
		_, _, err := cfg.Resolve("ap-south")
		if !errors.Is(err, ErrRegionUnresolvable) {
			t.Fatalf("err = %v, want ErrRegionUnresolvable", err)
		}
	})
}

// spec: §12.8 line 883 — a deployment with no escrow config fails closed.
func TestConfigResolveNoConfig_spec_12_8_883(t *testing.T) {
	var cfg Config
	if cfg.Configured() {
		t.Fatal("empty config reports Configured")
	}
	_, _, err := cfg.Resolve("")
	if !errors.Is(err, ErrRegionUnresolvable) {
		t.Fatalf("err = %v, want ErrRegionUnresolvable", err)
	}
}

func TestKEKAliasAndKey_spec_12_8(t *testing.T) {
	if got := KEKAlias("eu"); got != "platform:legal_hold_escrow:eu" {
		t.Fatalf("KEKAlias = %q", got)
	}
	if got := KEKAlias(DefaultRegion); got != "platform:legal_hold_escrow:default" {
		t.Fatalf("default KEKAlias = %q", got)
	}
	if got := EscrowObjectKey("acme", "artifact", "sess-1"); got != "legal-hold-escrow/acme/artifact/sess-1" {
		t.Fatalf("EscrowObjectKey = %q", got)
	}
}

// --- migrator test doubles ---

type fakeCipher struct{ alias string }

func (c fakeCipher) Seal(_ context.Context, pt []byte) ([]byte, error) {
	// A faithful stand-in: prefix the plaintext with the KEK alias so a
	// test can assert the payload was sealed under the escrow KEK.
	return append([]byte(c.alias+"|"), pt...), nil
}

type fakeSource struct {
	data map[string][]byte
	err  error
}

func (s fakeSource) Read(_ context.Context, a HeldArtifact) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	b, ok := s.data[a.BlobURI]
	if !ok {
		return nil, errors.New("not found")
	}
	return b, nil
}

type fakeEscrow struct {
	written map[string][]byte
	err     error
}

func (e *fakeEscrow) Write(_ context.Context, key string, sealed []byte) error {
	if e.err != nil {
		return e.err
	}
	if e.written == nil {
		e.written = map[string][]byte{}
	}
	e.written[key] = sealed
	return nil
}

type fakeLedger struct {
	resolved []RegionResolved
	escrowed []Escrowed
	regErr   error
	escErr   error
}

func (l *fakeLedger) RegionResolved(_ context.Context, ev RegionResolved) error {
	if l.regErr != nil {
		return l.regErr
	}
	l.resolved = append(l.resolved, ev)
	return nil
}

func (l *fakeLedger) Escrowed(_ context.Context, ev Escrowed) error {
	if l.escErr != nil {
		return l.escErr
	}
	l.escrowed = append(l.escrowed, ev)
	return nil
}

func newMigrator(src SourceReader, esc EscrowWriter, led Ledger, cfg Config) *Migrator {
	return &Migrator{
		Config: cfg,
		Cipher: func(alias string) (Cipher, error) { return fakeCipher{alias: alias}, nil },
		Source: src,
		Escrow: esc,
		Ledger: led,
		Clock:  func() time.Time { return time.Unix(1700000000, 0).UTC() },
	}
}

func defaultCfg() Config {
	return Config{Default: &RegionEscrow{Bucket: "escrow-default", Endpoint: "minio:9000"}}
}

// spec: §12.8 sub-steps 2-4 — the override migration happy path.
func TestMigrateHappyPath_spec_12_8(t *testing.T) {
	src := fakeSource{data: map[string][]byte{
		"lenny-blob://acme/artifact/sess-1/wf": []byte("held-bytes"),
	}}
	esc := &fakeEscrow{}
	led := &fakeLedger{}
	m := newMigrator(src, esc, led, defaultCfg())

	res, err := m.Migrate(context.Background(), Input{
		TenantID: "acme",
		JobID:    "job-1",
		Holds: []HeldArtifact{{
			ResourceType: "artifact",
			ResourceID:   "sess-1",
			BlobURI:      "lenny-blob://acme/artifact/sess-1/wf",
			HoldSetAt:    time.Unix(1699990000, 0).UTC(),
		}},
	})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if res.ResolvedRegion != DefaultRegion {
		t.Fatalf("region = %q", res.ResolvedRegion)
	}
	if res.EscrowKEKID != "platform:legal_hold_escrow:default" {
		t.Fatalf("kek = %q", res.EscrowKEKID)
	}
	wantKey := "legal-hold-escrow/acme/artifact/sess-1"
	if len(res.EscrowObjectKeys) != 1 || res.EscrowObjectKeys[0] != wantKey {
		t.Fatalf("keys = %v", res.EscrowObjectKeys)
	}
	// Sealed under the escrow KEK, not the tenant KEK.
	sealed := esc.written[wantKey]
	if string(sealed) != "platform:legal_hold_escrow:default|held-bytes" {
		t.Fatalf("sealed payload = %q", sealed)
	}
	if len(led.resolved) != 1 || led.resolved[0].ResolvedRegion != DefaultRegion {
		t.Fatalf("region-resolved ledger = %+v", led.resolved)
	}
	if len(led.escrowed) != 1 {
		t.Fatalf("escrowed ledger count = %d", len(led.escrowed))
	}
	e := led.escrowed[0]
	if e.EscrowObjectKey != wantKey || e.EscrowKEKID != res.EscrowKEKID || e.TenantDeleteJob != "job-1" {
		t.Fatalf("escrowed event = %+v", e)
	}
	if !e.OriginalHoldSet.Equal(time.Unix(1699990000, 0).UTC()) {
		t.Fatalf("original hold-set = %v", e.OriginalHoldSet)
	}
}

// spec: §12.8 line 883 — region-unresolvable aborts before any write.
func TestMigrateRegionUnresolvable_spec_12_8_883(t *testing.T) {
	esc := &fakeEscrow{}
	led := &fakeLedger{}
	m := newMigrator(fakeSource{}, esc, led, Config{}) // no escrow config

	_, err := m.Migrate(context.Background(), Input{
		TenantID: "acme",
		Holds:    []HeldArtifact{{ResourceType: "artifact", ResourceID: "s", BlobURI: "u"}},
	})
	if !errors.Is(err, ErrRegionUnresolvable) {
		t.Fatalf("err = %v, want ErrRegionUnresolvable", err)
	}
	if len(esc.written) != 0 || len(led.resolved) != 0 || len(led.escrowed) != 0 {
		t.Fatal("no escrow ciphertext or ledger row may be written when the region is unresolvable")
	}
}

// spec: §12.8 sub-step 1 — the override path is entered only with holds.
func TestMigrateNoHolds(t *testing.T) {
	m := newMigrator(fakeSource{}, &fakeEscrow{}, &fakeLedger{}, defaultCfg())
	_, err := m.Migrate(context.Background(), Input{TenantID: "acme"})
	if !errors.Is(err, ErrNoHolds) {
		t.Fatalf("err = %v, want ErrNoHolds", err)
	}
}

// A held resource with no blob body (a session row) is skipped for
// re-encryption; the hold itself stays set.
func TestMigrateBloblessResourceSkipped_spec_12_8(t *testing.T) {
	esc := &fakeEscrow{}
	led := &fakeLedger{}
	m := newMigrator(fakeSource{}, esc, led, defaultCfg())
	res, err := m.Migrate(context.Background(), Input{
		TenantID: "acme",
		Holds:    []HeldArtifact{{ResourceType: "session", ResourceID: "sess-1"}}, // BlobURI empty
	})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(res.EscrowObjectKeys) != 0 {
		t.Fatalf("keys = %v, want none", res.EscrowObjectKeys)
	}
	// Region-resolved still records (the residency decision was made).
	if len(led.resolved) != 1 {
		t.Fatalf("region-resolved = %d", len(led.resolved))
	}
	if len(led.escrowed) != 0 {
		t.Fatalf("escrowed = %d, want 0", len(led.escrowed))
	}
}

// spec: §12.8 — the override must not silently drop held evidence: a
// source read / seal / write / ledger failure aborts the migration.
func TestMigrateFailClosedPaths_spec_12_8(t *testing.T) {
	hold := HeldArtifact{ResourceType: "artifact", ResourceID: "s", BlobURI: "u"}
	src := fakeSource{data: map[string][]byte{"u": []byte("x")}}

	t.Run("source read error aborts", func(t *testing.T) {
		m := newMigrator(fakeSource{err: errors.New("boom")}, &fakeEscrow{}, &fakeLedger{}, defaultCfg())
		if _, err := m.Migrate(context.Background(), Input{TenantID: "acme", Holds: []HeldArtifact{hold}}); err == nil {
			t.Fatal("want error on source read failure")
		}
	})
	t.Run("escrow write error aborts", func(t *testing.T) {
		esc := &fakeEscrow{err: errors.New("disk full")}
		led := &fakeLedger{}
		m := newMigrator(src, esc, led, defaultCfg())
		if _, err := m.Migrate(context.Background(), Input{TenantID: "acme", Holds: []HeldArtifact{hold}}); err == nil {
			t.Fatal("want error on escrow write failure")
		}
		if len(led.escrowed) != 0 {
			t.Fatal("no escrowed ledger row when the escrow write failed")
		}
	})
	t.Run("ledger escrowed error aborts", func(t *testing.T) {
		m := newMigrator(src, &fakeEscrow{}, &fakeLedger{escErr: errors.New("pg down")}, defaultCfg())
		if _, err := m.Migrate(context.Background(), Input{TenantID: "acme", Holds: []HeldArtifact{hold}}); err == nil {
			t.Fatal("want error on ledger failure")
		}
	})
}

// spec: §12.8 "the segregation is idempotent" — a re-entered migration
// re-writes the same keys and re-records the ledger without error.
func TestMigrateIdempotent_spec_12_8(t *testing.T) {
	src := fakeSource{data: map[string][]byte{"u": []byte("held")}}
	esc := &fakeEscrow{}
	led := &fakeLedger{}
	m := newMigrator(src, esc, led, defaultCfg())
	in := Input{TenantID: "acme", Holds: []HeldArtifact{{ResourceType: "artifact", ResourceID: "s", BlobURI: "u"}}}

	if _, err := m.Migrate(context.Background(), in); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if _, err := m.Migrate(context.Background(), in); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if len(esc.written) != 1 {
		t.Fatalf("escrow object count = %d, want 1 (idempotent overwrite)", len(esc.written))
	}
}
