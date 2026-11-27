// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package telemetry configures OpenTelemetry from the standard environment
// variables and is a no-op when no exporter endpoint is set.
package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

// untraced are the probe paths a collector does not need to see.
var untraced = map[string]bool{"/healthz": true, "/readyz": true, "/livez": true}

var configured bool

// Enabled reports whether Start installed exporters.
func Enabled() bool { return configured }

// EndpointSet reports whether the environment asks for OTLP export.
func EndpointSet() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" ||
		os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != "" ||
		os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") != ""
}

// Start installs JSON logging and, when an OTLP endpoint is configured, the
// tracer and meter providers. The returned shutdown function is always safe to
// call; without an endpoint it does nothing.
func Start(ctx context.Context, service string) (func(context.Context) error, error) {
	InstallJSONLogging()
	if !EndpointSet() {
		return func(context.Context) error { return nil }, nil
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(semconv.ServiceName(service)),
	)
	if err != nil {
		return nil, err
	}

	traceExporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	metricExporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, err
	}

	traces := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)
	metrics := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
		sdkmetric.WithResource(res),
	)
	otel.SetTracerProvider(traces)
	otel.SetMeterProvider(metrics)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	if err := runtime.Start(runtime.WithMeterProvider(metrics)); err != nil {
		_ = traces.Shutdown(ctx)
		return nil, err
	}
	configured = true

	return func(ctx context.Context) error {
		return errors.Join(traces.Shutdown(ctx), metrics.Shutdown(ctx))
	}, nil
}

// InstallJSONLogging makes the default slog handler write JSON to stdout, which
// also redirects the standard log package used by the services.
func InstallJSONLogging() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
}

// Handler traces requests with a span named by the matched route pattern, so a
// route with a query string or record ID keeps one span name. Probes are not
// traced.
func Handler(next http.Handler) http.Handler {
	if !configured {
		return next
	}
	return otelhttp.NewHandler(next, "http.server",
		otelhttp.WithFilter(func(r *http.Request) bool { return !untraced[r.URL.Path] }),
		// ServeMux records the pattern on the request while serving it, and
		// otelhttp names the span again after the handler returns. The pattern
		// already starts with the method.
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			if r.Pattern != "" {
				return r.Pattern
			}
			return r.Method + " " + r.URL.Path
		}),
	)
}

// Client returns base with an OpenTelemetry transport, so outgoing requests
// carry the W3C traceparent header. base may be nil.
func Client(base http.RoundTripper) http.RoundTripper {
	if !configured {
		if base == nil {
			return http.DefaultTransport
		}
		return base
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return otelhttp.NewTransport(base)
}

// Logger returns the default logger with the trace and span IDs of ctx, so a
// service log line can be found from its trace. It never adds request bodies,
// tokens or personal data: callers pass only what they already log.
func Logger(ctx context.Context) *slog.Logger {
	span := trace.SpanContextFromContext(ctx)
	if !span.IsValid() {
		return slog.Default()
	}
	return slog.Default().With(
		slog.String("trace_id", span.TraceID().String()),
		slog.String("span_id", span.SpanID().String()),
	)
}

// Name returns the service name without surrounding whitespace.
func Name(service string) string { return strings.TrimSpace(service) }
