package lag

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// DefaultIgnore are the paths that change nothing a release ships, in every
// repository alike (ADR 0006): CI, the Renovate config, agents' settings,
// the lint config and documentation. Build files such as Makefile or
// scripts/ are not among them, since some repositories ship them.
var DefaultIgnore = []string{
	".github/",
	".gitlab/",
	".claude/",
	"docs/",
	"renovate.json",
	"renovate.json5",
	".renovaterc",
	".renovaterc.json",
	".renovaterc.json5",
	"*.md",
	"LICENSE*",
	".gitignore",
	".gitattributes",
	".editorconfig",
	".golangci.yml",
	".golangci.yaml",
}

// Ignore matches the paths whose changes do not count towards a release.
// A pattern is one of
//
//   - dir/: everything under that directory at the top of the repository
//   - name: a file or directory of that name at any depth
//   - a/b: the whole path from the top
//
// each matched with path.Match, so * and the like work in all three.
type Ignore struct {
	dirs, names, paths []string
}

// ParseIgnore checks the patterns and makes an Ignore of them.
func ParseIgnore(patterns []string) (*Ignore, error) {
	ignore := &Ignore{}
	var errs []error
	for _, p := range patterns {
		dir, isDir := strings.CutSuffix(p, "/")
		if dir == "" {
			errs = append(errs, fmt.Errorf("the pattern %q matches nothing", p))
			continue
		}
		if _, err := path.Match(dir, ""); err != nil {
			errs = append(errs, fmt.Errorf("the pattern %q: %w", p, err))
			continue
		}
		switch {
		case isDir:
			ignore.dirs = append(ignore.dirs, dir)
		case strings.Contains(p, "/"):
			ignore.paths = append(ignore.paths, p)
		default:
			ignore.names = append(ignore.names, p)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return ignore, nil
}

// Match tells whether p, a path from the top of the repository, is to be
// ignored. A nil Ignore ignores nothing.
func (i *Ignore) Match(p string) bool {
	if i == nil {
		return false
	}
	for _, pattern := range i.paths {
		if match(pattern, p) {
			return true
		}
	}
	segments := strings.Split(p, "/")
	for _, s := range segments {
		for _, pattern := range i.names {
			if match(pattern, s) {
				return true
			}
		}
	}
	for n := 1; n < len(segments); n++ {
		dir := strings.Join(segments[:n], "/")
		for _, pattern := range i.dirs {
			if match(pattern, dir) {
				return true
			}
		}
	}
	return false
}

// empty tells whether i ignores nothing, so that no file needs reading.
func (i *Ignore) empty() bool {
	return i == nil || len(i.dirs)+len(i.names)+len(i.paths) == 0
}

func match(pattern, name string) bool {
	ok, _ := path.Match(pattern, name)
	return ok
}
