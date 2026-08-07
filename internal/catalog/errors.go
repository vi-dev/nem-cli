package catalog

import (
	"fmt"
	"strings"
)

type PackageNotFoundError struct {
	Name     string
	Catalogs []string
}

func (e *PackageNotFoundError) Error() string {
	return fmt.Sprintf("package %s not found in catalog(s) %s", e.Name, strings.Join(e.Catalogs, ", "))
}

type CatalogNotFoundError struct{ Name string }

func (e *CatalogNotFoundError) Error() string {
	return fmt.Sprintf("catalog %s is not configured", e.Name)
}

type VersionNotFoundError struct{ Name, Version, Catalog string }

func (e *VersionNotFoundError) Error() string {
	return fmt.Sprintf("version %s of package %s not found in catalog %s", e.Version, e.Name, e.Catalog)
}

// DigestMismatchError reports that a locked package's catalog content no
// longer matches what was locked.
type DigestMismatchError struct{ Name, Version, Locked, Current string }

func (e *DigestMismatchError) Error() string {
	return fmt.Sprintf("catalog content for %s@%s changed (locked %s, now %s)", e.Name, e.Version, e.Locked, e.Current)
}
