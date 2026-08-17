package spec

import "strings"

// CompareVersions orders two version strings: >0 when a is newer, 0 when
// equal, <0 when older. A single leading "v" before a digit is ignored,
// so "v1.2.3" equals "1.2.3". The release part (everything before the
// first "-") is compared as digit and non-digit runs: digit runs compare
// numerically (leading-zero-safe), other runs lexically, and a longer
// release part outranks its prefix — so numeric build suffixes rank
// ("25.0.4_10" > "25.0.4_7" > "25.0.4"), as do four-part versions and
// letter patches ("1.1.1a" > "1.1.1"). A "-" suffix is a pre-release and
// ranks below its release ("1.2.3-rc1" < "1.2.3"); pre-releases compare
// against each other by the same run rules. The last two points deviate
// from strict semver deliberately: catalog upstreams ship build suffixes
// and appended patches as real, newer releases.
func CompareVersions(a, b string) int {
	ar, apre := splitVersion(a)
	br, bpre := splitVersion(b)
	if c := compareRuns(ar, br); c != 0 {
		return c
	}
	switch {
	case apre == bpre:
		return 0
	case apre == "":
		return 1
	case bpre == "":
		return -1
	}
	return compareRuns(apre, bpre)
}

// splitVersion drops a leading "v" spelling and separates the release
// part from the pre-release part at the first "-".
func splitVersion(v string) (release, pre string) {
	if len(v) > 1 && v[0] == 'v' && versionDigit(v[1]) {
		v = v[1:]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		return v[:i], v[i+1:]
	}
	return v, ""
}

// compareRuns orders two strings by maximal digit and non-digit runs.
func compareRuns(a, b string) int {
	as, bs := runs(a), runs(b)
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, y := as[i], bs[i]
		if x == y {
			continue
		}
		if versionDigit(x[0]) && versionDigit(y[0]) {
			xt, yt := strings.TrimLeft(x, "0"), strings.TrimLeft(y, "0")
			if len(xt) != len(yt) {
				return len(xt) - len(yt)
			}
			if c := strings.Compare(xt, yt); c != 0 {
				return c
			}
			continue // differ only in leading zeros
		}
		return strings.Compare(x, y)
	}
	return len(as) - len(bs)
}

// runs splits v into maximal runs of digits and non-digits.
func runs(v string) []string {
	var segs []string
	for i := 0; i < len(v); {
		j := i
		for j < len(v) && versionDigit(v[j]) == versionDigit(v[i]) {
			j++
		}
		segs = append(segs, v[i:j])
		i = j
	}
	return segs
}

func versionDigit(c byte) bool { return c >= '0' && c <= '9' }
