package main

import (
	"errors"
	"fmt"

	"github.com/vi-dev/nem-cli/internal/catalog"
	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/resolve"
)

// hintFor maps a known error into the remediation nem suggests alongside
// it; unrecognized errors get no hint.
func hintFor(err error) string {
	if errors.Is(err, ocix.ErrNotSynced) {
		return "Run `nem catalog update`"
	}
	var cnf *catalog.CatalogNotFoundError
	if errors.As(err, &cnf) {
		return "Run `nem catalog add <name> <ref>`"
	}
	var pnf *catalog.PackageNotFoundError
	if errors.As(err, &pnf) {
		return "Check the package name or run `nem catalog update`"
	}
	var upe *resolve.UnsupportedPlatformError
	if errors.As(err, &upe) {
		return "This package supports none of nem's platforms"
	}
	var dme *catalog.DigestMismatchError
	if errors.As(err, &dme) {
		return "Re-lock with `nem use <pkg>@<version>`"
	}
	var ute *UnpinnedToolsError
	if errors.As(err, &ute) {
		return "Pin exact versions in nem.toml or run `nem use <pkg>@<version>`"
	}
	var pce *resolve.PinConflictError
	if errors.As(err, &pce) {
		return fmt.Sprintf("Re-pin with `nem use %s@%s` or unuse the tool requiring it", pce.Name, pce.Required)
	}
	return ""
}
