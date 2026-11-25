package verifier

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// longVersionRe matches a solc long version as the API sends it,
// "v0.8.28+commit.7893614a", optionally with a pre-release tag
// ("v0.8.31-pre.1+commit.b59566f6"). The leading v is optional on input.
var longVersionRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.]+))?\+commit\.([0-9a-f]{8})$`)

// normalizeVersion returns the canonical "v..." form, or "" when s is not a
// solc long version.
func normalizeVersion(s string) string {
	s = strings.TrimSpace(s)
	if !longVersionRe.MatchString(s) {
		return ""
	}
	if !strings.HasPrefix(s, "v") {
		s = "v" + s
	}
	return s
}

type semver struct {
	major, minor, patch int
	pre                 string
}

func parseSemver(v string) (semver, bool) {
	m := longVersionRe.FindStringSubmatch(v)
	if m == nil {
		return semver{}, false
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	c, _ := strconv.Atoi(m[3])
	return semver{a, b, c, m[4]}, true
}

// newer reports whether version a sorts before b in a newest-first list.
// A release sorts above a pre-release of the same number.
func newer(a, b string) bool {
	x, okx := parseSemver(a)
	y, oky := parseSemver(b)
	if !okx || !oky {
		return a > b
	}
	if x.major != y.major {
		return x.major > y.major
	}
	if x.minor != y.minor {
		return x.minor > y.minor
	}
	if x.patch != y.patch {
		return x.patch > y.patch
	}
	if (x.pre == "") != (y.pre == "") {
		return x.pre == ""
	}
	return x.pre > y.pre
}

func sortNewestFirst(vs []string) {
	sort.SliceStable(vs, func(i, j int) bool { return newer(vs[i], vs[j]) })
}

// fileNameRe is what a compiler file may be called, in the manifest or in
// the official list. No directories, nothing that walks out of the cache.
var fileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*\.js$`)

// sha256Re is a sha256 as the official list writes it: 0x and 64 hex digits.
var sha256Re = regexp.MustCompile(`^0x[0-9a-f]{64}$`)
