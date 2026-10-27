// Package semver implements the subset of semantic versioning gala needs:
// parsing, semver.org ordering (including prerelease ordering), and bumps.
//
// Prerelease versions are fully supported by the comparator even though gala
// filters them out when planning, so prerelease channels can be added later
// without reworking the version layer.
package semver

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a semantic version. Prerelease is nil for a stable release.
type Version struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease []string
	Build      string
}

// Bump is the kind of version change a set of commits asks for.
type Bump int

const (
	// None means no release is due.
	None Bump = iota
	// Patch is a backwards-compatible bug fix.
	Patch
	// Minor is backwards-compatible functionality.
	Minor
	// Major is an incompatible API change.
	Major
)

// String names the bump for prose output.
func (b Bump) String() string {
	switch b {
	case Patch:
		return "patch"
	case Minor:
		return "minor"
	case Major:
		return "major"
	default:
		return "none"
	}
}

// Max returns the larger of two bumps.
func Max(a, b Bump) Bump {
	if a > b {
		return a
	}
	return b
}

// Parse parses a semantic version. A single leading "v" is accepted and
// dropped, because that is how tags are usually spelled.
func Parse(s string) (Version, error) {
	var v Version

	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return v, fmt.Errorf("empty version")
	}

	// Split off build metadata first: it never participates in ordering.
	if i := strings.IndexByte(s, '+'); i >= 0 {
		v.Build = s[i+1:]
		s = s[:i]
		if v.Build == "" {
			return v, fmt.Errorf("invalid version %q: empty build metadata", s+"+"+v.Build)
		}
	}

	// Split off the prerelease at the first '-': the numeric core can never
	// contain one, while a prerelease identifier can ('alpha-1').
	if i := strings.IndexByte(s, '-'); i >= 0 {
		pre := s[i+1:]
		s = s[:i]
		if pre == "" {
			return v, fmt.Errorf("invalid version: empty prerelease")
		}
		for _, id := range strings.Split(pre, ".") {
			if id == "" {
				return v, fmt.Errorf("invalid prerelease %q: empty identifier", pre)
			}
			if isNumeric(id) {
				if len(id) > 1 && id[0] == '0' {
					return v, fmt.Errorf("invalid prerelease %q: numeric identifier with leading zero", pre)
				}
			} else if !isAlphanumeric(id) {
				return v, fmt.Errorf("invalid prerelease %q: identifier %q contains illegal characters", pre, id)
			}
			v.Prerelease = append(v.Prerelease, id)
		}
	}

	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, fmt.Errorf("invalid version %q: want major.minor.patch", s)
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := parseNumeric(p)
		if err != nil {
			return v, fmt.Errorf("invalid version: %w", err)
		}
		nums[i] = n
	}
	v.Major, v.Minor, v.Patch = nums[0], nums[1], nums[2]
	return v, nil
}

// MustParse is Parse for compile-time constants in tests.
func MustParse(s string) Version {
	v, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

func parseNumeric(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty number in %q", s)
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, fmt.Errorf("number %q has a leading zero", s)
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("number %q is not numeric", s)
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("number %q out of range", s)
	}
	return n, nil
}

func isNumeric(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

func isAlphanumeric(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '-'
		if !ok {
			return false
		}
	}
	return len(s) > 0
}

// String renders the canonical form, without a leading "v".
func (v Version) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d.%d.%d", v.Major, v.Minor, v.Patch)
	if len(v.Prerelease) > 0 {
		b.WriteByte('-')
		b.WriteString(strings.Join(v.Prerelease, "."))
	}
	if v.Build != "" {
		b.WriteByte('+')
		b.WriteString(v.Build)
	}
	return b.String()
}

// Stable reports whether the version carries no prerelease identifiers.
func (v Version) Stable() bool {
	return len(v.Prerelease) == 0
}

// Bump applies a bump with plain semver arithmetic: there is no 0.x special
// casing, so 0.7.0 with a major bump becomes 1.0.0.
func (v Version) Bump(b Bump) Version {
	// A prerelease never survives a bump: the result is the next stable.
	v.Prerelease = nil
	v.Build = ""
	switch b {
	case Major:
		v.Major++
		v.Minor = 0
		v.Patch = 0
	case Minor:
		v.Minor++
		v.Patch = 0
	case Patch:
		v.Patch++
	}
	return v
}

// Compare orders two versions per semver.org: numeric fields first, then a
// prerelease against a release (prerelease sorts lower), then identifier by
// identifier.
func Compare(a, b Version) int {
	if c := cmpInt(a.Major, b.Major); c != 0 {
		return c
	}
	if c := cmpInt(a.Minor, b.Minor); c != 0 {
		return c
	}
	if c := cmpInt(a.Patch, b.Patch); c != 0 {
		return c
	}
	return cmpPrerelease(a.Prerelease, b.Prerelease)
}

// Less reports whether a sorts before b.
func Less(a, b Version) bool { return Compare(a, b) < 0 }

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpPrerelease(a, b []string) int {
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		return 1 // a release outranks any prerelease
	case len(b) == 0:
		return -1
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := cmpIdentifier(a[i], b[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(a), len(b))
}

func cmpIdentifier(a, b string) int {
	an, bn := isNumeric(a), isNumeric(b)
	switch {
	case an && bn:
		ai, _ := strconv.Atoi(a)
		bi, _ := strconv.Atoi(b)
		return cmpInt(ai, bi)
	case an:
		return -1 // numeric identifiers sort before alphanumeric ones
	case bn:
		return 1
	}
	return strings.Compare(a, b)
}
