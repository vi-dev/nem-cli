// Package clean plans and executes disk reclamation under $NEM_HOME.
package clean

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// ageRE accepts a whole number of days or hours. Sub-hour units are refused
// because they are meaningless for disk eviction, and a bare number is
// refused because its unit would be a guess.
var ageRE = regexp.MustCompile(`^([0-9]+)([dh])$`)

// maxAge caps --unused at a century. Without a cap the multiplication
// below overflows int64 and wraps: 213504d lands on roughly +25 minutes,
// which would evict every stamped version instead of none.
const maxAge = 36500 * 24 * time.Hour

// Age is the --unused flag value: how long a version must have gone unused
// before it is evictable.
type Age struct {
	raw string
	d   time.Duration
}

// Duration is the parsed window; zero means the flag was not set.
func (a Age) Duration() time.Duration { return a.d }

func (a Age) String() string { return a.raw }

func (a Age) Type() string { return "days|hours" }

// Set parses a value like "30d" or "12h".
func (a *Age) Set(s string) error {
	m := ageRE.FindStringSubmatch(s)
	if m == nil {
		return fmt.Errorf("invalid age %q: want whole days or hours, like 30d or 12h", s)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return fmt.Errorf("invalid age %q: want whole days or hours, like 30d or 12h", s)
	}
	if n == 0 {
		return fmt.Errorf("invalid age %q: must be greater than zero, like 30d or 12h", s)
	}
	unit := time.Hour
	if m[2] == "d" {
		unit = 24 * time.Hour
	}
	if n > int(maxAge/unit) {
		return fmt.Errorf("invalid age %q: must be no more than %dd, like 30d or 12h",
			s, int(maxAge/(24*time.Hour)))
	}
	a.raw, a.d = s, time.Duration(n)*unit
	return nil
}
