// SPDX-License-Identifier: MIT

package openapi_test

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/blobstore/replication"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditretention"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorinvoke"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/impersonation"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admintoken"
	"github.com/lennylabs/lenny/pkg/gateway/operability/recommendations"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/upgrade/runtimeupgrade"
	"github.com/lennylabs/lenny/pkg/kms/rekey"
	"github.com/lennylabs/lenny/pkg/mtls"
	"github.com/lennylabs/lenny/pkg/preflight"
	"github.com/lennylabs/lenny/pkg/schemamigrate"
)

// The types below satisfy the admin-router manager interfaces that gate
// the non-store-backed admin routes, so the route-completeness walk in
// TestDocumentMatchesRegisteredEndpoints sees every conditionally-
// registered route. Their methods are never invoked — the walk only
// enumerates registered patterns — so each returns the zero value. One
// type per interface keeps the method sets from colliding by name (for
// example Status, Get, Start, Resume, Revoke each recur across several
// interfaces). A new manager interface that gates a route adds a type
// here and a With* call to the router wiring.

// saltRotatorStub satisfies admin.ErasureSaltRotator.
type saltRotatorStub struct{}

func (saltRotatorStub) RotateErasureSalt(context.Context, string) error { return nil }

// erasureRunnerStub satisfies admin.ErasureRunner.
type erasureRunnerStub struct{}

func (erasureRunnerStub) Start(context.Context, string, string) (string, error) { return "", nil }
func (erasureRunnerStub) Run(context.Context, string) error                     { return nil }

// impersonationStub satisfies admin.ImpersonationService.
type impersonationStub struct{}

func (impersonationStub) Issue(context.Context, impersonation.IssueRequest) (impersonation.Ticket, string, error) {
	return impersonation.Ticket{}, "", nil
}

func (impersonationStub) End(context.Context, string, string) (impersonation.Ticket, error) {
	return impersonation.Ticket{}, nil
}

func (impersonationStub) ListActive(context.Context) ([]impersonation.Ticket, error) { return nil, nil }

// migrationStub satisfies admin.MigrationManager.
type migrationStub struct{}

func (migrationStub) Status(context.Context) (schemamigrate.StatusReport, error) {
	return schemamigrate.StatusReport{}, nil
}

func (migrationStub) Down(context.Context, uint) (schemamigrate.DownResult, error) {
	return schemamigrate.DownResult{}, nil
}

// eventBufferStub satisfies admin.EventBufferQuerier.
type eventBufferStub struct{}

func (eventBufferStub) Query(uint64, events.EventFilter, int) events.BufferedEventPage {
	return events.BufferedEventPage{}
}

// sessionAdminStub satisfies admin.SessionAdmin.
type sessionAdminStub struct{}

func (sessionAdminStub) GetByID(context.Context, string) (sessionstore.Session, error) {
	return sessionstore.Session{}, nil
}

func (sessionAdminStub) ForceTerminate(context.Context, string) (sessionstore.Session, session.State, bool, error) {
	return sessionstore.Session{}, "", false, nil
}

// recommendationsStub satisfies admin.RecommendationService.
type recommendationsStub struct{}

func (recommendationsStub) GetRecommendations(context.Context, *string) (*recommendations.RecommendationsResponse, error) {
	return nil, nil
}

// caRotationStub satisfies admin.CARotationManager.
type caRotationStub struct{}

func (caRotationStub) Status(context.Context) (mtls.CARotationSnapshot, bool, error) {
	return mtls.CARotationSnapshot{}, false, nil
}

func (caRotationStub) Begin(context.Context, string) (mtls.CARotationSnapshot, error) {
	return mtls.CARotationSnapshot{}, nil
}

func (caRotationStub) Promote(context.Context) (mtls.CARotationSnapshot, error) {
	return mtls.CARotationSnapshot{}, nil
}

func (caRotationStub) Retire(context.Context) (mtls.CARotationSnapshot, error) {
	return mtls.CARotationSnapshot{}, nil
}

// runtimeUpgradeStub satisfies admin.RuntimeUpgradeManager.
type runtimeUpgradeStub struct{}

func (runtimeUpgradeStub) Start(context.Context, string, runtimeupgrade.StartOptions) (runtimeupgrade.Snapshot, error) {
	return runtimeupgrade.Snapshot{}, nil
}

func (runtimeUpgradeStub) Proceed(context.Context, string) (runtimeupgrade.Snapshot, error) {
	return runtimeupgrade.Snapshot{}, nil
}

