package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/vi-dev/nem-cli/internal/mirror"
	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/publish"
)

func urlPkgYAML(name, version, digest string) string {
	return string(publish.URLPkgYAML(name, version, "https://example.com/"+name+"/{{.Version}}", publish.UniformSha256(digest)))
}

func TestCatalogMirrorCmd(t *testing.T) {
	src := memory.New()
	publish.PublishCatalogForTest(t, src, "example.com/cat", map[string]string{
		"go": urlPkgYAML("go", "1.0.0", "aaaa"),
	})
	srcArchives := memory.New()
	ocix.PushFakeArchive(t, srcArchives, "1.0.0", map[string][]byte{"linux/amd64": []byte("go-payload")})
	dst := memory.New()
	dstArchives := memory.New()

	t.Cleanup(mirror.SetSrcCatalogOpener(func(string) (oras.ReadOnlyTarget, string, error) { return src, "v2", nil }))
	t.Cleanup(mirror.SetDstCatalogOpener(func(string) (oras.Target, string, error) { return dst, "v2", nil }))
	t.Cleanup(mirror.SetSrcArchivesOpener(func(string, string) (oras.ReadOnlyTarget, error) { return srcArchives, nil }))
	t.Cleanup(mirror.SetDstArchivesOpener(func(string, string) (oras.Target, error) { return dstArchives, nil }))

	nemHome := t.TempDir()
	_, errb, err := runNem(t, nemHome, "catalog", "mirror", "example.com/cat:v2", "internal.example.com/cat:v2")
	if err != nil {
		t.Fatalf("mirror: %v\n%s", err, errb)
	}
	if !strings.Contains(errb, "Mirrored 1 packages, 1 tag(s)") {
		t.Fatalf("stderr missing summary line:\n%s", errb)
	}

	srcCatDesc, err := src.Resolve(context.Background(), "v2")
	if err != nil {
		t.Fatalf("resolve src catalog: %v", err)
	}
	dstCatDesc, err := dst.Resolve(context.Background(), "v2")
	if err != nil {
		t.Fatalf("resolve dst catalog: %v", err)
	}
	if srcCatDesc.Digest != dstCatDesc.Digest {
		t.Fatalf("dst catalog digest %s != src digest %s", dstCatDesc.Digest, srcCatDesc.Digest)
	}

	srcArchDesc, err := srcArchives.Resolve(context.Background(), "1.0.0")
	if err != nil {
		t.Fatalf("resolve src archive: %v", err)
	}
	dstArchDesc, err := dstArchives.Resolve(context.Background(), "1.0.0")
	if err != nil {
		t.Fatalf("resolve dst archive: %v", err)
	}
	if srcArchDesc.Digest != dstArchDesc.Digest {
		t.Fatalf("dst archive digest %s != src digest %s", dstArchDesc.Digest, srcArchDesc.Digest)
	}
}

func TestCatalogMirrorCmdDryRunWritesNothing(t *testing.T) {
	src := memory.New()
	publish.PublishCatalogForTest(t, src, "example.com/cat", map[string]string{
		"go": urlPkgYAML("go", "1.0.0", "aaaa"),
	})
	srcArchives := memory.New()
	ocix.PushFakeArchive(t, srcArchives, "1.0.0", map[string][]byte{"linux/amd64": []byte("go-payload")})
	dst := memory.New()
	dstArchives := memory.New()

	var dstOpened bool
	t.Cleanup(mirror.SetSrcCatalogOpener(func(string) (oras.ReadOnlyTarget, string, error) { return src, "v2", nil }))
	t.Cleanup(mirror.SetDstCatalogOpener(func(string) (oras.Target, string, error) {
		dstOpened = true
		return dst, "v2", nil
	}))
	t.Cleanup(mirror.SetSrcArchivesOpener(func(string, string) (oras.ReadOnlyTarget, error) { return srcArchives, nil }))
	t.Cleanup(mirror.SetDstArchivesOpener(func(string, string) (oras.Target, error) { return dstArchives, nil }))

	nemHome := t.TempDir()
	_, errb, err := runNem(t, nemHome, "catalog", "mirror", "example.com/cat:v2", "internal.example.com/cat:v2", "--dry-run")
	if err != nil {
		t.Fatalf("mirror --dry-run: %v\n%s", err, errb)
	}
	if dstOpened {
		t.Fatal("dry run must never open the dst catalog")
	}
	// Per-item progress ("would copy 1.0.0") is the task's transient status,
	// invisible outside a live TTY block; the scroll carries the package's
	// completion line and summary, worded "Would mirror" under dry-run.
	if !strings.Contains(errb, "Would mirror go (1 tag(s))") {
		t.Fatalf("stderr missing package completion line:\n%s", errb)
	}
	if !strings.Contains(errb, "Would mirror 1 packages") {
		t.Fatalf("stderr missing dry-run summary line:\n%s", errb)
	}
	if strings.Contains(errb, "would copy") {
		t.Fatalf("stderr must not print per-item progress lines:\n%s", errb)
	}
	if _, err := dstArchives.Resolve(context.Background(), "1.0.0"); err == nil {
		t.Fatal("dry run must not write archives")
	}
}

func TestCatalogMirrorCmdExitsNonzeroOnItemFailure(t *testing.T) {
	src := memory.New()
	publish.PublishCatalogForTest(t, src, "example.com/cat", map[string]string{
		"kubectl": string(publish.OCIPkgYAML("kubectl", "1.28.0")),
	})
	dst := memory.New()

	t.Cleanup(mirror.SetSrcCatalogOpener(func(string) (oras.ReadOnlyTarget, string, error) { return src, "v2", nil }))
	t.Cleanup(mirror.SetDstCatalogOpener(func(string) (oras.Target, string, error) { return dst, "v2", nil }))
	t.Cleanup(mirror.SetSrcArchivesOpener(func(string, string) (oras.ReadOnlyTarget, error) { return memory.New(), nil }))
	t.Cleanup(mirror.SetDstArchivesOpener(func(string, string) (oras.Target, error) { return memory.New(), nil }))

	nemHome := t.TempDir()
	_, errb, err := runNem(t, nemHome, "catalog", "mirror", "example.com/cat:v2", "internal.example.com/cat:v2")
	if err == nil {
		t.Fatal("a run with a failed item must exit nonzero")
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("err = %v, want *ExitError{Code:1}", err)
	}
	if !strings.Contains(errb, "archive missing from source") {
		t.Fatalf("stderr missing the anomaly warning:\n%s", errb)
	}
	if !strings.Contains(errb, "1 tag(s) failed") {
		t.Fatalf("stderr missing the failed count in the summary:\n%s", errb)
	}
}
