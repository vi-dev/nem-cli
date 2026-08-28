// Package ocix owns all oras-go knowledge for nem: wire constants, catalog
// sync into a local OCI-layout mirror, and store reads.
package ocix

import (
	"errors"
	"fmt"
)

const (
	// MediaTypePkg is the media type of a pkg.yaml layer blob.
	MediaTypePkg = "application/vnd.nem.pkg.v2+yaml"
	// MediaTypeArchive is the media type of a package archive layer blob.
	MediaTypeArchive = "application/vnd.nem.archive.v2"
	// AnnotationSchemaVersion names the catalog index annotation carrying
	// the nem catalog schema version.
	AnnotationSchemaVersion = "org.vi-dev.nem.catalog.schemaVersion"
	// SchemaVersion is the nem catalog schema version this build understands.
	SchemaVersion    = "2"
	SchemaVersionInt = 2
	// AnnotationTitle is the OCI annotation carrying a package's name.
	AnnotationTitle = "org.opencontainers.image.title"
	// AnnotationDescription is the OCI annotation carrying a package's
	// description.
	AnnotationDescription = "org.opencontainers.image.description"
	// AnnotationVersion is the OCI annotation carrying a package's latest
	// version.
	AnnotationVersion = "org.opencontainers.image.version"
	// LocalTag is the tag under which a synced catalog index is stored in
	// the local OCI layout mirror.
	LocalTag = "catalog"

	// syncConcurrency bounds concurrent copy tasks (CopyIndexClosure's only user).
	syncConcurrency = 32
)

// ErrNotSynced indicates the local catalog store has not been synced yet.
var ErrNotSynced = errors.New("catalog store not synced")

// ErrArchiveNotFound indicates a package archive is not present in the
// registry: the archives repo or tag does not exist, or a resolved index
// has no manifest for the requested platform.
var ErrArchiveNotFound = errors.New("archive not found in registry")

// PkgNotInIndexError indicates a package name has no entry in the catalog index.
type PkgNotInIndexError struct{ Name string }

func (e *PkgNotInIndexError) Error() string {
	return fmt.Sprintf("package %s not in catalog index", e.Name)
}
