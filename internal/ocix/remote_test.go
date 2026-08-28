package ocix

import (
	"testing"
)

func TestValidateRef(t *testing.T) {
	cases := []struct {
		ref string
		ok  bool
	}{
		{"ghcr.io/x/y", false},
		{"ghcr.io/x/y:v2", true},
		{"ghcr.io/x/y@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", true},
		{"not a ref", false},
	}
	for _, c := range cases {
		err := WithTagOrDigest(c.ref)
		if c.ok && err != nil {
			t.Errorf("ValidateRef(%q): unexpected error: %v", c.ref, err)
		}
		if !c.ok && err == nil {
			t.Errorf("ValidateRef(%q): want error, got nil", c.ref)
		}
	}
}

func TestValidateBaseRef(t *testing.T) {
	cases := []struct {
		ref string
		ok  bool
	}{
		{"ghcr.io/x/y", true},
		{"ghcr.io/x/y:v2", false},
		{"ghcr.io/x/y@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", false},
		{"not a ref", false},
	}
	for _, c := range cases {
		err := WithoutTagOrDigest(c.ref)
		if c.ok && err != nil {
			t.Errorf("ValidateBaseRef(%q): unexpected error: %v", c.ref, err)
		}
		if !c.ok && err == nil {
			t.Errorf("ValidateBaseRef(%q): want error, got nil", c.ref)
		}
	}
}

func TestNewRemoteRepositoryPlainHTTP(t *testing.T) {
	cases := []struct {
		ref  string
		want bool
	}{
		{"localhost:5001/nem-local-catalog:v2", true},
		{"127.0.0.1:5000/cat:v2", true},
		{"[::1]:5000/cat:v2", true},
		{"ghcr.io/vi-dev/nem-official-catalog:v2", false},
		{"192.168.1.10:5000/cat:v2", false},
	}
	for _, c := range cases {
		repo, err := NewRemoteRepository(c.ref)
		if err != nil {
			t.Fatalf("NewRemoteRepository(%q): %v", c.ref, err)
		}
		if repo.PlainHTTP != c.want {
			t.Errorf("NewRemoteRepository(%q).PlainHTTP = %v, want %v", c.ref, repo.PlainHTTP, c.want)
		}
	}
}
