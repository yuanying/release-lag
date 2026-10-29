package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yuanying/release-lag/internal/github"
	"github.com/yuanying/release-lag/internal/github/githubtest"
	"github.com/yuanying/release-lag/internal/lag"
	"github.com/yuanying/release-lag/internal/server"
)

const owner = "example-owner"

var (
	tagged = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	now    = time.Date(2026, 10, 29, 18, 0, 0, 0, time.UTC)
)

func fixture(t *testing.T) *githubtest.Server {
	t.Helper()
	released := &githubtest.Repo{Owner: owner, Name: "released-app", Files: []string{"renovate.json"},
		Tags: []githubtest.Tag{{Name: "v1.2.3", Annotated: true, TagDate: tagged}},
		Compare: map[string][]githubtest.Commit{"v1.2.3...main": {
			{SHA: "1", Login: "renovate[bot]"},
			{SHA: "2", Login: "a-person"},
			{SHA: "3", Login: "a-person", Merge: true},
		}},
	}
	unreleased := &githubtest.Repo{Owner: owner, Name: "new-app", Private: true, Files: []string{".github/renovate.json"}}
	return githubtest.New(t, released, unreleased)
}

func setup(t *testing.T, fake *githubtest.Server) (*httptest.Server, *lag.Refresher) {
	t.Helper()
	r := lag.NewRefresher(&lag.Collector{GitHub: github.NewClient(fake.URL, http.DefaultClient), Owner: owner})
	r.Now = func() time.Time { return now }
	s := &server.Server{Refresher: r, Owner: owner, Now: func() time.Time { return now }}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv, r
}

func do(t *testing.T, method, url string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(body)
}

type reposResponse struct {
	Owner         string     `json:"owner"`
	RefreshedAt   *time.Time `json:"refreshedAt"`
	LastAttemptAt *time.Time `json:"lastAttemptAt"`
	LastSuccess   bool       `json:"lastSuccess"`
	LastError     string     `json:"lastError"`
	Repositories  []struct {
		Name                           string     `json:"name"`
		URL                            string     `json:"url"`
		DefaultBranch                  string     `json:"defaultBranch"`
		Private                        bool       `json:"private"`
		Released                       bool       `json:"released"`
		LatestTag                      *string    `json:"latestTag"`
		ReleasedAt                     *time.Time `json:"releasedAt"`
		DaysSinceRelease               *int       `json:"daysSinceRelease"`
		CommitsSinceRelease            *int       `json:"commitsSinceRelease"`
		NonRenovateCommitsSinceRelease *int       `json:"nonRenovateCommitsSinceRelease"`
		CompareURL                     *string    `json:"compareUrl"`
		Error                          string     `json:"error"`
	} `json:"repositories"`
}

func decode(t *testing.T, body string) reposResponse {
	t.Helper()
	var out reposResponse
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return out
}

func TestHealthz(t *testing.T) {
	srv, _ := setup(t, fixture(t))
	resp, body := do(t, http.MethodGet, srv.URL+"/healthz")
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(body) != "ok" {
		t.Errorf("%d %q", resp.StatusCode, body)
	}
}

func TestHealthzDoesNotDependOnGitHub(t *testing.T) {
	fake := fixture(t)
	fake.Fail("/installation/repositories", http.StatusServiceUnavailable)
	srv, r := setup(t, fake)
	r.Refresh(t.Context())
	if resp, _ := do(t, http.MethodGet, srv.URL+"/healthz"); resp.StatusCode != http.StatusOK {
		t.Errorf("status %d", resp.StatusCode)
	}
}

