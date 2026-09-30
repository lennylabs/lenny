// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"

	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
)

// leaseExtensionAuditAdapter implements leasecontrol.Auditor, turning
// each ExtensionAudit record into a §11.7 hash-chained audit row
// keyed on the request tenant. The event type is the spec-listed
// `delegation.lease_extended`; the payload carries every §8.6 field so a forensic reconstruction can identify the requesting
// session, the approval mode and approver, the per-batch grouping,
// the issuing replica, and the client originator. F-8.6.10.
// spec: §8.6
type leaseExtensionAuditAdapter struct {
	appender policy.AuditAppender
}

func (a leaseExtensionAuditAdapter) RecordExtension(ctx context.Context, e leasecontrol.ExtensionAudit) {
	if a.appender == nil {
		return
	}
	payload := map[string]any{
		"session_id":      e.RequestSessionID,
		"root_session_id": e.RootSessionID,
		// §8.6 requested/granted amounts across every extendable
		// dimension, not just tokens. F-8.6.1.
		"requested_tokens":            e.Requested.Tokens,
		"granted_tokens":              e.Granted.Tokens,
		"requested_seconds":           e.Requested.Seconds,
		"granted_seconds":             e.Granted.Seconds,
		"requested_children":          e.Requested.Children,
		"granted_children":            e.Granted.Children,
		"requested_parallel_children": e.Requested.ParallelChildren,
		"granted_parallel_children":   e.Granted.ParallelChildren,
		"requested_tree_size":         e.Requested.TreeSize,
		"granted_tree_size":           e.Granted.TreeSize,
		"requested_file_export_files": e.Requested.FileExportFiles,
		"granted_file_export_files":   e.Granted.FileExportFiles,
		"requested_file_export_bytes": e.Requested.FileExportBytes,
		"granted_file_export_bytes":   e.Granted.FileExportBytes,
		"effective_max":               e.EffectiveMax,
		"outcome":                     string(e.Outcome),
		"approval_mode":               string(e.ApprovalMode),
		"approver":                    e.Approver,
		"batch_id":                    e.BatchID,
		"service_instance_id":         e.ServiceInstanceID,
		"client_ip":                   e.ClientIP,
		"new_limits": map[string]any{
			"token_budget":      e.NewLimits.Tokens,
			"max_age_seconds":   e.NewLimits.Seconds,
			"children":          e.NewLimits.Children,
			"parallel_children": e.NewLimits.ParallelChildren,
			"tree_size":         e.NewLimits.TreeSize,
			"file_export_files": e.NewLimits.FileExportFiles,
			"file_export_bytes": e.NewLimits.FileExportBytes,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = a.appender.Append(ctx, e.TenantID, "delegation.lease_extended", json.RawMessage(data), clockinject.Now().UTC())
}

// RecordAutoRateLimitExceeded emits the §8.6
// `delegation.lease_extension_auto_rate_limit_exceeded` audit row when an
// auto-mode extension request trips the tree's maxAutoExtensionsPerMinute
// and the gateway falls back to elicitation for the remainder of the
// window. F-8.6.7.
// spec: §8.6
func (a leaseExtensionAuditAdapter) RecordAutoRateLimitExceeded(ctx context.Context, e leasecontrol.AutoRateLimitAudit) {
	if a.appender == nil {
		return
	}
	payload := map[string]any{
		"session_id":          e.RequestSessionID,
		"root_session_id":     e.RootSessionID,
		"max_per_minute":      e.MaxPerMinute,
		"service_instance_id": e.ServiceInstanceID,
		"client_ip":           e.ClientIP,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = a.appender.Append(ctx, e.TenantID, "delegation.lease_extension_auto_rate_limit_exceeded", json.RawMessage(data), clockinject.Now().UTC())
}
