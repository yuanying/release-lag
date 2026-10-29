package github_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/yuanying/release-lag/internal/github"
	"github.com/yuanying/release-lag/internal/github/githubtest"
)

func client(fake *githubtest.Server) *github.Client {
	return github.NewClient(fake.URL, http.DefaultClient)
}

func TestListRepositoriesFollowsPages(t *testing.T) {
	var repos []*githubtest.Repo
	for _, name := range []string{"alpha", "bravo", "charlie", "delta", "echo"} {
		repos = append(repos, &githubtest.Repo{Owner: "example", Name: name, Private: name == "bravo", Fork: name == "charlie", Archived: name == "delta", DefaultBranch: map[string]string{"echo": "trunk"}[name]})
	}
	fake := githubtest.New(t, repos...)
	fake.PerPage = 2

	got, err := client(fake).ListInstallationRepositories(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d repositories, want 5", len(got))
	}
	if fake.Requests("/installation/repositories") != 3 {
		t.Errorf("%d requests, want 3 pages", fake.Requests("/installation/repositories"))
	}
	want := github.Repository{Owner: "example", Name: "bravo", DefaultBranch: "main", Private: true, HTMLURL: "https://github.com/example/bravo"}
	if got[1] != want {
		t.Errorf("got %+v, want %+v", got[1], want)
	}
	if !got[2].Fork || !got[3].Archived || got[4].DefaultBranch != "trunk" {
		t.Errorf("flags lost: %+v", got)
	}
}

func TestTree(t *testing.T) {
	fake := githubtest.New(t, &githubtest.Repo{Owner: "example", Name: "app", Files: []string{"README.md", ".github/renovate.json", ".github/workflows/ci.yml"}})
	c := client(fake)

	root, err := c.Tree(t.Context(), "example", "app", "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(root) != 2 || root[0].Path != ".github" || root[0].Type != "tree" || root[1].Path != "README.md" || root[1].Type != "blob" {
		t.Fatalf("root = %+v", root)
	}
	sub, err := c.Tree(t.Context(), "example", "app", root[0].SHA)
	if err != nil {
		t.Fatal(err)
	}
	if len(sub) != 2 || sub[0].Path != "renovate.json" || sub[1].Path != "workflows" {
		t.Fatalf(".github = %+v", sub)
	}
}

func TestTreeOfAnEmptyRepository(t *testing.T) {
	fake := githubtest.New(t, &githubtest.Repo{Owner: "example", Name: "app", Empty: true})
	_, err := client(fake).Tree(t.Context(), "example", "app", "main")
	if !errors.Is(err, github.ErrEmpty) {
		t.Fatalf("err = %v, want ErrEmpty", err)
	}
}

func TestTreeNotFound(t *testing.T) {
	fake := githubtest.New(t, &githubtest.Repo{Owner: "example", Name: "app"})
	_, err := client(fake).Tree(t.Context(), "example", "app", "gone")
	if !errors.Is(err, github.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestTagsAndTheirDates(t *testing.T) {
	tagDate := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	commitDate := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	fake := githubtest.New(t, &githubtest.Repo{Owner: "example", Name: "app", Tags: []githubtest.Tag{
		{Name: "v1.0.0", Annotated: true, TagDate: tagDate, CommitDate: commitDate},
		{Name: "v0.9.0", CommitDate: commitDate},
	}})
	c := client(fake)

	tags, err := c.ListTags(t.Context(), "example", "app")
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 2 || tags[0].Name != "v1.0.0" || !tags[0].Annotated || tags[1].Name != "v0.9.0" || tags[1].Annotated {
		t.Fatalf("tags = %+v", tags)
	}
	for i, want := range []time.Time{tagDate, commitDate} {
		got, err := c.TagDate(t.Context(), "example", "app", tags[i])
		if err != nil {
			t.Fatal(err)
		}
		if !got.Equal(want) {
			t.Errorf("%s: date %v, want %v", tags[i].Name, got, want)
		}
	}
}

func TestTagsOfAnEmptyRepository(t *testing.T) {
	fake := githubtest.New(t, &githubtest.Repo{Owner: "example", Name: "app", Empty: true})
	tags, err := client(fake).ListTags(t.Context(), "example", "app")
	if err != nil || len(tags) != 0 {
		t.Fatalf("tags = %v, err = %v; want none", tags, err)
	}
}

func TestCompareFollowsPages(t *testing.T) {
	commits := []githubtest.Commit{
		{SHA: "a", Login: "renovate[bot]", Name: "renovate[bot]"},
		{SHA: "b", Login: "someone", Name: "Some One"},
		{SHA: "c", Name: "Unlinked"},
		{SHA: "d", Login: "someone", Merge: true},
		{SHA: "e", Login: "renovate[bot]"},
	}
	fake := githubtest.New(t, &githubtest.Repo{Owner: "example", Name: "app", Compare: map[string][]githubtest.Commit{"v1.0.0...main": commits}})
	fake.PerPage = 2

	cmp, err := client(fake).Compare(t.Context(), "example", "app", "v1.0.0", "main")
	if err != nil {
		t.Fatal(err)
	}
	if cmp.HTMLURL != "https://github.com/example/app/compare/v1.0.0...main" {
		t.Errorf("HTMLURL = %q", cmp.HTMLURL)
	}
	if len(cmp.Commits) != 5 {
		t.Fatalf("got %d commits, want 5", len(cmp.Commits))
	}
	want := []github.Commit{
		{SHA: "a", Login: "renovate[bot]", AuthorName: "renovate[bot]", Parents: 1},
		{SHA: "b", Login: "someone", AuthorName: "Some One", Parents: 1},
		{SHA: "c", AuthorName: "Unlinked", Parents: 1},
		{SHA: "d", Login: "someone", Parents: 2},
		{SHA: "e", Login: "renovate[bot]", Parents: 1},
	}
	for i := range want {
		if cmp.Commits[i] != want[i] {
			t.Errorf("commit %d = %+v, want %+v", i, cmp.Commits[i], want[i])
		}
	}
}

func TestErrorStatusIsReported(t *testing.T) {
	fake := githubtest.New(t)
	fake.Fail("/installation/repositories", http.StatusForbidden)
	_, err := client(fake).ListInstallationRepositories(t.Context())
	if err == nil {
		t.Fatal("no error")
	}
}
