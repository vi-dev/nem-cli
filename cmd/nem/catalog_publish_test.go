package main

import (
	"context"
	"strings"
	"testing"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/oci"

	"github.com/vi-dev/nem-cli/internal/publish"
)

const publishFixtureGoPkg = `
schema: 2
name: go
description: The Go programming language
homepage: https://go.dev
license: BSD-3-Clause
artifact:
  url: "https://go.dev/dl/go{{.Version}}.{{.OS}}-{{.Arch}}.tar.gz"
install:
  - extract: {strip: 1}
versions:
  - version: v1.26.5
    sha256:
      darwin/arm64: "aaa"
      darwin/amd64: "bbb"
      linux/arm64: "ccc"
      linux/amd64: "ddd"
`

func TestCatalogPublishPushesToTarget(t *testing.T) {
	ctx := context.Background()
	store, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatalf("oci.New: %v", err)
	}
	restore := publish.SetTargetOpenerForTest(func(context.Context, string) (oras.Target, error) { return store, nil })
	defer restore()

	nemHome := t.TempDir()
	fixtureDir := writeLintFixture(t, map[string]string{"go": publishFixtureGoPkg})

	if _, _, err := runNem(t, nemHome, "catalog", "publish", "example.com/cat", fixtureDir, "--tag", "v2"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := store.Resolve(ctx, "v2"); err != nil {
		t.Fatalf("resolve v2: %v", err)
	}
}

func TestCatalogPublishDryRunWritesNothing(t *testing.T) {
	store, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatalf("oci.New: %v", err)
	}
	var opened bool
	restore := publish.SetTargetOpenerForTest(func(context.Context, string) (oras.Target, error) {
		opened = true
		return store, nil
	})
	defer restore()

	nemHome := t.TempDir()
	fixtureDir := writeLintFixture(t, map[string]string{"go": publishFixtureGoPkg})

	_, errb, err := runNem(t, nemHome, "catalog", "publish", "example.com/cat", fixtureDir, "--dry-run")
	if err != nil {
		t.Fatalf("publish --dry-run: %v", err)
	}
	if opened {
		t.Fatal("dry run must not open the target")
	}
	if !strings.Contains(errb, "Dry run") {
		t.Fatalf("narration = %q, want the dry-run plan", errb)
	}
}

func TestCatalogPublishRejectsTaggedRef(t *testing.T) {
	nemHome := t.TempDir()
	fixtureDir := writeLintFixture(t, map[string]string{"go": publishFixtureGoPkg})

	if _, _, err := runNem(t, nemHome, "catalog", "publish", "example.com/cat:v2", fixtureDir); err == nil {
		t.Fatal("tagged oci ref must be rejected")
	}
}
