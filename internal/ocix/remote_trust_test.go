package ocix

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"oras.land/oras-go/v2/registry/remote/auth"

	"github.com/vi-dev/nem-cli/internal/netx"
)

func sha256Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// resetNetx clears netx's active settings after a test that calls
// netx.Set, so shuffled test order never leaks configuration into an
// unrelated test in this package (notably TestNewRemoteRepositoryPlainHTTP,
// which assumes no configured hosts).
func resetNetx(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { netx.Set(nil) })
}

func TestNewRemoteRepositoryExplicitBeatsLoopbackDefault(t *testing.T) {
	resetNetx(t)
	// A loopback host with an explicit non-plainHTTP entry loses the
	// built-in loopback-defaults-to-plain-HTTP convenience.
	netx.Set(map[string]netx.HostSettings{"127.0.0.1:5000": {Insecure: true}})
	repo, err := NewRemoteRepository("127.0.0.1:5000/cat:v2")
	if err != nil {
		t.Fatalf("NewRemoteRepository: %v", err)
	}
	if repo.PlainHTTP {
		t.Fatal("explicit insecure entry on a loopback host must not default to plain HTTP")
	}
}

func TestNewRemoteRepositoryExplicitPlainHTTPOnNonLoopback(t *testing.T) {
	resetNetx(t)
	// A non-loopback host with an explicit plainHTTP entry gets plain
	// HTTP despite not being loopback.
	netx.Set(map[string]netx.HostSettings{"registry.corp:5000": {PlainHTTP: true}})
	repo, err := NewRemoteRepository("registry.corp:5000/cat:v2")
	if err != nil {
		t.Fatalf("NewRemoteRepository: %v", err)
	}
	if !repo.PlainHTTP {
		t.Fatal("explicit plainHTTP entry on a non-loopback host must be honored")
	}
}

// TestNewRemoteRepositoryHostClientMemoizedPerHost proves
// NewRemoteRepository gets its transport from netx's per-host memoization
// rather than building its own: two repositories on the same host share
// the same underlying *http.Client, and a different host gets a different
// one.
func TestNewRemoteRepositoryHostClientMemoizedPerHost(t *testing.T) {
	resetNetx(t)
	netx.Set(map[string]netx.HostSettings{
		"registry.corp:5000": {Insecure: true},
		"other.corp:5000":    {Insecure: true},
	})

	r1, err := NewRemoteRepository("registry.corp:5000/one:v1")
	if err != nil {
		t.Fatalf("NewRemoteRepository: %v", err)
	}
	r2, err := NewRemoteRepository("registry.corp:5000/two:v1")
	if err != nil {
		t.Fatalf("NewRemoteRepository: %v", err)
	}
	r3, err := NewRemoteRepository("other.corp:5000/three:v1")
	if err != nil {
		t.Fatalf("NewRemoteRepository: %v", err)
	}

	c1 := r1.Client.(*auth.Client).Client
	c2 := r2.Client.(*auth.Client).Client
	c3 := r3.Client.(*auth.Client).Client
	if c1 != c2 {
		t.Fatal("two repositories on the same host got different HTTP clients")
	}
	if c1 == c3 {
		t.Fatal("repositories on different hosts got the same HTTP client")
	}
}

// TestNewRemoteRepositoryResolveOverTrustedTLS proves the full wiring
// end-to-end: a hosts: entry naming a private CA lets
// NewRemoteRepository complete a real TLS handshake and an OCI manifest
// resolve against a server whose certificate that CA signs, and forces
// HTTPS on what would otherwise be a plain-HTTP loopback default.
func TestNewRemoteRepositoryResolveOverTrustedTLS(t *testing.T) {
	resetNetx(t)
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.empty.v1+json","digest":"sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8","size":2},"layers":[]}`)
	digest := sha256Digest(manifest)

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/v2/repo/manifests/v1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		w.Header().Set("Docker-Content-Digest", digest)
		w.Header().Set("Content-Length", strconv.Itoa(len(manifest)))
		if r.Method == http.MethodGet {
			w.Write(manifest)
		}
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(caPath, caPEM, 0o644); err != nil {
		t.Fatal(err)
	}

	host := srv.Listener.Addr().String()
	netx.Set(map[string]netx.HostSettings{host: {CA: caPath}})

	repo, err := NewRemoteRepository(host + "/repo:v1")
	if err != nil {
		t.Fatalf("NewRemoteRepository: %v", err)
	}
	if repo.PlainHTTP {
		t.Fatal("a CA-configured loopback host must use HTTPS, not the plain-HTTP default")
	}

	desc, err := repo.Resolve(context.Background(), "v1")
	if err != nil {
		t.Fatalf("Resolve over TLS with configured CA: %v", err)
	}
	if desc.Digest.String() != digest {
		t.Fatalf("resolved digest = %s, want %s", desc.Digest, digest)
	}
}
