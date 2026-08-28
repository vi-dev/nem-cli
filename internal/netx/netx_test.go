package netx

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// reset restores netx to its zero (no active settings) state after a test
// that calls Set, so shuffled test order never leaks configuration between
// tests in this package.
func reset(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { Set(nil) })
}

func TestForHostReportsConfiguredEntries(t *testing.T) {
	reset(t)
	Set(map[string]HostSettings{"registry.corp:5000": {Insecure: true}})

	got, ok := ForHost("registry.corp:5000")
	if !ok || !got.Insecure {
		t.Fatalf("ForHost = %+v, %v; want Insecure entry", got, ok)
	}
	if _, ok := ForHost("other.example.com"); ok {
		t.Fatal("ForHost found an entry for an unconfigured host")
	}
}

func TestHostClientMemoizesPerHost(t *testing.T) {
	reset(t)
	Set(map[string]HostSettings{"a.example.com": {Insecure: true}})

	c1, err := HostClient("a.example.com")
	if err != nil {
		t.Fatalf("HostClient: %v", err)
	}
	c2, err := HostClient("a.example.com")
	if err != nil {
		t.Fatalf("HostClient: %v", err)
	}
	if c1 != c2 {
		t.Fatal("HostClient returned different clients for the same host")
	}

	c3, err := HostClient("b.example.com")
	if err != nil {
		t.Fatalf("HostClient: %v", err)
	}
	if c3 == c1 {
		t.Fatal("HostClient returned the same client for different hosts")
	}
}

func TestHostClientInvalidatedOnSet(t *testing.T) {
	reset(t)
	Set(map[string]HostSettings{"a.example.com": {Insecure: true}})
	before, err := HostClient("a.example.com")
	if err != nil {
		t.Fatalf("HostClient: %v", err)
	}

	Set(map[string]HostSettings{"a.example.com": {Insecure: true}})
	after, err := HostClient("a.example.com")
	if err != nil {
		t.Fatalf("HostClient: %v", err)
	}
	if before == after {
		t.Fatal("HostClient kept the pre-Set client after settings were re-set")
	}
}

func TestHostClientBadCAFile(t *testing.T) {
	reset(t)
	Set(map[string]HostSettings{"a.example.com": {CA: filepath.Join(t.TempDir(), "missing.pem")}})
	if _, err := HostClient("a.example.com"); err == nil {
		t.Fatal("HostClient accepted an unreadable CA file")
	}
}

func TestHostClientGarbageCAFile(t *testing.T) {
	reset(t)
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, []byte("not a certificate"), 0o644); err != nil {
		t.Fatal(err)
	}
	Set(map[string]HostSettings{"a.example.com": {CA: caPath}})
	if _, err := HostClient("a.example.com"); err == nil {
		t.Fatal("HostClient accepted a CA file with no valid PEM certificates")
	}
}

// TestHostClientTrustsConfiguredCA proves the CA-configured client
// actually completes a TLS handshake against a server whose certificate is
// signed by that CA, and rejects a server it wasn't told to trust.
func TestHostClientTrustsConfiguredCA(t *testing.T) {
	reset(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(caPath, caPEM, 0o644); err != nil {
		t.Fatal(err)
	}

	host := srv.Listener.Addr().String()
	Set(map[string]HostSettings{host: {CA: caPath}})

	client, err := HostClient(host)
	if err != nil {
		t.Fatalf("HostClient: %v", err)
	}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get with configured CA: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// A client for a different, unconfigured host must not trust this
	// server's certificate — default system trust rejects it.
	Set(nil)
	untrusted, err := HostClient(host)
	if err != nil {
		t.Fatalf("HostClient: %v", err)
	}
	if _, err := untrusted.Get(srv.URL); err == nil {
		var unknownAuthority x509.UnknownAuthorityError
		t.Fatalf("Get without configured CA unexpectedly succeeded (want %T)", unknownAuthority)
	}
}

func TestHostClientInsecureSkipsVerify(t *testing.T) {
	reset(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	host := srv.Listener.Addr().String()
	Set(map[string]HostSettings{host: {Insecure: true}})

	client, err := HostClient(host)
	if err != nil {
		t.Fatalf("HostClient: %v", err)
	}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get with insecure: %v", err)
	}
	resp.Body.Close()
}
