package ocix

import (
	"bytes"
	"context"
	"os"
	"testing"

	"oras.land/oras-go/v2/content/oci"

	"github.com/vi-dev/nem-cli/internal/spec"
)

func TestPushArchiveMergesPlatformsAndRoundTrips(t *testing.T) {
	ctx := context.Background()
	store, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	darwin := spec.Platform{OS: "darwin", Arch: "arm64"}
	linux := spec.Platform{OS: "linux", Arch: "amd64"}
	dArc := []byte("darwin-archive-bytes")
	lArc := []byte("linux-archive-bytes")

	if _, pushed, err := PushArchive(ctx, store, "v1", darwin, dArc, false); err != nil || !pushed {
		t.Fatalf("push darwin: pushed=%v err=%v", pushed, err)
	}
	if _, pushed, err := PushArchive(ctx, store, "v1", linux, lArc, false); err != nil || !pushed {
		t.Fatalf("push linux: pushed=%v err=%v", pushed, err)
	}
	// unchanged re-push of darwin is a no-op
	if _, pushed, err := PushArchive(ctx, store, "v1", darwin, dArc, false); err != nil || pushed {
		t.Fatalf("re-push darwin unchanged: pushed=%v (want false) err=%v", pushed, err)
	}
	// force re-pushes
	if _, pushed, err := PushArchive(ctx, store, "v1", darwin, dArc, true); err != nil || !pushed {
		t.Fatalf("force re-push: pushed=%v (want true) err=%v", pushed, err)
	}

	// both platforms are readable via the consumer read path
	for plat, want := range map[spec.Platform][]byte{darwin: dArc, linux: lArc} {
		p, err := PullArchiveFrom(ctx, store, "v1", plat, t.TempDir())
		if err != nil {
			t.Fatalf("pull %s: %v", plat, err)
		}
		got, _ := os.ReadFile(p)
		if !bytes.Equal(got, want) {
			t.Fatalf("pull %s = %q, want %q", plat, got, want)
		}
	}
}