func TestReposBeforeTheFirstRefresh(t *testing.T) {
	srv, _ := setup(t, fixture(t))
	resp, body := do(t, http.MethodGet, srv.URL+"/api/repos")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("%d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	got := decode(t, body)
	if got.RefreshedAt != nil || got.Repositories == nil || len(got.Repositories) != 0 || got.Owner != owner {
		t.Errorf("got %s", body)
	}
}

func TestRepos(t *testing.T) {
	srv, r := setup(t, fixture(t))
	r.Refresh(t.Context())
	_, body := do(t, http.MethodGet, srv.URL+"/api/repos")
	got := decode(t, body)
	if got.RefreshedAt == nil || !got.RefreshedAt.Equal(now) || !got.LastSuccess || got.LastError != "" {
		t.Errorf("header fields: %s", body)
	}
	if len(got.Repositories) != 2 {
		t.Fatalf("repositories: %s", body)
	}
	// Never released comes first.
	u, rel := got.Repositories[0], got.Repositories[1]
	if u.Name != "new-app" || u.Released || !u.Private || u.LatestTag != nil || u.ReleasedAt != nil ||
		u.DaysSinceRelease != nil || u.CommitsSinceRelease != nil || u.NonRenovateCommitsSinceRelease != nil || u.CompareURL != nil {
		t.Errorf("unreleased: %+v", u)
	}
	if rel.Name != "released-app" || !rel.Released || rel.Private || rel.DefaultBranch != "main" ||
		rel.URL != "https://github.com/example-owner/released-app" {
		t.Errorf("released: %+v", rel)
	}
	if rel.LatestTag == nil || *rel.LatestTag != "v1.2.3" || rel.ReleasedAt == nil || !rel.ReleasedAt.Equal(tagged) {
		t.Errorf("release: %+v", rel)
	}
	// 28 days and 6 hours.
	if rel.DaysSinceRelease == nil || *rel.DaysSinceRelease != 28 {
		t.Errorf("daysSinceRelease: %v", rel.DaysSinceRelease)
	}
	if rel.CommitsSinceRelease == nil || *rel.CommitsSinceRelease != 2 || rel.NonRenovateCommitsSinceRelease == nil || *rel.NonRenovateCommitsSinceRelease != 1 {
		t.Errorf("commits: %v, %v", rel.CommitsSinceRelease, rel.NonRenovateCommitsSinceRelease)
	}
	if rel.CompareURL == nil || *rel.CompareURL != "https://github.com/example-owner/released-app/compare/v1.2.3...main" {
		t.Errorf("compareUrl: %v", rel.CompareURL)
	}
}

func TestRefresh(t *testing.T) {
	fake := fixture(t)
	srv, _ := setup(t, fake)
	resp, body := do(t, http.MethodPost, srv.URL+"/api/refresh")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if fake.Requests("/installation/repositories") != 1 {
		t.Error("nothing was collected")
	}
	got := decode(t, body)
	if got.RefreshedAt == nil || len(got.Repositories) != 2 {
		t.Errorf("got %s", body)
	}
}

func TestRefreshThatFails(t *testing.T) {
	fake := fixture(t)
	fake.Fail("/installation/repositories", http.StatusServiceUnavailable)
	srv, _ := setup(t, fake)
	resp, body := do(t, http.MethodPost, srv.URL+"/api/refresh")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status %d", resp.StatusCode)
	}
	if got := decode(t, body); got.LastSuccess || got.LastError == "" || got.LastAttemptAt == nil {
		t.Errorf("got %s", body)
	}
}

func TestMethods(t *testing.T) {
	srv, _ := setup(t, fixture(t))
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/refresh"},
		{http.MethodPost, "/api/repos"},
		{http.MethodDelete, "/"},
	} {
		if resp, _ := do(t, tc.method, srv.URL+tc.path); resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: %d", tc.method, tc.path, resp.StatusCode)
		}
	}
	if resp, _ := do(t, http.MethodGet, srv.URL+"/no-such-page"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown path: %d", resp.StatusCode)
	}
}

func TestIndex(t *testing.T) {
	srv, _ := setup(t, fixture(t))
	resp, body := do(t, http.MethodGet, srv.URL+"/")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("%d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	// The page draws itself from the API, by a relative path so it works
	// under any prefix.
	for _, want := range []string{"<table", `"api/repos"`, `"api/refresh"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page has no %s", want)
		}
	}
}

func TestMetrics(t *testing.T) {
	srv, r := setup(t, fixture(t))
	r.Refresh(t.Context())
	resp, body := do(t, http.MethodGet, srv.URL+"/metrics")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	for _, want := range []string{
		`release_lag_days_since_release{repo="released-app"} 28.25`,
		`release_lag_commits_since_release{repo="released-app"} 2`,
		`release_lag_non_renovate_commits_since_release{repo="released-app"} 1`,
		`release_lag_unreleased{repo="new-app"} 1`,
		`release_lag_unreleased{repo="released-app"} 0`,
		`release_lag_last_refresh_success 1`,
		`release_lag_last_refresh_timestamp_seconds 1.7932968e+09`,
		`release_lag_repository_info{default_branch="main",latest_tag="v1.2.3",private="false",repo="released-app"} 1`,
		`release_lag_repository_info{default_branch="main",latest_tag="",private="true",repo="new-app"} 1`,
	} {
		if !strings.Contains(body, want+"\n") {
			t.Errorf("no %s", want)
		}
	}
	// Nothing measured since a release that does not exist.
	for _, absent := range []string{
		`release_lag_days_since_release{repo="new-app"}`,
		`release_lag_commits_since_release{repo="new-app"}`,
		`release_lag_non_renovate_commits_since_release{repo="new-app"}`,
	} {
		if strings.Contains(body, absent) {
			t.Errorf("unexpected %s", absent)
		}
	}
}

func TestMetricsBeforeTheFirstRefresh(t *testing.T) {
	srv, _ := setup(t, fixture(t))
	_, body := do(t, http.MethodGet, srv.URL+"/metrics")
	if !strings.Contains(body, "release_lag_last_refresh_success 0\n") || !strings.Contains(body, "release_lag_last_refresh_timestamp_seconds 0\n") {
		t.Errorf("metrics:\n%s", body)
	}
}
