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

func TestHintSuppressedByQuiet(t *testing.T) {
	c, _, errb := newTest(Options{Color: ColorNever})
	c.Hint("Run `nem catalog update dev` to sync it")
	if !strings.Contains(errb.String(), "  hint: Run `nem catalog update dev` to sync it\n") {
		t.Errorf("hint line wrong: %q", errb.String())
	}

	cq, _, errbq := newTest(Options{Quiet: true, Color: ColorNever})
	cq.Hint("nope")
	if strings.Contains(errbq.String(), "nope") {
		t.Errorf("quiet did not suppress hint: %q", errbq.String())
	}
}

func TestDataGoesToStdoutVerbatim(t *testing.T) {
	c, out, errb := newTest(Options{Color: ColorNever})
	c.Data("%s v%s\n", "go", "1.26.5")
	if got, want := out.String(), "go v1.26.5\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if errb.Len() != 0 {
		t.Errorf("data leaked to stderr: %q", errb.String())
	}
}

func TestDataIgnoresQuiet(t *testing.T) {
	c, out, _ := newTest(Options{Quiet: true, Color: ColorNever})
	c.Data("payload\n")
	if out.String() != "payload\n" {
		t.Errorf("quiet suppressed data output: %q", out.String())
	}
}

func TestPromptIgnoresQuiet(t *testing.T) {
	c, out, errb := newTest(Options{Quiet: true, Color: ColorNever})
	c.Prompt("Remove these? [y/N]: ")
	if errb.String() != "Remove these? [y/N]: \n" {
		t.Errorf("quiet suppressed a question the command blocks on: %q", errb.String())
	}
	if out.Len() != 0 {
		t.Errorf("prompt leaked to stdout: %q", out.String())
	}
}

func TestDataNeverColored(t *testing.T) {
	c, out, _ := newTest(Options{Color: ColorAlways})
	c.Data("plain\n")
	if strings.Contains(out.String(), "\x1b[") {
		t.Errorf("data output contains ANSI codes: %q", out.String())
	}
}

func TestJSONGoesToStdoutIndented(t *testing.T) {
	c, out, errb := newTest(Options{Color: ColorNever})
	if err := c.JSON(map[string]string{"name": "go"}); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if got, want := out.String(), "{\n  \"name\": \"go\"\n}\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if errb.Len() != 0 {
		t.Errorf("json leaked to stderr: %q", errb.String())
	}
}

func TestJSONIgnoresQuiet(t *testing.T) {
	c, out, _ := newTest(Options{Quiet: true, Color: ColorNever})
	if err := c.JSON([]int{1}); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if out.Len() == 0 {
		t.Errorf("quiet suppressed json output")
	}
}

func TestJSONReturnsEncodeError(t *testing.T) {
	c, _, _ := newTest(Options{Color: ColorNever})
	if err := c.JSON(func() {}); err == nil {
		t.Errorf("expected error encoding a func value")
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
