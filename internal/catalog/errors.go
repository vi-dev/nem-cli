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
