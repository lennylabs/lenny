// SPDX-License-Identifier: MIT

package opsserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

// ErrPodLogNotFound is returned by a PodLogReader when the named pod or
// its container does not exist, so the handler maps the failure to a
// §25.2 404 POD_NOT_FOUND without the opsserver package importing the
// Kubernetes apierrors package. The cmd/lenny-ops adapter wraps the
// client-go not-found error with this sentinel.
var ErrPodLogNotFound = errors.New("pod not found")

// PodLogOptions are the §25.4 line 2532 pod-log query parameters
// (?container=, ?since=, ?tail=, ?previous=) resolved to the Kubernetes
// pod-log API shape.
type PodLogOptions struct {
	// Container selects a container in a multi-container pod. Empty lets
	// the API server pick the default container.
	Container string
	// SinceSeconds, when non-nil, returns logs newer than this many
	// seconds. Mirrors the K8s PodLogOptions.SinceSeconds field.
	SinceSeconds *int64
	// TailLines, when non-nil, returns only the last N lines.
	TailLines *int64
	// Previous returns the logs of the previous terminated container
	// instance (the ?previous=true crash-investigation path).
	Previous bool
}

// PodLogReader streams a pod's container logs via the Kubernetes API. The
// cmd/lenny-ops adapter backs it with client-go's
// CoreV1().Pods(ns).GetLogs; a nil reader leaves the §25.4 log-proxy
// endpoint reporting the Kubernetes API unavailable. It returns
// ErrPodLogNotFound when the pod or container is unknown.
//
// spec: §25.4 lines 2528-2534.
type PodLogReader interface {
	ReadPodLogs(ctx context.Context, namespace, name string, opts PodLogOptions) (io.ReadCloser, error)
}

// registerLogRoutes wires the §25.4 line 2532 log-proxy endpoint onto the
// Server's mux.
func (s *Server) registerLogRoutes() {
	s.mux.HandleFunc("GET /v1/admin/logs/pods/{namespace}/{name}", s.handleGetPodLogs)
}

// handleGetPodLogs serves GET /v1/admin/logs/pods/{namespace}/{name}: the
// §25.4 convenience endpoint that proxies container logs from the
// Kubernetes pod-log API so agents do not need kubectl access. The
// response body is the raw log stream as text/plain; the query parameters
// ?container=, ?since=, ?tail=, and ?previous= map onto the K8s
// PodLogOptions.
//
// spec: §25.4 lines 2528-2534.
func (s *Server) handleGetPodLogs(w http.ResponseWriter, r *http.Request) {
	if s.podLogs == nil {
		conventions.WriteError(w, http.StatusServiceUnavailable, "LOG_PROXY_UNAVAILABLE",
			conventions.CategoryTransient, "the pod-log proxy is not configured (no Kubernetes API connection)")
		return
	}
	namespace := r.PathValue("namespace")
	name := r.PathValue("name")
	if namespace == "" || name == "" {
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, "namespace and pod name are required")
		return
	}
	opts, err := parsePodLogParams(r.URL.Query())
	if err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, err.Error())
		return
	}
	stream, err := s.podLogs.ReadPodLogs(r.Context(), namespace, name, opts)
	if err != nil {
		if errors.Is(err, ErrPodLogNotFound) {
			conventions.WriteError(w, http.StatusNotFound, "POD_NOT_FOUND",
				conventions.CategoryPermanent, "no pod "+namespace+"/"+name)
			return
		}
		// The Kubernetes API rejected or could not serve the request (RBAC,
		// connection, container not yet started). Surface it as a transient
		// upstream failure rather than a 500.
		conventions.WriteError(w, http.StatusBadGateway, "LOG_PROXY_ERROR",
			conventions.CategoryTransient, "the Kubernetes pod-log API returned an error: "+err.Error())
		return
	}
	defer func() { _ = stream.Close() }()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, stream)
}

// parsePodLogParams resolves the §25.4 line 2532 query parameters onto a
// PodLogOptions. ?since= accepts either a Go duration ("5m", "1h30m") or
// a plain integer count of seconds; ?tail= is a non-negative line count;
// ?previous= is a boolean. A malformed value is a validation error so the
// caller cannot silently get unfiltered logs.
func parsePodLogParams(q url.Values) (PodLogOptions, error) {
	opts := PodLogOptions{Container: q.Get("container")}

	if raw := q.Get("since"); raw != "" {
		secs, err := parseSinceSeconds(raw)
		if err != nil {
			return PodLogOptions{}, errors.New("invalid since: must be a duration (e.g. 5m) or a non-negative number of seconds")
		}
		opts.SinceSeconds = &secs
	}

	if raw := q.Get("tail"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			return PodLogOptions{}, errors.New("invalid tail: must be a non-negative integer line count")
		}
		opts.TailLines = &n
	}

	if raw := q.Get("previous"); raw != "" {
		prev, err := strconv.ParseBool(raw)
		if err != nil {
			return PodLogOptions{}, errors.New("invalid previous: must be true or false")
		}
		opts.Previous = prev
	}

	return opts, nil
}

// parseSinceSeconds converts the ?since= value to a whole-second count.
// It accepts a Go duration string first (so "5m" and "1h" work) and
// falls back to a bare integer interpreted as seconds. A negative or
// sub-second-only duration is rejected because the K8s SinceSeconds field
// is a non-negative whole-second count.
func parseSinceSeconds(raw string) (int64, error) {
	if d, err := time.ParseDuration(raw); err == nil {
		secs := int64(d / time.Second)
		if secs < 0 {
			return 0, errors.New("negative duration")
		}
		return secs, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, errors.New("not a duration or non-negative integer")
	}
	return n, nil
}
