package fill

import (
	"context"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content/memory"

	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/spec"
)

func testPkg(t *testing.T, urlTemplate, digestHex string) *spec.Package {
	t.Helper()
	pkg, err := spec.Parse([]byte(urlPkg("go", "1.0.0", urlTemplate, digestHex)))
	if err != nil {
		t.Fatalf("parse test pkg.yaml: %v", err)
	}
	return pkg
}

func TestFillItemPresentSkipsDownload(t *testing.T) {
	up := newUpstream(t) // never asked to serve anything
	wireUpstream(t, up)
	payload := []byte("payload")
	pkg := testPkg(t, up.URL+"/{{.Version}}", sha256Hex(payload))

	archives := memory.New()
	platMap := map[string][]byte{}
	for _, p := range spec.Supported {
		platMap[p.String()] = payload
	}
	ocix.PushFakeArchive(t, archives, "1.0.0", platMap)

	rep := &fakeReporter{}
	task := rep.Task("test")
	got, _ := fillItem(context.Background(), newHome(t), archives, pkg, "1.0.0", spec.Supported[0], false, rep, task)
	if got != outcomePresent {
		t.Fatalf("outcome = %v, want outcomePresent", got)
	}
	statuses := rep.taskFor("test").snapshotStatuses()
	if len(rep.snapshotWarns()) != 0 || len(statuses) != 0 {
		t.Fatalf("present must produce no narration and no status update: warns=%v statuses=%v", rep.snapshotWarns(), statuses)
	}
	if up.hits.Load() != 0 {
		t.Fatal("present must not touch upstream")
	}
}

func TestFillItemAbsentDownloadsAndPublishes(t *testing.T) {
	up := newUpstream(t)
	wireUpstream(t, up)
	payload := []byte("payload")
	up.set("/1.0.0", payload)
	pkg := testPkg(t, up.URL+"/{{.Version}}", sha256Hex(payload))

	archives := memory.New()
	plat := spec.Supported[0]

	rep := &fakeReporter{}
	got, entry := fillItem(context.Background(), newHome(t), archives, pkg, "1.0.0", plat, false, rep, rep.Task("test"))
	if got != outcomeFilled {
		t.Fatalf("outcome = %v, want outcomeFilled", got)
	}
	if entry.Digest == "" {
		t.Fatal("fillItem returned a zero-value entry for a filled outcome")
	}
	// fillItem only publishes the platform's manifest; it never touches the
	// version's index — the caller commits it, batched with any siblings.
	if _, err := ocix.ArchiveLayerDigest(context.Background(), archives, "1.0.0", plat); err == nil {
		t.Fatal("fillItem must not commit the platform to the index by itself")
	}
	if _, err := ocix.CommitArchiveManifests(context.Background(), archives, "1.0.0", []ocispec.Descriptor{entry}); err != nil {
		t.Fatalf("CommitArchiveManifests: %v", err)
	}
	gotDigest, err := ocix.ArchiveLayerDigest(context.Background(), archives, "1.0.0", plat)
	if err != nil {
		t.Fatalf("resolve published archive: %v", err)
	}
	if gotDigest.Encoded() != sha256Hex(payload) {
		t.Fatalf("published digest = %s, want %s", gotDigest.Encoded(), sha256Hex(payload))
	}
	if len(rep.snapshotInfos()) != 0 {
		t.Fatalf("infos = %v, want none: per-item progress must not print", rep.snapshotInfos())
	}
	statuses := rep.taskFor("test").snapshotStatuses()
	want := "filling 1.0.0 " + plat.String()
	if len(statuses) != 1 || statuses[0] != want {
		t.Fatalf("statuses = %v, want [%q]", statuses, want)
	}
}

func TestFillItemHealsDifferingDigest(t *testing.T) {
	up := newUpstream(t)
	wireUpstream(t, up)
	newPayload := []byte("new-payload")
	up.set("/1.0.0", newPayload)
	pkg := testPkg(t, up.URL+"/{{.Version}}", sha256Hex(newPayload))

	archives := memory.New()
	plat := spec.Supported[0]
	ocix.PushFakeArchive(t, archives, "1.0.0", map[string][]byte{plat.String(): []byte("old-payload")})

	rep := &fakeReporter{}
	got, entry := fillItem(context.Background(), newHome(t), archives, pkg, "1.0.0", plat, false, rep, rep.Task("test"))
	if got != outcomeHealed {
		t.Fatalf("outcome = %v, want outcomeHealed", got)
	}
	if _, err := ocix.CommitArchiveManifests(context.Background(), archives, "1.0.0", []ocispec.Descriptor{entry}); err != nil {
		t.Fatalf("CommitArchiveManifests: %v", err)
	}
	gotDigest, err := ocix.ArchiveLayerDigest(context.Background(), archives, "1.0.0", plat)
	if err != nil {
		t.Fatalf("resolve healed archive: %v", err)
	}
	if gotDigest.Encoded() != sha256Hex(newPayload) {
		t.Fatalf("healed digest = %s, want %s", gotDigest.Encoded(), sha256Hex(newPayload))
	}
	if len(rep.snapshotInfos()) != 0 {
		t.Fatalf("infos = %v, want none: per-item progress must not print", rep.snapshotInfos())
	}
	statuses := rep.taskFor("test").snapshotStatuses()
	want := "healing 1.0.0 " + plat.String()
	if len(statuses) != 1 || statuses[0] != want {
		t.Fatalf("statuses = %v, want [%q]", statuses, want)
	}
}

func TestFillItemShaLookupFailureWarnsAndFails(t *testing.T) {
	up := newUpstream(t)
	wireUpstream(t, up)
	pkg := testPkg(t, up.URL+"/{{.Version}}", "deadbeef")
	archives := memory.New()

	rep := &fakeReporter{}
	got, _ := fillItem(context.Background(), newHome(t), archives, pkg, "9.9.9", spec.Supported[0], false, rep, rep.Task("test"))
	if got != outcomeFailed {
		t.Fatalf("outcome = %v, want outcomeFailed for an undeclared version", got)
	}
	if len(rep.snapshotWarns()) != 1 {
		t.Fatalf("warns = %v, want exactly one", rep.snapshotWarns())
	}
	if up.hits.Load() != 0 {
		t.Fatal("a sha lookup failure must never reach upstream")
	}
}
