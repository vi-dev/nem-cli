// Package netx holds per-host registry trust settings (config.yaml's
// hosts: list) and the HTTP clients built from them.
package netx

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"oras.land/oras-go/v2/registry/remote/retry"
)

// Dial/TLS/header waits are bounded so a stalled peer fails quickly;
// deliberately no total-request timeout, since large transfers run slow
// without being stuck.
const (
	dialTimeout           = 10 * time.Second
	tlsHandshakeTimeout   = 10 * time.Second
	responseHeaderTimeout = 30 * time.Second
)

var client = &http.Client{Transport: newTransport()}

// Client is for direct upstream fetches; OCI registry connections use
// HostClient instead, for per-host trust.
func Client() *http.Client { return client }

func newTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: responseHeaderTimeout,
	}
}

// HostSettings: exactly one of CA, PlainHTTP, or Insecure applies.
type HostSettings struct {
	CA        string // absolute path to a PEM CA bundle; "" when unset
	PlainHTTP bool
	Insecure  bool
}

var (
	mu      sync.Mutex
	hosts   map[string]HostSettings
	clients map[string]*http.Client
)

// Set installs h (copied) as the active per-host settings, invalidating
// every memoized client.
func Set(h map[string]HostSettings) {
	cp := make(map[string]HostSettings, len(h))
	for k, v := range h {
		cp[k] = v
	}
	mu.Lock()
	defer mu.Unlock()
	hosts = cp
	clients = nil
}

func ForHost(host string) (HostSettings, bool) {
	mu.Lock()
	defer mu.Unlock()
	s, ok := hosts[host]
	return s, ok
}

// HostClient memoizes host's client, building it from that host's
// settings on first use — default system trust if unconfigured.
func HostClient(host string) (*http.Client, error) {
	mu.Lock()
	defer mu.Unlock()
	if c, ok := clients[host]; ok {
		return c, nil
	}
	c, err := buildHostClient(hosts[host])
	if err != nil {
		return nil, err
	}
	if clients == nil {
		clients = make(map[string]*http.Client)
	}
	clients[host] = c
	return c, nil
}

func buildHostClient(s HostSettings) (*http.Client, error) {
	t := newTransport()
	switch {
	case s.CA != "":
		pool, err := loadCAPool(s.CA)
		if err != nil {
			return nil, fmt.Errorf("load ca %s: %w", s.CA, err)
		}
		t.TLSClientConfig = &tls.Config{RootCAs: pool}
	case s.Insecure:
		t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &http.Client{Transport: retry.NewTransport(t)}, nil
}

func loadCAPool(path string) (*x509.CertPool, error) {
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("%s contains no valid PEM certificates", path)
	}
	return pool, nil
}
