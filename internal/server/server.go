// Package server serves the collection: the page at /, the JSON API under
// /api/, the Prometheus metrics at /metrics and the liveness probe at
// /healthz, all on one port (ADR 0004).
//
// There is no authentication here: the Ingress in front of it is gated's,
// which lets only the owner through (ADR 0001).
package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/yuanying/release-lag/internal/lag"
)

//go:embed static/index.html
var indexHTML []byte

// refreshTimeout bounds a refresh asked for over HTTP. The refresh is not
// tied to the request: a client that gives up does not leave half a
// collection behind.
const refreshTimeout = 5 * time.Minute

// Server serves what Refresher holds.
type Server struct {
	Refresher *lag.Refresher
	// Owner is the account the repositories belong to.
	Owner string
	// Now is the clock the days since a release are counted to; time.Now
	// when nil.
	Now func() time.Time
}

// Handler routes every path release-lag serves.
func (s *Server) Handler() http.Handler {
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		&metrics{refresher: s.Refresher, now: s.now},
	)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /api/repos", s.repos)
	mux.HandleFunc("POST /api/refresh", s.refresh)
	mux.HandleFunc("GET /healthz", healthz)
	mux.Handle("GET /metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	return mux
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Server) index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML)
}

// healthz answers as long as the process serves, whatever GitHub does: a
// GitHub outage is no reason to restart the Pod.
func healthz(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) repos(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Response(s.Refresher.State()))
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), refreshTimeout)
	defer cancel()
	state := s.Refresher.Refresh(ctx)
	status := http.StatusOK
	if !state.LastSuccess {
		status = http.StatusBadGateway
	}
	writeJSON(w, status, s.Response(state))
}

// ReposResponse is the body of GET /api/repos and POST /api/refresh
// (ADR 0004).
type ReposResponse struct {
	Owner         string         `json:"owner"`
	RefreshedAt   *time.Time     `json:"refreshedAt"`
	LastAttemptAt *time.Time     `json:"lastAttemptAt"`
	LastSuccess   bool           `json:"lastSuccess"`
	LastError     string         `json:"lastError"`
	Repositories  []RepoResponse `json:"repositories"`
}

// RepoResponse is one repository. The fields about the release are null
// when there is none.
type RepoResponse struct {
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
	Error                          string     `json:"error,omitempty"`
}

// Response is the body GET /api/repos answers with for state.
func (s *Server) Response(state lag.State) ReposResponse {
	now := s.now()
	out := ReposResponse{
		Owner:         s.Owner,
		RefreshedAt:   timePtr(state.RefreshedAt),
		LastAttemptAt: timePtr(state.LastAttemptAt),
		LastSuccess:   state.LastSuccess,
		LastError:     state.LastError,
		Repositories:  make([]RepoResponse, 0, len(state.Repos)),
	}
	for _, r := range state.Repos {
		repo := RepoResponse{
			Name:          r.Name,
			URL:           r.URL,
			DefaultBranch: r.DefaultBranch,
			Private:       r.Private,
			Released:      r.Released(),
			Error:         r.Error,
		}
		if r.Released() {
			repo.LatestTag = &r.LatestTag
		}
		if !r.ReleasedAt.IsZero() {
			repo.ReleasedAt = timePtr(r.ReleasedAt)
			days := int(now.Sub(r.ReleasedAt) / (24 * time.Hour))
			repo.DaysSinceRelease = &days
		}
		if r.CompareURL != "" {
			repo.CompareURL = &r.CompareURL
			repo.CommitsSinceRelease = &r.Commits
			repo.NonRenovateCommitsSinceRelease = &r.NonRenovateCommits
		}
		out.Repositories = append(out.Repositories, repo)
	}
	return out
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writing a response", "error", err)
	}
}
