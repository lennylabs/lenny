// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// k8sPodLogReader backs the §25.4 log-proxy endpoint with the Kubernetes
// pod-log API. It satisfies opsserver.PodLogReader.
//
// spec: §25.4 lines 2528-2534.
type k8sPodLogReader struct {
	pods corev1client.PodsGetter
}

// ReadPodLogs streams the named pod's container logs. A not-found pod is
// translated to opsserver.ErrPodLogNotFound so the handler returns the
// §25.2 404 POD_NOT_FOUND envelope.
func (r k8sPodLogReader) ReadPodLogs(ctx context.Context, namespace, name string, opts opsserver.PodLogOptions) (io.ReadCloser, error) {
	req := r.pods.Pods(namespace).GetLogs(name, &corev1.PodLogOptions{
		Container:    opts.Container,
		Previous:     opts.Previous,
		SinceSeconds: opts.SinceSeconds,
		TailLines:    opts.TailLines,
	})
	stream, err := req.Stream(ctx)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("%w: %v", opsserver.ErrPodLogNotFound, err)
		}
		return nil, err
	}
	return stream, nil
}
