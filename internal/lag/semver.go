package lag

import (
	"regexp"
	"strconv"
)

// releaseTag is a semver version, with or without a leading v, without a
// prerelease (ADR 0002). Build metadata is allowed and ignored.
var releaseTag = regexp.MustCompile(`^(v?)(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:\+[0-9A-Za-z.-]+)?$`)

type version struct {
	parts [3]uint64
	v     bool
}

func parseRelease(tag string) (version, bool) {
	m := releaseTag.FindStringSubmatch(tag)
	if m == nil {
		return version{}, false
	}
	var out version
	out.v = m[1] == "v"
	for i := range 3 {
		n, err := strconv.ParseUint(m[i+2], 10, 64)
		if err != nil {
			return version{}, false
		}
		out.parts[i] = n
	}
	return out, true
}

// less orders versions; at the same version the tag without a v comes first,
// so the one with a v wins.
func (a version) less(b version) bool {
	for i := range 3 {
		if a.parts[i] != b.parts[i] {
			return a.parts[i] < b.parts[i]
		}
	}
	return !a.v && b.v
}

// latestRelease returns the tag with the greatest release version, or "" if
// no tag is one.
func latestRelease(tags []string) string {
	var best string
	var bestV version
	for _, tag := range tags {
		v, ok := parseRelease(tag)
		if !ok {
			continue
		}
		if best == "" || bestV.less(v) {
			best, bestV = tag, v
		}
	}
	return best
}
