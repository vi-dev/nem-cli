package spec

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFormatIdempotentAndParseEqual(t *testing.T) {
	for _, name := range []string{"prebuilt.yaml", "source-commented.yaml"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "format", name))
			if err != nil {
				t.Fatal(err)
			}
			once, err := Format(data)
			if err != nil {
				t.Fatal(err)
			}
			twice, err := Format(once)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(once, twice) {
				t.Fatalf("Format not idempotent:\n--- once ---\n%s\n--- twice ---\n%s", once, twice)
			}
			if !bytes.HasSuffix(once, []byte("\n")) {
				t.Fatal("formatted output must end with a newline")
			}
			before, err := Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			after, err := Parse(once)
			if err != nil {
				t.Fatalf("formatted output no longer parses: %v", err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("Format changed the parsed package")
			}
		})
	}
}

func TestFormatPreservesComments(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "format", "source-commented.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Format(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, comment := range []string{
		"macOS bakes absolute install names",
		"keep both libs consistent",
	} {
		if !strings.Contains(string(out), comment) {
			t.Errorf("comment %q lost by Format", comment)
		}
	}
}
