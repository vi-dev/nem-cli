package report

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func newTest(opts Options) (*Console, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	return New(&out, &errb, opts), &out, &errb
}

func TestLinesAndStreams(t *testing.T) {
	c, out, errb := newTest(Options{Color: ColorNever})
	c.Success("Installed go v1.26.5")
	c.Warn("Catalog dev is stale")
	c.Info("Resolving")
	c.Debug("hidden without verbose")

	if out.Len() != 0 {
		t.Fatalf("narration leaked to stdout: %q", out.String())
	}
	got := errb.String()
	for _, want := range []string{"OK Installed go v1.26.5\n", "WARN Catalog dev is stale\n", "Resolving\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "hidden") {
		t.Errorf("debug shown without verbose")
	}
}

func TestQuietSuppressesNarrationNotDiagnostics(t *testing.T) {
	c, _, errb := newTest(Options{Quiet: true, Color: ColorNever})
	c.Info("nope")
	c.Success("nope")
	c.Warn("still here")
	got := errb.String()
	if strings.Contains(got, "nope") {
		t.Errorf("quiet did not suppress narration: %q", got)
	}
	if !strings.Contains(got, "WARN still here") {
		t.Errorf("quiet suppressed diagnostics: %q", got)
	}
}

func TestErrorCapitalizesDisplayOnly(t *testing.T) {
	c, _, errb := newTest(Options{Color: ColorNever})
	c.Error(errors.New("parse nem.toml: bad value"), "Run `nem status`")
	got := errb.String()
	if !strings.Contains(got, "ERROR Parse nem.toml: bad value\n") {
		t.Errorf("error line wrong: %q", got)
	}
	if !strings.Contains(got, "  hint: Run `nem status`\n") {
		t.Errorf("hint line wrong: %q", got)
	}
}

func TestUnicodeGlyphsWhenColored(t *testing.T) {
	c, _, errb := newTest(Options{Color: ColorAlways})
	c.Success("Done")
	if !strings.Contains(errb.String(), "✓") {
		t.Errorf("expected unicode glyph, got %q", errb.String())
	}
}

func TestTable(t *testing.T) {
	c, out, _ := newTest(Options{Color: ColorNever})
	c.Table([]string{"package", "version"}, [][]string{
		{"go", "v1.26.5"},
		{"kubectl", "v1.34.1"},
	})
	want := "PACKAGE  VERSION\ngo       v1.26.5\nkubectl  v1.34.1\n"
	if out.String() != want {
		t.Errorf("table:\n%q\nwant:\n%q", out.String(), want)
	}
}

func TestTableRaggedRows(t *testing.T) {
	c, out, _ := newTest(Options{Color: ColorNever})
	c.Table([]string{"a", "b"}, [][]string{
		{"1", "2", "3"}, // extra cell must not panic
		{"4"},           // short row pads, doesn't drop
	})
	got := out.String()
	if !strings.Contains(got, "3") || !strings.Contains(got, "4") {
		t.Errorf("ragged rows mishandled:\n%q", got)
	}
}

func TestColorAutoFollowsTTY(t *testing.T) {
	c, _, errb := newTest(Options{Color: ColorAuto, IsTTY: true})
	c.Success("On")
	if !strings.Contains(errb.String(), "✓") {
		t.Errorf("ColorAuto with TTY should color: %q", errb.String())
	}
	c2, _, errb2 := newTest(Options{Color: ColorAuto, IsTTY: false})
	c2.Success("Off")
	if !strings.Contains(errb2.String(), "OK Off") {
		t.Errorf("ColorAuto without TTY should be plain: %q", errb2.String())
	}
}
