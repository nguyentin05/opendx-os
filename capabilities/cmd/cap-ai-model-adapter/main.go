// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

// Command cap-ai-model-adapter serves the private bounded-http-v1 model endpoint.
// It translates the reviewed bounded protocol into one reviewed OpenAI-compatible
// POST per call. It owns no database, exposes no public listener and reads no
// secret from its arguments: the route declaration, the Gateway token and the TLS
// key are read from protected mounts and the environment.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/modeladapter"
)

const (
	configFileEnv = "MODEL_ADAPTER_CONFIG_FILE"
	tokenEnv      = "MODEL_ADAPTER_TOKEN"
	certFileEnv   = "MODEL_ADAPTER_TLS_CERT_FILE"
	keyFileEnv    = "MODEL_ADAPTER_TLS_KEY_FILE"
	listenEnv     = "MODEL_ADAPTER_LISTEN"

	defaultListen = ":8443"

	// maxConfigBytes matches the declaration size the parser accepts.
	maxConfigBytes = 65536
	// MaxHeaderBytes bounds one request head. The bounded envelope is small and
	// carries no cookies or long-lived headers.
	maxHeaderBytes = 16 * 1024

	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 120 * time.Second
	idleTimeout       = 60 * time.Second
	shutdownTimeout   = 10 * time.Second
)

// ErrService is a startup failure of the adapter executable. Every failure is
// static: it names no secret, mount path, upstream URL or raw configuration value.
var ErrService = errors.New("MODEL_ADAPTER_SERVICE_INVALID")

// serviceConfig is the executable's deployment contract.
type serviceConfig struct {
	configFile string
	token      string
	certFile   string
	keyFile    string
	listen     string
}

// loadService reads the deployment contract from the environment. A missing or
// blank value fails before any file is opened.
func loadService(getenv func(string) string) (serviceConfig, error) {
	service := serviceConfig{
		configFile: strings.TrimSpace(getenv(configFileEnv)),
		token:      strings.TrimSpace(getenv(tokenEnv)),
		certFile:   strings.TrimSpace(getenv(certFileEnv)),
		keyFile:    strings.TrimSpace(getenv(keyFileEnv)),
		listen:     strings.TrimSpace(getenv(listenEnv)),
	}
	if service.configFile == "" || service.token == "" || service.certFile == "" || service.keyFile == "" {
		return serviceConfig{}, serviceInvalid("configuration, token, certificate and key paths are required")
	}
	if service.listen == "" {
		service.listen = defaultListen
	}
	return service, nil
}

// readConfigFile reads the declaration from its protected mount with a hard cap.
func readConfigFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, serviceInvalid("the route declaration is not readable")
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return nil, serviceInvalid("the route declaration is not readable")
	}
	if len(raw) > maxConfigBytes {
		return nil, serviceInvalid("the route declaration exceeds the size limit")
	}
	return raw, nil
}

// newHandler builds the served handler: the reviewed route declaration, the
// Gateway bearer and the OpenAI-compatible upstream client.
func newHandler(service serviceConfig, getenv func(string) string, upstream modeladapter.Upstream) (http.Handler, error) {
	raw, err := readConfigFile(service.configFile)
	if err != nil {
		return nil, err
	}
	cfg, err := modeladapter.ParseConfig(raw)
	if err != nil {
		return nil, serviceCause(err)
	}
	if upstream == nil {
		upstream, err = modeladapter.NewOpenAIClient(cfg, getenv, nil)
		if err != nil {
			return nil, serviceCause(err)
		}
	}
	handler, err := modeladapter.NewHandler(cfg, service.token, upstream)
	if err != nil {
		return nil, serviceCause(err)
	}
	return handler, nil
}

// newServer bounds one TLS listener. TLS 1.2 is the floor, every phase is bounded
// and the request head has a hard size cap.
func newServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}
}

// serve serves the handler over TLS until the context is canceled. The listener is
// supplied by the caller so a test can bind an ephemeral loopback port. A normal
// shutdown returns nil; the TLS material is read here, so a bad certificate is
// reported without its path.
func serve(ctx context.Context, server *http.Server, listener net.Listener, certFile, keyFile string, stderr io.Writer) error {
	if stderr != nil {
		_, _ = fmt.Fprintf(stderr, "cap-ai-model-adapter listening on %s\n", listener.Addr())
	}
	served := make(chan error, 1)
	go func() { served <- server.ServeTLS(listener, certFile, keyFile) }()
	select {
	case err := <-served:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		// The TLS error names the mount path, so it never leaves this function.
		return serviceInvalid("the TLS listener is not usable")
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return serviceInvalid("the TLS listener did not stop cleanly")
		}
		if err := <-served; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return serviceInvalid("the TLS listener is not usable")
		}
		return nil
	}
}

// run builds the deployment, binds the listener and serves until cancellation.
// Missing or invalid configuration, credential, certificate or key returns a
// static error that carries no raw value.
func run(ctx context.Context, getenv func(string) string, stderr io.Writer) error {
	if getenv == nil {
		return serviceInvalid("an environment lookup is required")
	}
	service, err := loadService(getenv)
	if err != nil {
		return err
	}
	handler, err := newHandler(service, getenv, nil)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", service.listen)
	if err != nil {
		return serviceInvalid("the listener address is not usable")
	}
	defer func() { _ = listener.Close() }()
	return serve(ctx, newServer(handler), listener, service.certFile, service.keyFile, stderr)
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Getenv, os.Stderr); err != nil {
		// Every error returned by run is static, so this line carries no secret.
		log.Printf("cap-ai-model-adapter: %v", err)
		os.Exit(1)
	}
}

func serviceInvalid(reason string) error { return fmt.Errorf("%w: %s", ErrService, reason) }

// serviceCause reports a failure caused by another error and keeps both
// identities, so a caller can test errors.Is(err, ErrService) without losing
// errors.Is(err, modeladapter.ErrConfig) or errors.Is(err, modeladapter.ErrClient).
func serviceCause(err error) error { return fmt.Errorf("%w: %w", ErrService, err) }
