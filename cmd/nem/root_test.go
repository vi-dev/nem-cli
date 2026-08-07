package main

import (
	"bytes"
	"testing"

	"github.com/vi-dev/nem-cli/internal/report"
)

func TestUnknownCommandIsUsageError(t *testing.T) {
	root := newRoot()
	root.SetArgs([]string{"definitely-not-a-command"})
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	err := root.Execute()
	if err == nil {
		t.Fatal("want error for unknown command")
	}
	if ranHook {
		t.Fatal("hook ran for unknown command; usage errors must exit 2")
	}
}

func TestColorFlagValidation(t *testing.T) {
	root := newRoot()
	root.SetArgs([]string{"--color", "sometimes", "version"})
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	if err := root.Execute(); err == nil {
		t.Fatal("want error for invalid --color value")
	}
}

func TestResolveColorMode(t *testing.T) {
	cases := []struct {
		name    string
		flag    string
		noColor bool
		want    report.Mode
		wantErr bool
	}{
		{name: "auto without NO_COLOR", flag: "auto", noColor: false, want: report.ColorAuto},
		{name: "auto with NO_COLOR downgrades to never", flag: "auto", noColor: true, want: report.ColorNever},
		{name: "always with NO_COLOR still wins", flag: "always", noColor: true, want: report.ColorAlways},
		{name: "never with NO_COLOR stays never", flag: "never", noColor: true, want: report.ColorNever},
		{name: "invalid value errors", flag: "sometimes", noColor: false, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveColorMode(c.flag, c.noColor)
			if c.wantErr {
				if err == nil {
					t.Fatalf("want error for flag %q", c.flag)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveColorMode(%q, %v): %v", c.flag, c.noColor, err)
			}
			if got != c.want {
				t.Fatalf("resolveColorMode(%q, %v) = %v, want %v", c.flag, c.noColor, got, c.want)
			}
		})
	}
}
