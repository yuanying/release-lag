package lag_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/yuanying/release-lag/internal/github/githubtest"
	"github.com/yuanying/release-lag/internal/lag"
)

func refresher(fake *githubtest.Server, now *time.Time) *lag.Refresher {
	r := lag.NewRefresher(collector(fake))
	r.Now = func() time.Time { return *now }
	return r
}

func TestStateBeforeTheFirstRefresh(t *testing.T) {
	now := may1
	s := refresher(githubtest.New(t), &now).State()
	if !s.RefreshedAt.IsZero() || !s.LastAttemptAt.IsZero() || s.LastSuccess || s.Repos != nil {
		t.Errorf("state = %+v, want empty", s)
	}
}

func TestRefreshKeepsTheResult(t *testing.T) {
	now := may1
	fake := githubtest.New(t, repo("one", "renovate.json"))
	r := refresher(fake, &now)
	s := r.Refresh(t.Context())
	if !s.LastSuccess || s.LastError != "" || !s.RefreshedAt.Equal(may1) || !s.LastAttemptAt.Equal(may1) || len(s.Repos) != 1 {
		t.Fatalf("state = %+v", s)
	}
	if got := r.State(); len(got.Repos) != 1 || !got.RefreshedAt.Equal(may1) {
		t.Errorf("State() = %+v, want what Refresh returned", got)
	}
}

func TestRefreshWithAFailingRepository(t *testing.T) {
	now := may1
	bad := repo("bad", "renovate.json")
	bad.Tags = []githubtest.Tag{{Name: "v1.0.0", CommitDate: may1}}
	fake := githubtest.New(t, repo("good", "renovate.json"), bad)
	fake.Fail("/repos/example-owner/bad/compare/v1.0.0...main", http.StatusBadGateway)
	s := refresher(fake, &now).Refresh(t.Context())
	if s.LastSuccess || s.LastError == "" {
		t.Errorf("success %v, error %q; want a failure", s.LastSuccess, s.LastError)
	}
	if len(s.Repos) != 2 || !s.RefreshedAt.Equal(may1) {
		t.Errorf("state = %+v, want both repositories as of now", s)
	}
}

func TestRefreshThatCannotListKeepsThePreviousResult(t *testing.T) {
	now := may1
	fake := githubtest.New(t, repo("one", "renovate.json"))
	r := refresher(fake, &now)
	r.Refresh(t.Context())

	now = june1
	fake.Fail("/installation/repositories", http.StatusServiceUnavailable)
	s := r.Refresh(t.Context())
	if s.LastSuccess || s.LastError == "" {
		t.Errorf("success %v, error %q; want a failure", s.LastSuccess, s.LastError)
	}
	if len(s.Repos) != 1 || !s.RefreshedAt.Equal(may1) || !s.LastAttemptAt.Equal(june1) {
		t.Errorf("state = %+v, want May's repositories, attempted in June", s)
	}
}

func TestRunRefreshesAtOnceAndOnEveryTick(t *testing.T) {
	fake := githubtest.New(t, repo("one", "renovate.json"))
	r := lag.NewRefresher(collector(fake))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		r.Run(ctx, 20*time.Millisecond)
		close(done)
	}()
	deadline := time.After(5 * time.Second)
	for fake.Requests("/installation/repositories") < 3 {
		select {
		case <-deadline:
			t.Fatalf("%d refreshes in 5s", fake.Requests("/installation/repositories"))
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
}
