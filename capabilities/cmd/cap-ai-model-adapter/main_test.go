// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/modeladapter"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/models"
)

// adapterConfigJSON is a reviewed deployment declaration whose upstream is never
// contacted: the handler's upstream is injected, so no test opens an external
// connection.
const adapterConfigJSON = `{"schema_version":1,"routes":[{"protocol_version":1,"type":"openai-compatible",` +
	`"base_url":"https://upstream.example.invalid/v1","credential_ref":"UPSTREAM_TEST_KEY","models":[` +
	`{"model_id":"gen-test","purpose":"GENERATE","limits":{"max_input_tokens":8192,"max_output_tokens":512,"max_bytes":65536,"timeout_ms":5000}},` +
	`{"model_id":"embed-test","purpose":"EMBED","dimension":3,"limits":{"max_input_tokens":8191,"max_output_tokens":1,"max_bytes":65536,"timeout_ms":5000}}]}]}`

const (
	adapterToken  = "ADAPTER_GATEWAY_TOKEN_CANARY"
	upstreamToken = "UPSTREAM_VENDOR_TOKEN_CANARY"
)

// staticUpstream stands in for the OpenAI-compatible client: the executable tests
// exercise the TLS listener, the request gates and the shutdown path, not the
// vendor protocol Task 3 already covers.
type staticUpstream struct {
	calls  atomic.Int32
	result models.Result
	err    error
}

func (s *staticUpstream) Invoke(context.Context, modeladapter.Route, modeladapter.ModelRule, models.BoundedRequest) (models.Result, error) {
	s.calls.Add(1)
	return s.result, s.err
}

func envFrom(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

// with and without derive one deployment fixture mutation per case.
func with(values map[string]string, key, value string) map[string]string {
	values[key] = value
	return values
}

func without(values map[string]string, key string) map[string]string {
	delete(values, key)
	return values
}

func deploymentEnv(dir, certFile, keyFile string) map[string]string {
	return map[string]string{
		"MODEL_ADAPTER_CONFIG_FILE":   filepath.Join(dir, "adapter.json"),
		"MODEL_ADAPTER_TOKEN":         adapterToken,
		"MODEL_ADAPTER_TLS_CERT_FILE": certFile,
		"MODEL_ADAPTER_TLS_KEY_FILE":  keyFile,
		"MODEL_ADAPTER_LISTEN":        "127.0.0.1:0",
		"UPSTREAM_TEST_KEY":           upstreamToken,
	}
}

// selfSignedCertificate writes a disposable certificate for 127.0.0.1 into dir.
// The material is generated per test run and never committed.
func selfSignedCertificate(t *testing.T, dir string) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "cap-ai-model-adapter-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("certificate: %v", err)
	}
	encodedKey, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("private key: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: encodedKey})
	certFile = filepath.Join(dir, "tls.crt")
	keyFile = filepath.Join(dir, "tls.key")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatalf("write certificate: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	pool = x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("certificate is not usable as a test root")
	}
	return certFile, keyFile, pool
}

func writeAdapterConfig(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "adapter.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write configuration: %v", err)
	}
	return path
}

// assertStaticError pins a startup failure: the error names no secret, no mount
// path and no upstream URL.
func assertStaticError(t *testing.T, err error, dir string) {
	t.Helper()
	if err == nil {
		t.Fatal("startup succeeded, want a static failure")
	}
	message := err.Error()
	for _, forbidden := range []string{adapterToken, upstreamToken, dir, "UPSTREAM_TEST_KEY", "upstream.example.invalid", "tls.crt", "tls.key"} {
		if strings.Contains(message, forbidden) {
			t.Fatalf("startup error leaked %q: %q", forbidden, message)
		}
	}
}

