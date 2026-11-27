---
title: Observability
description: Emit traces, metrics and logs to an OpenTelemetry-compatible backend you operate.
diataxis: how-to
audience: [operator]
status: active
owner: platform
last_reviewed: 2026-11-27
---

# Observability

The platform is **OpenTelemetry-compatible**: it emits telemetry in standard
formats, and an operator's own OpenTelemetry Collector receives it. The project
does not ship or run a Collector, Jaeger, Prometheus or Grafana. With no
`OTEL_*` configuration, nothing extra runs.

## What each component emits

| Component | Traces | Metrics | Logs |
| --- | --- | --- | --- |
| `dxos-authz`, `cap-process`, `cap-hr`, `cap-data` | OTLP/HTTP from the OpenTelemetry SDK; `otelhttp` on the server and on outgoing clients, with W3C `traceparent` propagated to Kestra and Keycloak | OTLP: HTTP server metrics and Go runtime metrics | JSON on stdout, with `trace_id` and `span_id` on request-scoped lines |
| APISIX 3.10 | `opentelemetry` plugin, OTLP/HTTP; the plugin passes `traceparent` upstream. It samples nothing until a route enables it | `prometheus` plugin on `:9091` (`/apisix/prometheus/metrics`) | access log on stdout |
| Keycloak 26.0 | preview tracing (`--features=opentelemetry`, `KC_TRACING_ENABLED`, `KC_TRACING_ENDPOINT`) | `KC_METRICS_ENABLED` on the management port `:9000/metrics` | JSON console output with `KC_LOG_CONSOLE_OUTPUT=json` |
| Kestra 0.20 | Micronaut OTel with `kestra.traces.root` | OTLP metrics through `micronaut.metrics.export.otlp` | stdout |
| dxos-controller | — | controller-runtime Prometheus endpoint, disabled by default; the example component enables `:8080` | stdout |
| Hasura v2.45 CE | **not available**: OpenTelemetry export is an Enterprise feature | **not available**: Prometheus metrics are an Enterprise feature | JSON on stdout |
| PostgreSQL, Kubernetes | — | Collector receivers (`postgresql`, `kubeletstats`) | container logs read by the Collector (`filelog`) |

## Environment variables

The Go services read only the standard OpenTelemetry variables. An endpoint is
required; without one the SDK providers are not installed and the service
behaves as before.

| Variable | Meaning |
| --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP/HTTP base endpoint, for example `http://collector:4318`. `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` and `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` override it per signal |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `http/protobuf` for the bundled exporters |
| `OTEL_SERVICE_NAME` | Overrides the service name the binary sets (`dxos-authz`, `cap-process`, `cap-hr`, `cap-data`) |
| `OTEL_RESOURCE_ATTRIBUTES` | Standard comma-separated resource attributes, for example `deployment.environment=prod` |
| `OTEL_TRACES_EXPORTER` | `otlp` (the default) or `none` |

Kestra and Keycloak have their own switches:

| Variable | Meaning |
| --- | --- |
| `KESTRA_OTEL_ENABLED` | Enable Kestra traces (`true`) |
| `KESTRA_TRACES_ROOT` | `DEFAULT` to trace flow executions, `DISABLED` to stop |
| `KESTRA_OTLP_METRICS_ENABLED` | Enable Kestra OTLP metrics (OTLP/HTTP at `OTEL_EXPORTER_OTLP_ENDPOINT` + `/v1/metrics`) |
| `KESTRA_OTEL_ENDPOINT` | Collector OTLP/gRPC address for Kestra traces (`:4317`); Kestra does not support OTLP/HTTP for traces |
| `KC_FEATURES` | `opentelemetry` enables the preview tracing feature |
| `KC_TRACING_ENABLED`, `KC_TRACING_ENDPOINT`, `KC_TRACING_PROTOCOL` | Keycloak tracing; use `http/protobuf` with the OTLP/HTTP endpoint |
| `KC_METRICS_ENABLED` | Expose Keycloak metrics on the management port |
| `KC_LOG_CONSOLE_OUTPUT` | `json` for structured console logs |
| `OTEL_COLLECTOR_ADDRESS` | Collector `host:port` for APISIX (OTLP/HTTP) |

On Compose these are read from `.env`; see `.env.example`. On Kubernetes the
opt-in component described below sets them.

APISIX loads the `opentelemetry` plugin on every route with the `always_on`
sampler, so a Collector receives its spans and `traceparent` reaches the
upstream. The Collector address comes from `OTEL_COLLECTOR_ADDRESS`; the APISIX
config default is `127.0.0.1:4318`. Span export uses an asynchronous batch
processor: with no Collector listening, APISIX logs export errors and requests
continue. The Prometheus endpoint on `:9091` is enabled unconditionally and only
listens on the internal network.

## Kubernetes component

`deploy/k8s/components/otel` is an opt-in kustomize component:

```yaml
components:
  - ../../components/otel
```

It creates the `dxos-otel` ConfigMap with the Collector endpoint, sets
`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME` and
`OTEL_RESOURCE_ATTRIBUTES` on the Go services, turns on Keycloak tracing,
metrics and JSON logs, enables Kestra traces and OTLP metrics, supplies APISIX's
collector address, and adds Prometheus scrape annotations for APISIX, Keycloak
and the controller. Without the component, the base renders exactly as before.

Change the two `dxos-otel` literals to the Collector Service in your cluster.
`deploy/k8s/components/otel/collector-example.yaml` shows a Collector with an
OTLP receiver, Prometheus scraping, `filelog`, `k8sattributes` and a placeholder
OTLP exporter. Copy and adapt it; it is documentation, not a deployed workload.

## Privacy

Do not put tokens, passwords, request bodies, query parameters, personal data
or record contents in span attributes, metric labels or logs. HTTP spans are
named by the matched route (`GET /api/cap/process/records/{app_id}/{table}/{entity_id}/history`),
not by the full URL. Request logs add only the trace and span identifiers.
Collector pipelines should drop or hash any attribute an operator adds beyond
this contract.

## Verifying with a throwaway Collector

Run the services with `OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318`,
point a Collector's `debug` exporter at the same process (or at Jaeger), and
make one request. Expect one trace spanning APISIX → `dxos-authz` or
APISIX → `cap-process` → Kestra, metrics from the Go services, and a service log
line carrying the request's `trace_id`. Keycloak emits its own spans when
`KC_TRACING_ENABLED=true`.
