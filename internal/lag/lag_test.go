package lag_test

import (
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/yuanying/release-lag/internal/github"
	"github.com/yuanying/release-lag/internal/github/githubtest"
	"github.com/yuanying/release-lag/internal/lag"
)

const owner = "example-owner"

var (
	may1  = time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	june1 = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	july1 = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
)

func repo(name string, files ...string) *githubtest.Repo {
	return &githubtest.Repo{Owner: owner, Name: name, Files: files}
}

func collector(fake *githubtest.Server, exclude ...string) *lag.Collector {
	return &lag.Collector{
		GitHub:  github.NewClient(fake.URL, http.DefaultClient),
		Owner:   owner,
		Exclude: exclude,
	}
}

func names(repos []lag.Repo) []string {
	out := make([]string, 0, len(repos))
	for _, r := range repos {
		out = append(out, r.Name)
	}
	sort.Strings(out)
	return out
}

func find(t *testing.T, repos []lag.Repo, name string) lag.Repo {
	t.Helper()
	for _, r := range repos {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("%s is not in %v", name, names(repos))
	return lag.Repo{}
}

func TestOnlyRepositoriesWithARenovateConfigAreTracked(t *testing.T) {
	fake := githubtest.New(t,
		repo("root-json", "renovate.json", "main.go"),
		repo("root-json5", "renovate.json5"),
		repo("rc", ".renovaterc"),
		repo("rc-json", ".renovaterc.json"),
		repo("rc-json5", ".renovaterc.json5"),
		repo("github-json", ".github/renovate.json", ".github/workflows/ci.yml"),
		repo("github-json5", ".github/renovate.json5"),
		repo("gitlab-json", ".gitlab/renovate.json"),
		repo("gitlab-json5", ".gitlab/renovate.json5"),
		repo("no-config", "README.md", ".github/workflows/ci.yml"),
		repo("elsewhere", "docs/renovate.json", "config/.renovaterc"),
		repo("a-directory", "renovate.json/readme.md"),
		&githubtest.Repo{Owner: owner, Name: "empty", Empty: true},
	)
	got, err := collector(fake).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := "github-json,github-json5,gitlab-json,gitlab-json5,rc,rc-json,rc-json5,root-json,root-json5"
	if strings.Join(names(got), ",") != want {
		t.Errorf("tracked %v, want %s", names(got), want)
	}
}

func TestTheConfigIsLookedForOnTheDefaultBranch(t *testing.T) {
	r := repo("trunk-based", "renovate.json")
	r.DefaultBranch = "trunk"
	fake := githubtest.New(t, r)
	got, err := collector(fake).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].DefaultBranch != "trunk" {
		t.Fatalf("got %+v", got)
	}
	if fake.Requests("/repos/example-owner/trunk-based/git/trees/trunk") != 1 {
		t.Error("the tree of trunk was not read")
	}
}

func TestForksArchivesOtherOwnersAndExcludedAreLeftOut(t *testing.T) {
	fork := repo("forked", "renovate.json")
	fork.Fork = true
	archived := repo("archived", "renovate.json")
	archived.Archived = true
	other := &githubtest.Repo{Owner: "someone-else", Name: "theirs", Files: []string{"renovate.json"}}
	sameOwnerOtherCase := &githubtest.Repo{Owner: "Example-Owner", Name: "cased", Files: []string{"renovate.json"}}
	fake := githubtest.New(t, fork, archived, other, sameOwnerOtherCase,
		repo("kept", "renovate.json"),
		repo("excluded", "renovate.json"),
		repo("also-excluded", "renovate.json"),
	)
	got, err := collector(fake, "excluded", "also-excluded").Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names(got), ",") != "cased,kept" {
		t.Errorf("tracked %v, want [cased kept]", names(got))
	}
	// What is left out costs no further request.
	for _, name := range []string{"forked", "archived", "excluded"} {
		if n := fake.Requests("/repos/example-owner/" + name + "/git/trees/main"); n != 0 {
			t.Errorf("%s: %d tree requests", name, n)
		}
	}
}

func TestTheListIsRebuiltOnEveryCollection(t *testing.T) {
	fake := githubtest.New(t, repo("one", "renovate.json"))
	c := collector(fake)
	if got, _ := c.Collect(t.Context()); strings.Join(names(got), ",") != "one" {
		t.Fatalf("first: %v", names(got))
	}
	fake.SetRepos(repo("one"), repo("two", ".github/renovate.json"))
	if got, _ := c.Collect(t.Context()); strings.Join(names(got), ",") != "two" {
		t.Fatalf("second: %v", names(got))
	}
}

func TestARepositoryWithoutReleaseTagsIsUnreleased(t *testing.T) {
	noTags := repo("no-tags", "renovate.json")
	onlyPre := repo("only-prereleases", "renovate.json")
	onlyPre.Tags = []githubtest.Tag{{Name: "v1.0.0-rc.1", CommitDate: may1}, {Name: "nightly", CommitDate: may1}}
	fake := githubtest.New(t, noTags, onlyPre)
	got, err := collector(fake).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got {
		if r.Released() || r.LatestTag != "" || !r.ReleasedAt.IsZero() || r.CompareURL != "" {
			t.Errorf("%s: %+v, want unreleased", r.Name, r)
		}
	}
	if n := fake.Requests("/repos/example-owner/no-tags/compare/"); n != 0 {
		t.Errorf("compared %d times", n)
	}
}

