package envx

import "strings"

var reservedNames = map[string]bool{
	"PATH":             true,
	"PROMPT_COMMAND":   true,
	"BASH_ENV":         true,
	"ENV":              true,
	"IFS":              true,
	"PS1":              true,
	"CDPATH":           true,
	"FPATH":            true,
	"MANPATH":          true,
	"MAILPATH":         true,
	"MODULE_PATH":      true,
	"FIGNORE":          true,
	"PSVAR":            true,
	"WATCH":            true,
	"GCONV_PATH":       true,
	"GLIBC_TUNABLES":   true,
	"LOCPATH":          true,
	"NLSPATH":          true,
	"GETCONF_DIR":      true,
	"HOSTALIASES":      true,
	"RESOLV_HOST_CONF": true,
}

// IsReserved reports whether name is on the denylist (case-insensitive),
// including the NEM_*/LD_*/DYLD_* prefixes and the *_SET suffix.
func IsReserved(name string) bool {
	upper := strings.ToUpper(name)

	if reservedNames[upper] {
		return true
	}

	// Check for NEM_, LD_, DYLD_ prefixes
	if strings.HasPrefix(upper, "NEM_") || strings.HasPrefix(upper, "LD_") || strings.HasPrefix(upper, "DYLD_") {
		return true
	}

	// Check for _SET suffix
	if strings.HasSuffix(upper, "_SET") {
		return true
	}

	return false
}
