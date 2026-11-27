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

package telemetry

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// enableRecords installs an in-memory tracer provider and marks telemetry as
// configured, so the handler traces into the recorder.
func enableRecords(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	previous := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	previousConfigured := configured
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	configured = true
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		otel.SetTextMapPropagator(previousPropagator)
		configured = previousConfigured
	})
	return recorder
}

func TestStartIsANoOpWithoutAnEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	previous := configured
	configured = false
	t.Cleanup(func() { configured = previous })

	shutdown, err := Start(context.Background(), "cap-test")
	if err != nil {
		t.Fatal(err)
	}
	if Enabled() {
		t.Fatal("exporters were installed without an endpoint")
	}
	if shutdown == nil {
		t.Fatal("shutdown must always be callable")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHandlerNamesSpansByRouteAndSkipsProbes(t *testing.T) {
	recorder := enableRecords(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/cap/process/records/{app_id}/{table}/{entity_id}/history", func(w http.ResponseWriter, _ *http.Request) {})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {})
	handler := Handler(mux)

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	handler.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/api/cap/process/records/app/domain.t/9f0d/history?role=role_x", nil))

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("traced %d requests, want only the API request", len(spans))
	}
	if got, want := spans[0].Name(), "GET /api/cap/process/records/{app_id}/{table}/{entity_id}/history"; got != want {
		t.Fatalf("span name = %q, want %q", got, want)
	}
}

func TestOutgoingRequestCarriesTraceparent(t *testing.T) {
	enableRecords(t)

	var header string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get("traceparent")
	}))
	defer upstream.Close()

	ctx, span := otel.Tracer("test").Start(context.Background(), "outgoing")
	defer span.End()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream.URL, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: Client(nil)}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()

	if !strings.HasPrefix(header, "00-") || !strings.Contains(header, span.SpanContext().TraceID().String()) {
		t.Fatalf("traceparent = %q, want trace %s", header, span.SpanContext().TraceID())
	}
}

func TestLoggerAddsTheRequestTraceID(t *testing.T) {
	var output strings.Builder
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	ctx, span := otel.Tracer("test").Start(context.Background(), "request")
	defer span.End()
	Logger(ctx).Info("handled request")
	Logger(context.Background()).Info("startup")

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("wrote %d lines, want 2: %s", len(lines), output.String())
	}
	if !strings.Contains(lines[0], span.SpanContext().TraceID().String()) || !strings.Contains(lines[0], "span_id") {
		t.Fatalf("request line lacks trace IDs: %s", lines[0])
	}
	if strings.Contains(lines[1], "trace_id") {
		t.Fatalf("startup line should have no trace IDs: %s", lines[1])
	}
}

func TestNameTrimsWhitespace(t *testing.T) {
	if got := Name("  cap-process \n"); got != "cap-process" {
		t.Fatalf("Name = %q", got)
	}
}

func TestHandlerIsTheIdentityWithoutConfiguration(t *testing.T) {
	previous := configured
	configured = false
	t.Cleanup(func() { configured = previous })

	var spans int
	mux := http.NewServeMux()
	mux.HandleFunc("GET /x", func(http.ResponseWriter, *http.Request) { spans++ })
	Handler(mux).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	if spans != 1 {
		t.Fatal("the unwrapped handler must still serve")
	}
}