// TestAdapterStartup serves the real TLS listener and pins the trusted/untrusted
// client boundary plus the graceful shutdown.
func TestAdapterStartup(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, pool := selfSignedCertificate(t, dir)
	writeAdapterConfig(t, dir, adapterConfigJSON)
	upstream := &staticUpstream{result: models.Result{
		Text: "The synthetic project code is BLUE-17.", CitationIDs: []string{"E1"},
		Usage: &models.Usage{InputTokens: 30, OutputTokens: 12}, ProviderRequestID: "upstream-request-9",
	}}

	env := envFrom(deploymentEnv(dir, certFile, keyFile))
	service, err := loadService(env)
	if err != nil {
		t.Fatalf("service configuration: %v", err)
	}
	if service.listen != "127.0.0.1:0" || service.token != adapterToken || service.certFile != certFile {
		t.Fatalf("service configuration %+v", service)
	}
	handler, err := newHandler(service, env, upstream)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	server := newServer(handler)
	if server.TLSConfig == nil || server.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("server TLS configuration %+v", server.TLSConfig)
	}
	if server.ReadHeaderTimeout <= 0 || server.ReadTimeout <= 0 || server.WriteTimeout <= 0 || server.IdleTimeout <= 0 || server.MaxHeaderBytes <= 0 {
		t.Fatalf("unbounded server %+v", server)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listener: %v", err)
	}
	address := listener.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var warnings bytes.Buffer
	go func() { done <- serve(ctx, server, listener, certFile, keyFile, &warnings) }()

	raw, err := os.ReadFile(service.configFile)
	if err != nil {
		t.Fatalf("configuration file: %v", err)
	}
	cfg, err := modeladapter.ParseConfig(raw)
	if err != nil {
		t.Fatalf("configuration: %v", err)
	}
	routePath, err := modeladapter.RoutePath(cfg.Routes[0])
	if err != nil {
		t.Fatalf("route path: %v", err)
	}

	trusted := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	body := boundedBody(t, models.BoundedRequest{
		Model: "gen-test", Purpose: "GENERATE", System: models.BoundedSystemInstruction,
		Input: models.Payload{Query: "what is the synthetic project code?", Evidence: []models.Evidence{{
			ID: "E1", Text: "The synthetic project code is BLUE-17.", Provenance: []string{"handbook.md"},
		}}},
		Limits: models.Limits{MaxInputTokens: 8192, MaxOutputTokens: 512, MaxBytes: 65536, TimeoutMS: 5000},
	})

	t.Run("trusted client is served", func(t *testing.T) {
		request, err := http.NewRequest(http.MethodPost, "https://"+address+routePath, strings.NewReader(body))
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+adapterToken)
		response, err := trusted.Do(request)
		if err != nil {
			t.Fatalf("trusted request: %v", err)
		}
		defer func() { _ = response.Body.Close() }()
		answer, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatalf("response body: %v", err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status %d body %q", response.StatusCode, string(answer))
		}
		var result models.Result
		if err := json.Unmarshal(answer, &result); err != nil {
			t.Fatalf("result body: %v", err)
		}
		if result.Text != "The synthetic project code is BLUE-17." || len(result.CitationIDs) != 1 || result.CitationIDs[0] != "E1" {
			t.Fatalf("grounded result %+v", result)
		}
		if upstream.calls.Load() != 1 {
			t.Fatalf("%d upstream dispatches", upstream.calls.Load())
		}
	})

	t.Run("untrusted client is rejected", func(t *testing.T) {
		untrusted := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}}
		request, err := http.NewRequest(http.MethodPost, "https://"+address+routePath, strings.NewReader(body))
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+adapterToken)
		response, err := untrusted.Do(request)
		if err == nil {
			_ = response.Body.Close()
			t.Fatal("an untrusted client was accepted")
		}
	})

	t.Run("retired TLS version is rejected", func(t *testing.T) {
		legacy := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MaxVersion: tls.VersionTLS11}}}
		request, err := http.NewRequest(http.MethodPost, "https://"+address+routePath, strings.NewReader(body))
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+adapterToken)
		response, err := legacy.Do(request)
		if err == nil {
			_ = response.Body.Close()
			t.Fatal("a TLS 1.1 client was accepted")
		}
	})

	t.Run("wrong adapter token is refused", func(t *testing.T) {
		request, err := http.NewRequest(http.MethodPost, "https://"+address+routePath, strings.NewReader(body))
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+upstreamToken)
		response, err := trusted.Do(request)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status %d", response.StatusCode)
		}
		if upstream.calls.Load() != 1 {
			t.Fatalf("a rejected request reached the upstream: %d dispatches", upstream.calls.Load())
		}
	})

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("graceful shutdown returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown did not complete")
	}
	if !strings.Contains(warnings.String(), "listening on") {
		t.Fatalf("startup log %q", warnings.String())
	}
	if strings.Contains(warnings.String(), adapterToken) || strings.Contains(warnings.String(), upstreamToken) {
		t.Fatalf("startup log leaked a secret: %q", warnings.String())
	}
}