func (runtimeUpgradeStub) Pause(context.Context, string, string) (runtimeupgrade.Snapshot, error) {
	return runtimeupgrade.Snapshot{}, nil
}

func (runtimeUpgradeStub) Resume(context.Context, string) (runtimeupgrade.Snapshot, error) {
	return runtimeupgrade.Snapshot{}, nil
}

func (runtimeUpgradeStub) Rollback(context.Context, string, bool) (runtimeupgrade.Snapshot, error) {
	return runtimeupgrade.Snapshot{}, nil
}

func (runtimeUpgradeStub) Status(context.Context, string) (runtimeupgrade.Snapshot, bool, error) {
	return runtimeupgrade.Snapshot{}, false, nil
}

// credentialRekeyStub satisfies admin.CredentialRekeyer.
type credentialRekeyStub struct{}

func (credentialRekeyStub) Run(context.Context, string) (rekey.Summary, error) {
	return rekey.Summary{}, nil
}
func (credentialRekeyStub) Verify(context.Context, string) (int, error) { return 0, nil }

// connectorRefreshStub satisfies admin.ConnectorCapabilityRefresher.
type connectorRefreshStub struct{}

func (connectorRefreshStub) RefreshCapabilities(context.Context, string, string, string, string) (connectorinvoke.CapabilityRefreshResult, error) {
	return connectorinvoke.CapabilityRefreshResult{}, nil
}

// reconciliationResumerStub satisfies admin.ReconciliationResumer.
type reconciliationResumerStub struct{}

func (reconciliationResumerStub) ResumePoolReconciliation(context.Context, string) (int, error) {
	return 0, nil
}

// leaseDenialStub satisfies admin.LeaseDenialClearer.
type leaseDenialStub struct{}

func (leaseDenialStub) ClearSubtreeDenial(context.Context, string, string) (bool, error) {
	return false, nil
}

// quotaReconcilerStub satisfies admin.QuotaReconciler.
type quotaReconcilerStub struct{}

func (quotaReconcilerStub) Reconcile(context.Context, admin.QuotaReconcileScope) (admin.QuotaReconcileResult, error) {
	return admin.QuotaReconcileResult{}, nil
}

// preflighterStub satisfies admin.InfraPreflighter.
type preflighterStub struct{}

func (preflighterStub) Preflight(context.Context) []preflight.CheckResult { return nil }

// adminTokenStub satisfies admin.AdminTokenProvisioner.
type adminTokenStub struct{}

func (adminTokenStub) Provision(context.Context) (admintoken.Result, error) {
	return admintoken.Result{}, nil
}

func (adminTokenStub) Rotate(context.Context) (admintoken.Result, error) {
	return admintoken.Result{}, nil
}
func (adminTokenStub) Username() string            { return "" }
func (adminTokenStub) SecretRef() (string, string) { return "", "" }

// issuedTokenStub satisfies admin.IssuedTokenRevoker.
type issuedTokenStub struct{}

func (issuedTokenStub) Revoke(context.Context, string, string, string, time.Time) error { return nil }

// revocationCacheStub satisfies admin.RevocationCache.
type revocationCacheStub struct{}

func (revocationCacheStub) Revoke(string) {}

// artifactReplicationStub satisfies admin.ArtifactReplicationController.
type artifactReplicationStub struct{}

func (artifactReplicationStub) Resume(context.Context, string, string, string) error { return nil }
func (artifactReplicationStub) GetState(context.Context, string) (replication.RegionState, bool, error) {
	return replication.RegionState{}, false, nil
}

// artifactLegalHoldStub satisfies admin.ArtifactLegalHolder.
type artifactLegalHoldStub struct{}

func (artifactLegalHoldStub) Get(context.Context, string) (artifactcatalog.Record, error) {
	return artifactcatalog.Record{}, nil
}

func (artifactLegalHoldStub) SetLegalHold(context.Context, string, bool, string, time.Time, string) error {
	return nil
}

func (artifactLegalHoldStub) IsLegalHeldAt(context.Context, string, string) (bool, error) {
	return false, nil
}

func (artifactLegalHoldStub) ListLegalHeld(context.Context, string) ([]artifactcatalog.Record, error) {
	return nil, nil
}

// auditPrunerStub satisfies admin.AuditPartitionDropper.
type auditPrunerStub struct{}

func (auditPrunerStub) ForceDrop(context.Context, string, string, time.Time) (auditretention.ForceDropResult, error) {
	return auditretention.ForceDropResult{}, nil
}
