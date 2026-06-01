// SPDX-License-Identifier: MIT

package tracing

// This file initializes the process-wide OpenTelemetry TracerProvider and
// trace-context propagator that the §16.3 span catalog (tracing.go) emits
// against. Without a provider installed here, every span started through
// Tracer.Start lands on otel.GetTracerProvider()'s default no-op provider
// and is silently dropped.
//
// §16.3 line 359 fixes the platform-side sampling posture: "The gateway
// emits 100% of traces to the OpenTelemetry Collector; the Collector applies
// tail-based sampling to decide which traces to export." So the in-process
// sampler is AlwaysSample (head sampling is 100%); the 10% probabilistic rate
// (global.traceSamplingRate) is a Collector concern, not a gateway one.
//
// Exporter selection follows §16.3 line 359 as well: an OTLP/HTTP exporter
// when an OTLP endpoint is configured, and a stdout exporter otherwise ("a
// stdout exporter for `make run`"), which also covers dev mode.

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
)

// ProviderConfig configures InitProvider. ServiceName is the resource
// service.name stamped on every span (e.g. "lenny-gateway"). OTLPEndpoint is
// the value of OTEL_EXPORTER_OTLP_ENDPOINT; when non-empty an OTLP/HTTP
// exporter targets it, otherwise the stdout exporter is installed. DevMode
// forces the stdout exporter regardless of OTLPEndpoint so a local `make run`
// developer sees spans without standing up a Collector.
type ProviderConfig struct {
	ServiceName  string
	OTLPEndpoint string
	DevMode      bool
}

// InitProvider builds an SDK TracerProvider per §16.3, installs it as the
// global provider, and installs the W3C trace-context + baggage propagator
// so the §16.3 propagation chain (Client → Gateway → Pod → Child) can carry
// trace context across HTTP headers and gRPC metadata. The returned shutdown
// func flushes the batch processor; callers defer or invoke it during
// graceful shutdown so buffered spans are not lost.
//
// spec: §16.3 line 359 (100% head sampling, OTLP to the Collector, stdout in
// dev / `make run`); §16.3 "Tier 1 trace context flows through" propagation
// chain.
func InitProvider(ctx context.Context, cfg ProviderConfig) (func(context.Context) error, error) {
	exporter, err := newExporter(ctx, cfg)
	if err != nil {
		return nil, err
	}

	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(cfg.ServiceName),
	))
	if err != nil {
		// A schema-URL conflict is the only error resource.Merge returns;
		// fall back to the service-name-only resource rather than failing
		// process startup over an observability resource.
		res = resource.NewSchemaless(semconv.ServiceName(cfg.ServiceName))
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		// §16.3 line 359: the gateway emits 100% of traces; the Collector
		// tail-samples. ParentBased keeps a delegation tree intact when an
		// upstream already made the sampling decision.
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return tp.Shutdown, nil
}

// newExporter returns the OTLP/HTTP exporter when an endpoint is configured
// and dev mode is off, otherwise the stdout exporter. spec: §16.3 line 359.
func newExporter(ctx context.Context, cfg ProviderConfig) (sdktrace.SpanExporter, error) {
	if cfg.OTLPEndpoint == "" || cfg.DevMode {
		exp, err := stdouttrace.New()
		if err != nil {
			return nil, fmt.Errorf("tracing: stdout exporter: %w", err)
		}
		return exp, nil
	}
	exp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(cfg.OTLPEndpoint))
	if err != nil {
		return nil, fmt.Errorf("tracing: otlp/http exporter for %q: %w", cfg.OTLPEndpoint, err)
	}
	return exp, nil
}
