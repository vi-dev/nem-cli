package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAddRemoveWriteRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nem.toml")
	m := &Manifest{Path: path}

	if !AddTool(m, ToolKey{Name: "go"}, "v1.26.5") {
		t.Fatal("AddTool reported no change")
	}
	if !AddTool(m, ToolKey{Catalog: "dev", Name: "kubectl"}, "v1.34.1") {
		t.Fatal("AddTool 2 reported no change")
	}
	if AddTool(m, ToolKey{Name: "go"}, "v1.26.5") {
		t.Fatal("idempotent AddTool reported change")
	}
	if err := WriteManifest(m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	loaded, err := LoadManifest(path)
	if err != nil || len(loaded.Tools) != 2 {
		t.Fatalf("reload: %+v, %v", loaded, err)
	}
	if loaded.Tools[1].Key.String() != "dev:kubectl" {
		t.Fatalf("prefixed key lost: %+v", loaded.Tools)
	}

	if !RemoveTool(loaded, "kubectl") {
		t.Fatal("RemoveTool reported no change")
	}
	if RemoveTool(loaded, "absent") {
		t.Fatal("RemoveTool of absent reported change")
	}
	if err := WriteManifest(loaded); err != nil {
		t.Fatalf("WriteManifest 2: %v", err)
	}
	final, _ := LoadManifest(path)
	if len(final.Tools) != 1 || final.Tools[0].Key.Name != "go" {
		t.Fatalf("after remove: %+v", final.Tools)
	}
}

func TestWriteManifestNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nem.toml")
	m := &Manifest{Path: path}
	AddTool(m, ToolKey{Name: "go"}, "v1")
	if err := WriteManifest(m); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)

	m2, _ := LoadManifest(path)
	if err := WriteManifest(m2); err != nil { // nothing changed
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("no-op write touched the file")
	}
}
