package mirror

import (
	"context"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/spec"
)

func TestOciRefIsRelative(t *testing.T) {
	cases := []struct {
		tmpl string
		want bool
	}{
		{"", true},
		{":{{.Version}}", true},
		{"@{{.Version}}", true},
		{"ghcr.io/other/pkg:{{.Version}}", false},
		{"other-registry.example.com/vendor/tool:{{.Version}}", false},
	}
	for _, c := range cases {
		if got := ociRefIsRelative(c.tmpl); got != c.want {
			t.Errorf("ociRefIsRelative(%q) = %v, want %v", c.tmpl, got, c.want)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name             string
		pkg              *spec.Package
		wantParticipates bool
		wantPrebuilt     bool
	}{
		{"url", &spec.Package{Artifact: spec.Artifact{URL: "https://example.com/{{.Version}}"}}, true, false},
		{"github", &spec.Package{Artifact: spec.Artifact{GitHub: &spec.GitHubAsset{Repo: "a/b"}}}, true, false},
		{"oci relative", &spec.Package{Artifact: spec.Artifact{OCI: ":{{.Version}}"}}, true, true},
		{"oci absolute", &spec.Package{Artifact: spec.Artifact{OCI: "ghcr.io/other/pkg:{{.Version}}"}}, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotParticipates, gotPrebuilt := classify(c.pkg)
			if gotParticipates != c.wantParticipates || gotPrebuilt != c.wantPrebuilt {
				t.Errorf("classify() = (%v, %v), want (%v, %v)", gotParticipates, gotPrebuilt, c.wantParticipates, c.wantPrebuilt)
			}
		})
	}
}

func TestMirrorVersionPresentSkipsCopy(t *testing.T) {
	ctx := context.Background()
	src := memory.New()
	ocix.PushFakeArchive(t, src, "1.0.0", map[string][]byte{"linux/amd64": []byte("payload")})
	dst := memory.New()
	ocix.PushFakeArchive(t, dst, "1.0.0", map[string][]byte{"linux/amd64": []byte("payload")})

	rep := &fakeReporter{}
	task := rep.Task("test")
	got := mirrorVersion(ctx, src, dst, "go", "1.0.0", false, false, rep, task)
	if got != outcomePresent {
		t.Fatalf("outcome = %v, want outcomePresent", got)
	}
	statuses := rep.taskFor("test").snapshotStatuses()
	if len(rep.snapshotWarns()) != 0 || len(statuses) != 0 {
		t.Fatalf("present must produce no narration and no status update: warns=%v statuses=%v", rep.snapshotWarns(), statuses)
	}
}

func TestMirrorVersionAbsentOnDstCopies(t *testing.T) {
	ctx := context.Background()
	src := memory.New()
	ocix.PushFakeArchive(t, src, "1.0.0", map[string][]byte{"linux/amd64": []byte("payload")})
	dst := memory.New()

	rep := &fakeReporter{}
	got := mirrorVersion(ctx, src, dst, "go", "1.0.0", false, false, rep, rep.Task("test"))
	if got != outcomeCopied {
		t.Fatalf("outcome = %v, want outcomeCopied", got)
	}
	srcDesc, err := src.Resolve(ctx, "1.0.0")
	if err != nil {
		t.Fatalf("resolve src: %v", err)
	}
	dstDesc, err := dst.Resolve(ctx, "1.0.0")
	if err != nil {
		t.Fatalf("resolve dst after copy: %v", err)
	}
	if srcDesc.Digest != dstDesc.Digest {
		t.Fatalf("dst digest %s != src digest %s", dstDesc.Digest, srcDesc.Digest)
	}
	if len(rep.snapshotInfos()) != 0 {
		t.Fatalf("infos = %v, want none: per-item progress must not print", rep.snapshotInfos())
	}
	statuses := rep.taskFor("test").snapshotStatuses()
	if len(statuses) != 1 || statuses[0] != "copying 1.0.0" {
		t.Fatalf("statuses = %v, want [\"copying 1.0.0\"]", statuses)
	}
}

func TestMirrorVersionDryRunCopiesNothing(t *testing.T) {
	ctx := context.Background()
	src := memory.New()
	ocix.PushFakeArchive(t, src, "1.0.0", map[string][]byte{"linux/amd64": []byte("payload")})
	dst := memory.New()

	rep := &fakeReporter{}
	got := mirrorVersion(ctx, src, dst, "go", "1.0.0", false, true, rep, rep.Task("test"))
	if got != outcomeCopied {
		t.Fatalf("outcome = %v, want outcomeCopied (would-copy still counts)", got)
	}
	if _, err := dst.Resolve(ctx, "1.0.0"); err == nil {
		t.Fatal("dry run must not write to dst")
	}
	if len(rep.snapshotInfos()) != 0 {
		t.Fatalf("infos = %v, want none: per-item progress must not print", rep.snapshotInfos())
	}
	statuses := rep.taskFor("test").snapshotStatuses()
	if len(statuses) != 1 || statuses[0] != "would copy 1.0.0" {
		t.Fatalf("statuses = %v, want [\"would copy 1.0.0\"]", statuses)
	}
}