// TestAdapterStartupRejectsInvalidDeployment pins every startup failure: a static
// error without a raw value, and no listener bound behind it.
func TestAdapterStartupRejectsInvalidDeployment(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, _ := selfSignedCertificate(t, dir)
	writeAdapterConfig(t, dir, adapterConfigJSON)

	cases := map[string]map[string]string{
		"missing configuration path": without(deploymentEnv(dir, certFile, keyFile), "MODEL_ADAPTER_CONFIG_FILE"),
		"missing token":              without(deploymentEnv(dir, certFile, keyFile), "MODEL_ADAPTER_TOKEN"),
		"blank token":                with(deploymentEnv(dir, certFile, keyFile), "MODEL_ADAPTER_TOKEN", "   "),
		"missing credential":         without(deploymentEnv(dir, certFile, keyFile), "UPSTREAM_TEST_KEY"),
		"missing configuration file": with(deploymentEnv(dir, certFile, keyFile), "MODEL_ADAPTER_CONFIG_FILE", filepath.Join(dir, "absent.json")),
		"missing TLS certificate":    with(deploymentEnv(dir, certFile, keyFile), "MODEL_ADAPTER_TLS_CERT_FILE", filepath.Join(dir, "absent.crt")),
		"missing TLS key":            without(deploymentEnv(dir, certFile, keyFile), "MODEL_ADAPTER_TLS_KEY_FILE"),
		"missing listen address":     with(deploymentEnv(dir, certFile, keyFile), "MODEL_ADAPTER_LISTEN", "not-an-address"),
	}

	for name, values := range cases {
		values := values
		t.Run(name, func(t *testing.T) {
			// Every deployment failure has to be reported before the listener is
			// reached, so the context is bounded: a regression that starts serving
			// instead of failing cannot hang the suite.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var log bytes.Buffer
			err := run(ctx, envFrom(values), &log)
			assertStaticError(t, err, dir)
			if strings.Contains(log.String(), adapterToken) || strings.Contains(log.String(), upstreamToken) {
				t.Fatalf("startup log leaked a secret: %q", log.String())
			}
		})
	}

	t.Run("invalid declaration", func(t *testing.T) {
		invalid := filepath.Join(dir, "invalid.json")
		if err := os.WriteFile(invalid, []byte(`{"schema_version":1,"routes":[]}`), 0o600); err != nil {
			t.Fatalf("write configuration: %v", err)
		}
		values := deploymentEnv(dir, certFile, keyFile)
		values["MODEL_ADAPTER_CONFIG_FILE"] = invalid
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var log bytes.Buffer
		err := run(ctx, envFrom(values), &log)
		if err == nil {
			t.Fatal("invalid declaration was accepted")
		}
		for _, forbidden := range []string{adapterToken, upstreamToken, dir, "schema_version"} {
			if strings.Contains(err.Error(), forbidden) {
				t.Fatalf("startup error leaked %q: %q", forbidden, err.Error())
			}
		}
	})

	t.Run("oversized declaration", func(t *testing.T) {
		oversized := filepath.Join(dir, "oversized.json")
		padding := strings.Repeat("p", 70000)
		if err := os.WriteFile(oversized, []byte(`{"schema_version":1,"padding":"`+padding+`"}`), 0o600); err != nil {
			t.Fatalf("write configuration: %v", err)
		}
		values := deploymentEnv(dir, certFile, keyFile)
		values["MODEL_ADAPTER_CONFIG_FILE"] = oversized
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var log bytes.Buffer
		assertStaticError(t, run(ctx, envFrom(values), &log), dir)
	})

	t.Run("graceful shutdown on canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var log bytes.Buffer
		if err := run(ctx, envFrom(deploymentEnv(dir, certFile, keyFile)), &log); err != nil {
			t.Fatalf("canceled start returned %v", err)
		}
	})
}

func boundedBody(t *testing.T, request models.BoundedRequest) string {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("request body: %v", err)
	}
	return string(raw)
}

func TestAdapterCanceledStartupReportsInvalidTLS(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	directory := t.TempDir()
	err = serve(ctx, newServer(http.NotFoundHandler()), listener, filepath.Join(directory, "missing.crt"), filepath.Join(directory, "missing.key"), nil)
	if err == nil {
		t.Fatal("cancellation concealed invalid TLS material")
	}
	if strings.Contains(err.Error(), directory) {
		t.Fatal("TLS error disclosed mount path")
	}
}
