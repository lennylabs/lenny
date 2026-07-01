// SPDX-License-Identifier: MIT

package main

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/elicitationfloor"
)

// phaseStampFloorReader reads the §17.2 line 86
// security.elicitationContentIntegrity.floor key from the
// lenny-deployment-phase-stamp ConfigMap via the controller-runtime
// client. It implements elicitationfloor.FloorReader so the gateway's
// platform floor stays live across `helm upgrade` floor changes without a
// pod restart.
//
// spec: §17.2 line 86. F-17.2.9.
type phaseStampFloorReader struct {
	client    client.Client
	namespace string
	name      string
}

// ReadFloor returns (value, present, error). A missing ConfigMap or a
// ConfigMap without the floor key reports present=false (not an error) so
// the reconciler retains the last-known floor rather than weakening to a
// default.
func (rd phaseStampFloorReader) ReadFloor(ctx context.Context) (string, bool, error) {
	var cm corev1.ConfigMap
	key := client.ObjectKey{Namespace: rd.namespace, Name: rd.name}
	if err := rd.client.Get(ctx, key, &cm); err != nil {
		if apierrors.IsNotFound(err) {
			// No phase-stamp ConfigMap (e.g. the chart was not installed or
			// the gateway runs in a namespace without it). Not an error;
			// the startup flag value remains in force.
			return "", false, nil
		}
		return "", false, err
	}
	v, ok := cm.Data[elicitationfloor.ConfigMapDataKey]
	if !ok || v == "" {
		return "", false, nil
	}
	return v, true, nil
}