func TestTheGreatestReleaseIsTheLatest(t *testing.T) {
	r := repo("app", "renovate.json")
	r.Tags = []githubtest.Tag{
		{Name: "v1.9.0", Annotated: true, TagDate: may1},
		{Name: "v1.10.0", Annotated: true, TagDate: june1},
		{Name: "v2.0.0-rc.1", Annotated: true, TagDate: july1},
	}
	r.Compare = map[string][]githubtest.Commit{"v1.10.0...main": nil}
	fake := githubtest.New(t, r)
	got, err := collector(fake).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	app := find(t, got, "app")
	if app.LatestTag != "v1.10.0" || !app.ReleasedAt.Equal(june1) {
		t.Errorf("latest %s at %v, want v1.10.0 at %v", app.LatestTag, app.ReleasedAt, june1)
	}
	if app.CompareURL != "https://github.com/example-owner/app/compare/v1.10.0...main" {
		t.Errorf("CompareURL = %q", app.CompareURL)
	}
}

func TestReleaseDates(t *testing.T) {
	annotated := repo("annotated", "renovate.json")
	// Tagged a month after the commit: the tag's date counts.
	annotated.Tags = []githubtest.Tag{{Name: "v1.0.0", Annotated: true, TagDate: june1, CommitDate: may1}}
	annotated.Compare = map[string][]githubtest.Commit{"v1.0.0...main": nil}
	lightweight := repo("lightweight", "renovate.json")
	lightweight.Tags = []githubtest.Tag{{Name: "1.0.0", CommitDate: may1}}
	lightweight.Compare = map[string][]githubtest.Commit{"1.0.0...main": nil}
	fake := githubtest.New(t, annotated, lightweight)
	got, err := collector(fake).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if d := find(t, got, "annotated").ReleasedAt; !d.Equal(june1) {
		t.Errorf("annotated: %v, want the tag's date %v", d, june1)
	}
	if d := find(t, got, "lightweight").ReleasedAt; !d.Equal(may1) {
		t.Errorf("lightweight: %v, want the commit's date %v", d, may1)
	}
}

func TestCommitsSinceTheRelease(t *testing.T) {
	r := repo("app", "renovate.json")
	r.Tags = []githubtest.Tag{{Name: "v0.3.0", CommitDate: may1}}
	r.Compare = map[string][]githubtest.Commit{"v0.3.0...main": {
		{SHA: "1", Login: "renovate[bot]", Name: "renovate[bot]"},
		{SHA: "2", Login: "renovate[bot]", Name: "renovate[bot]"},
		{SHA: "3", Login: "a-person", Name: "A Person"},
		// The merge of a pull request is not a change of its own.
		{SHA: "4", Login: "a-person", Name: "A Person", Merge: true},
		{SHA: "5", Login: "renovate[bot]", Merge: true},
		// No account behind the email: the git name still tells.
		{SHA: "6", Name: "renovate[bot]"},
		{SHA: "7", Name: "Someone Unlinked"},
	}}
	fake := githubtest.New(t, r)
	fake.PerPage = 3
	got, err := collector(fake).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	app := find(t, got, "app")
	if app.Commits != 5 {
		t.Errorf("Commits = %d, want 5 (merges left out)", app.Commits)
	}
	if app.NonRenovateCommits != 2 {
		t.Errorf("NonRenovateCommits = %d, want 2", app.NonRenovateCommits)
	}
}

func TestRepositoryDetails(t *testing.T) {
	r := repo("secret", "renovate.json")
	r.Private = true
	fake := githubtest.New(t, r)
	got, err := collector(fake).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	s := find(t, got, "secret")
	if !s.Private || s.URL != "https://github.com/example-owner/secret" || s.DefaultBranch != "main" {
		t.Errorf("got %+v", s)
	}
}

func TestAFailingRepositoryDoesNotHideTheOthers(t *testing.T) {
	good := repo("good", "renovate.json")
	bad := repo("bad", "renovate.json")
	bad.Tags = []githubtest.Tag{{Name: "v1.0.0", CommitDate: may1}}
	bad.Compare = map[string][]githubtest.Commit{"v1.0.0...main": nil}
	fake := githubtest.New(t, good, bad)
	fake.Fail("/repos/example-owner/bad/compare/v1.0.0...main", http.StatusInternalServerError)
	got, err := collector(fake).Collect(t.Context())
	if err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("err = %v, want one naming bad", err)
	}
	if find(t, got, "good").Error != "" {
		t.Error("good has an error")
	}
	if b := find(t, got, "bad"); b.Error == "" || b.LatestTag != "v1.0.0" {
		t.Errorf("bad = %+v, want its tag and the error", b)
	}
}

func TestListingFailureIsAnError(t *testing.T) {
	fake := githubtest.New(t)
	fake.Fail("/installation/repositories", http.StatusUnauthorized)
	if _, err := collector(fake).Collect(t.Context()); err == nil {
		t.Fatal("no error")
	}
}

func TestOrderLongestWithoutReleaseFirst(t *testing.T) {
	mk := func(name string, tag string, date time.Time) *githubtest.Repo {
		r := repo(name, "renovate.json")
		if tag != "" {
			r.Tags = []githubtest.Tag{{Name: tag, CommitDate: date}}
			r.Compare = map[string][]githubtest.Commit{tag + "...main": nil}
		}
		return r
	}
	fake := githubtest.New(t,
		mk("recent", "v1.0.0", july1),
		mk("unreleased-b", "", time.Time{}),
		mk("old", "v1.0.0", may1),
		mk("middle", "v1.0.0", june1),
		mk("unreleased-a", "", time.Time{}),
	)
	got, err := collector(fake).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, r := range got {
		order = append(order, r.Name)
	}
	want := "unreleased-a,unreleased-b,old,middle,recent"
	if strings.Join(order, ",") != want {
		t.Errorf("order %v, want %s", order, want)
	}
}
