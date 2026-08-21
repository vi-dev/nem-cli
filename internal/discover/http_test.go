package discover

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vi-dev/nem-cli/internal/spec"
)

func TestHTTPVersions(t *testing.T) {
	page := `<html><body>
<a href="gpgme-1.23.0.tar.bz2">gpgme-1.23.0.tar.bz2</a>
<a href="gpgme-1.24.3.tar.bz2">gpgme-1.24.3.tar.bz2</a>
<a href="gpgme-1.24.3.tar.bz2.sig">gpgme-1.24.3.tar.bz2.sig</a>
</body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ftp/gcrypt/gpgme/" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, page)
	}))
	defer srv.Close()

	got, err := httpVersions(context.Background(), srv.Client(), &spec.HTTPDiscovery{
		URL:    srv.URL + "/ftp/gcrypt/gpgme/",
		Filter: `gpgme-(\d+\.\d+\.\d+)\.tar\.bz2`,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1.23.0", "1.24.3"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("versions = %v, want %v", got, want)
	}
}

func TestHTTPVersionsFullMatchWithoutGroup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "9.1 9.2 9.2")
	}))
	defer srv.Close()

	got, err := httpVersions(context.Background(), srv.Client(), &spec.HTTPDiscovery{
		URL:    srv.URL,
		Filter: `\d+\.\d+`,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"9.1", "9.2"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("versions = %v, want %v", got, want)
	}
}

func TestListDispatchesHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="x-3.1.0.tar.gz">x-3.1.0.tar.gz</a>`)
	}))
	defer srv.Close()

	pkg := &spec.Package{Name: "x", VersionDiscovery: &spec.Discovery{
		HTTP: &spec.HTTPDiscovery{URL: srv.URL, Filter: `x-(\d+\.\d+\.\d+)\.tar\.gz`},
	}}
	got, err := List(context.Background(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "3.1.0" {
		t.Fatalf("versions = %v, want [3.1.0]", got)
	}
}

func TestHTTPVersionsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := httpVersions(context.Background(), srv.Client(), &spec.HTTPDiscovery{
		URL: srv.URL, Filter: `(\d+)`,
	})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want status error", err)
	}
}

func TestHTTPVersionsBodyTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		big := strings.Repeat("x", maxHTTPBody+1)
		fmt.Fprint(w, big)
	}))
	defer srv.Close()

	_, err := httpVersions(context.Background(), srv.Client(), &spec.HTTPDiscovery{
		URL: srv.URL, Filter: `(\d+)`,
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v, want size-cap error", err)
	}
}
