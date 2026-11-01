package lag_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/yuanying/release-lag/internal/github/githubtest"
	"github.com/yuanying/release-lag/internal/lag"
)

func TestIgnoreMatch(t *testing.T) {
	ignore, err := lag.ParseIgnore(lag.DefaultIgnore)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		".github/workflows/ci.yml":   true,
		".github/renovate.json":      true,
		".gitlab/ci.yml":             true,
		".claude/skills/x/SKILL.md":  true,
		"docs/adr/0001-a.md":         true,
		"docs/diagram.svg":           true,
		"renovate.json":              true,
		"renovate.json5":             true,
		".renovaterc":                true,
		".renovaterc.json":           true,
		".renovaterc.json5":          true,
		"README.md":                  true,
		"wiki/topics/home.md":        true,
		"LICENSE":                    true,
		"LICENSE.txt":                true,
		".gitignore":                 true,
		"web/.gitignore":             true,
		".gitattributes":             true,
		".editorconfig":              true,
		".golangci.yml":              true,
		".golangci.yaml":             true,
		"main.go":                    false,
		"go.mod":                     false,
		"Dockerfile":                 false,
		"web/Dockerfile":             false,
		"Makefile":                   false,
		"scripts/next-release.sh":    false,
		".dockerignore":              false,
		"deploy/crd/policies.yaml":   false,
		"web/docs/page.html":         false, // docs/ is a directory at the top only
		"documents/x.txt":            false,
		"README.md.tmpl":             false,
		"internal/github/github.go":  false,
		"cmd/release-lag/.github.go": false,
	} {
		if got := ignore.Match(path); got != want {
			t.Errorf("Match(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestIgnorePatternForms(t *testing.T) {
	ignore, err := lag.ParseIgnore([]string{"deploy/*/README.txt", "hack/", "*.svg"})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		"deploy/crd/README.txt":   true,
		"deploy/README.txt":       false,
		"deploy/a/b/README.txt":   false,
		"hack/live.yaml":          true,
		"hack/a/b.sh":             true,
		"hack":                    false, // a file named so, not the directory
		"x/hack/live.yaml":        false,
		"img/logo.svg":            true,
		"logo.svg":                true,
		"logo.svg.go":             false,
		"internal/lag/semver.go":  false,
		"deploy/crd/policies.txt": false,
	} {
		if got := ignore.Match(path); got != want {
			t.Errorf("Match(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestIgnoreRejectsBadPatterns(t *testing.T) {
	for _, pattern := range []string{"", "[", "docs/[/"} {
		if _, err := lag.ParseIgnore([]string{pattern}); err == nil {
			t.Errorf("ParseIgnore(%q) gave no error", pattern)
		}
	}
}

func TestANilIgnoreMatchesNothing(t *testing.T) {
	var ignore *lag.Ignore
	if ignore.Match("README.md") {
		t.Error("a nil Ignore matched")
	}
}

func ignoring(t *testing.T, fake *githubtest.Server) *lag.Collector {
	t.Helper()
	c := collector(fake)
	ignore, err := lag.ParseIgnore(lag.DefaultIgnore)
	if err != nil {
		t.Fatal(err)
	}
	c.Ignore = ignore
	return c
}

func TestCommitsChangingNothingReleasedAreNotCounted(t *testing.T) {
	r := repo("app", "renovate.json")
	r.Tags = []githubtest.Tag{{Name: "v0.3.0", CommitDate: may1}}
	r.Compare = map[string][]githubtest.Commit{"v0.3.0...main": {
		{SHA: "1", Login: "renovate[bot]", Name: "renovate[bot]", Files: []string{"go.mod", "go.sum"}},
		{SHA: "2", Login: "renovate[bot]", Name: "renovate[bot]", Files: []string{".github/workflows/ci.yml"}},
		{SHA: "3", Login: "a-person", Name: "A Person", Files: []string{"renovate.json"}},
		{SHA: "4", Login: "a-person", Name: "A Person", Files: []string{"README.md", "main.go"}},
		{SHA: "5", Login: "a-person", Name: "A Person", Files: []string{"docs/adr/0002-x.md", "LICENSE"}},
		// Merge commits stay uncounted whatever they change.
		{SHA: "6", Login: "a-person", Name: "A Person", Merge: true, Files: []string{"main.go"}},
	}}
	fake := githubtest.New(t, r)
	got, err := ignoring(t, fake).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	app := find(t, got, "app")
	if app.Commits != 2 || app.NonRenovateCommits != 1 {
		t.Errorf("Commits = %d, NonRenovateCommits = %d; want 2 and 1", app.Commits, app.NonRenovateCommits)
	}
	if app.CompareURL == "" {
		t.Error("CompareURL is empty")
	}
	if n := fake.Requests("/repos/example-owner/app/commits/6"); n != 0 {
		t.Errorf("the merge commit was read %d times", n)
	}
}

func TestAComparisonChangingNothingReleasedReadsNoCommit(t *testing.T) {
	r := repo("app", "renovate.json")
	r.Tags = []githubtest.Tag{{Name: "v1.0.0", CommitDate: may1}}
	r.Compare = map[string][]githubtest.Commit{"v1.0.0...main": {
		{SHA: "1", Login: "a-person", Name: "A Person", Files: []string{"renovate.json"}},
		{SHA: "2", Login: "renovate[bot]", Name: "renovate[bot]", Files: []string{".github/workflows/ci.yml"}},
	}}
	fake := githubtest.New(t, r)
	got, err := ignoring(t, fake).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	app := find(t, got, "app")
	if app.Commits != 0 || app.NonRenovateCommits != 0 {
		t.Errorf("Commits = %d, NonRenovateCommits = %d; want 0", app.Commits, app.NonRenovateCommits)
	}
	if app.CompareURL == "" {
		t.Error("CompareURL is empty")
	}
	for _, sha := range []string{"1", "2"} {
		if n := fake.Requests("/repos/example-owner/app/commits/" + sha); n != 0 {
			t.Errorf("commit %s was read %d times", sha, n)
		}
	}
}

func TestATruncatedFileListIsNotTrusted(t *testing.T) {
	// GitHub lists 300 files for a comparison. When they are all ignored,
	// the ones it left out may not be.
	var docs []string
	for i := range 300 {
		docs = append(docs, fmt.Sprintf("docs/page-%03d.md", i))
	}
	r := repo("app", "renovate.json")
	r.Tags = []githubtest.Tag{{Name: "v1.0.0", CommitDate: may1}}
	r.Compare = map[string][]githubtest.Commit{"v1.0.0...main": {
		{SHA: "1", Login: "a-person", Name: "A Person", Files: docs},
		{SHA: "2", Login: "a-person", Name: "A Person", Files: []string{"main.go"}},
	}}
	fake := githubtest.New(t, r)
	got, err := ignoring(t, fake).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if app := find(t, got, "app"); app.Commits != 1 || app.NonRenovateCommits != 1 {
		t.Errorf("Commits = %d, NonRenovateCommits = %d; want 1 and 1", app.Commits, app.NonRenovateCommits)
	}
}

func TestEachCommitIsReadOnce(t *testing.T) {
	r := repo("app", "renovate.json")
	r.Tags = []githubtest.Tag{{Name: "v1.0.0", CommitDate: may1}}
	r.Compare = map[string][]githubtest.Commit{"v1.0.0...main": {
		{SHA: "1", Login: "a-person", Name: "A Person", Files: []string{"main.go"}},
		{SHA: "2", Login: "a-person", Name: "A Person", Files: []string{"README.md"}},
	}}
	fake := githubtest.New(t, r)
	c := ignoring(t, fake)
	for range 3 {
		got, err := c.Collect(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if app := find(t, got, "app"); app.Commits != 1 {
			t.Errorf("Commits = %d, want 1", app.Commits)
		}
	}
	for _, sha := range []string{"1", "2"} {
		if n := fake.Requests("/repos/example-owner/app/commits/" + sha); n != 1 {
			t.Errorf("commit %s was read %d times, want once", sha, n)
		}
	}
}

func TestWithoutIgnoreNoCommitIsRead(t *testing.T) {
	r := repo("app", "renovate.json")
	r.Tags = []githubtest.Tag{{Name: "v1.0.0", CommitDate: may1}}
	r.Compare = map[string][]githubtest.Commit{"v1.0.0...main": {
		{SHA: "1", Login: "a-person", Name: "A Person", Files: []string{"README.md"}},
	}}
	fake := githubtest.New(t, r)
	got, err := collector(fake).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if app := find(t, got, "app"); app.Commits != 1 || app.NonRenovateCommits != 1 {
		t.Errorf("Commits = %d, NonRenovateCommits = %d; want 1 and 1", app.Commits, app.NonRenovateCommits)
	}
	if n := fake.Requests("/repos/example-owner/app/commits/1"); n != 0 {
		t.Errorf("the commit was read %d times", n)
	}
}

func TestAFailedCommitReadLeavesTheCountsOut(t *testing.T) {
	r := repo("app", "renovate.json")
	r.Tags = []githubtest.Tag{{Name: "v1.0.0", CommitDate: may1}}
	r.Compare = map[string][]githubtest.Commit{"v1.0.0...main": {
		{SHA: "1", Login: "a-person", Name: "A Person", Files: []string{"main.go"}},
	}}
	fake := githubtest.New(t, r)
	fake.Fail("/repos/example-owner/app/commits/1", http.StatusInternalServerError)
	got, err := ignoring(t, fake).Collect(t.Context())
	if err == nil {
		t.Fatal("no error")
	}
	app := find(t, got, "app")
	if app.Error == "" || app.CompareURL != "" || app.LatestTag != "v1.0.0" {
		t.Errorf("app = %+v; want the tag, an error and no counts", app)
	}
}
