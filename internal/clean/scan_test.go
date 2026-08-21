package clean

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vi-dev/nem-cli/internal/home"
)

func scanHome(t *testing.T) (home.Home, string) {
	t.Helper()
	root := t.TempDir()
	h := home.Resolve(func(k string) string {
		if k == "NEM_HOME" {
			return root
		}
		return ""
	})
	return h, root
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestScanFindsStagingWithDeepestMtime(t *testing.T) {
	h, root := scanHome(t)
	deep := filepath.Join(root, "tmp", "go-build-1", "src", "main.c")
	writeFile(t, deep, "int main(){}")

	// The top-level dir is backdated; the file inside is not. Scan must
	// report the newest mtime in the tree, not the directory's own.
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(filepath.Join(root, "tmp", "go-build-1"), old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	s, err := Scan(h, true)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(s.Staging) != 1 {
		t.Fatalf("found %d staging dirs, want 1", len(s.Staging))
	}
	if time.Since(s.Staging[0].Newest) > time.Hour {
		t.Fatalf("Newest = %v; must come from the file inside, not the dir", s.Staging[0].Newest)
	}
	if s.Staging[0].Size == 0 {
		t.Error("staging size should count the tree")
	}
}

func TestScanTakesOnlyBuildStagingDirsFromTmp(t *testing.T) {
	h, root := scanHome(t)
	writeFile(t, filepath.Join(root, "tmp", "go-build-7", "src", "main.c"), "int main(){}")
	writeFile(t, filepath.Join(root, "tmp", "someone-elses-workdir", "data"), "x")

	s, err := Scan(h, true)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(s.Staging) != 1 || filepath.Base(s.Staging[0].Path) != "go-build-7" {
		t.Fatalf("staging = %v, want only the build staging dir", s.Staging)
	}
}

func TestScanFindsLeakedDownloads(t *testing.T) {
	h, root := scanHome(t)
	writeFile(t, filepath.Join(root, "tmp", "go-1.26.6-8412.tmp"), "a killed download")
	writeFile(t, filepath.Join(root, "tmp", "not-a-download"), "x")

	s, err := Scan(h, true)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(s.Downloads) != 1 || filepath.Base(s.Downloads[0].Path) != "go-1.26.6-8412.tmp" {
		t.Fatalf("downloads = %v, want only the .tmp file", s.Downloads)
	}
	if s.Downloads[0].Size == 0 {
		t.Error("a leaked download must report its size; reclaiming it is the point")
	}
	if time.Since(s.Downloads[0].Newest) > time.Hour {
		t.Errorf("download Newest = %v; a download in flight is recognised by it", s.Downloads[0].Newest)
	}
}

func TestScanSeparatesVersionsFromPartials(t *testing.T) {
	h, root := scanHome(t)
	writeFile(t, filepath.Join(root, "packages", "go", "1.26.6", "bin", "go"), "x")
	writeFile(t, filepath.Join(root, "packages", "go", "1.26.5-abc.tmp", "bin", "go"), "x")

	s, err := Scan(h, true)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(s.Versions) != 1 || s.Versions[0].Name != "go" || s.Versions[0].Version != "1.26.6" {
		t.Fatalf("versions = %v, want only go 1.26.6", s.Versions)
	}
	if len(s.Partials) != 1 {
		t.Fatalf("partials = %v, want the .tmp dir", s.Partials)
	}
	if time.Since(s.Partials[0].Newest) > time.Hour {
		t.Errorf("partial Newest = %v; an install in flight is recognised by it", s.Partials[0].Newest)
	}
}

func TestScanIgnoresPackageNamesNemNeverGenerates(t *testing.T) {
	h, root := scanHome(t)
	writeFile(t, filepath.Join(root, "packages", "go", ".hidden", "bin", "go"), "x")
	writeFile(t, filepath.Join(root, "packages", ".stray", "1.0.0", "bin", "x"), "x")

	s, err := Scan(h, true)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(s.Versions)+len(s.Partials) != 0 {
		t.Fatalf("scanned %+v; a name nem never generated is not ours to judge", s)
	}
}

func TestScanWithoutVersionsSkipsVersionTreesButKeepsPartials(t *testing.T) {
	h, root := scanHome(t)
	writeFile(t, filepath.Join(root, "packages", "go", "1.26.6", "bin", "go"), "x")
	writeFile(t, filepath.Join(root, "packages", "go", "1.26.5-abc.tmp", "bin", "go"), "x")

	s, err := Scan(h, false)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(s.Versions) != 0 {
		t.Fatalf("versions = %v, want none when includeVersions is false", s.Versions)
	}
	if len(s.Partials) != 1 {
		t.Fatalf("partials = %v, want the .tmp dir even when includeVersions is false", s.Partials)
	}
}

func TestScanOnEmptyHomeIsNotAnError(t *testing.T) {
	h, _ := scanHome(t)
	s, err := Scan(h, true)
	if err != nil {
		t.Fatalf("Scan on empty home: %v", err)
	}
	if len(s.Staging)+len(s.Partials)+len(s.Versions) != 0 {
		t.Fatalf("empty home yielded %+v", s)
	}
}
