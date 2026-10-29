package lag

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// State is what the last refreshes left behind.
type State struct {
	// Repos are the tracked repositories as of RefreshedAt.
	Repos []Repo
	// RefreshedAt is when Repos were collected; zero before the first
	// collection that could list the repositories.
	RefreshedAt time.Time
	// LastAttemptAt is when the last refresh finished, successful or not.
	LastAttemptAt time.Time
	// LastSuccess tells whether every repository could be measured in the
	// last refresh.
	LastSuccess bool
	// LastError is why the last refresh failed.
	LastError string
}

// Refresher keeps the latest collection in memory and refreshes it on a
// timer or on request (ADR 0001).
type Refresher struct {
	collector *Collector
	// Now is the clock; time.Now when nil.
	Now func() time.Time
	// Log receives a line per refresh; slog.Default() when nil.
	Log *slog.Logger

	// refreshing lets one refresh run at a time: one asked for while
	// another runs waits for it and then collects anew.
	refreshing sync.Mutex
	mu         sync.RWMutex
	state      State
}

// NewRefresher refreshes with c.
func NewRefresher(c *Collector) *Refresher {
	return &Refresher{collector: c}
}

// State returns the current state without waiting for a refresh.
func (r *Refresher) State() State {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state
}

// Refresh collects the repositories now and returns the new state. When the
// repositories cannot even be listed, the previous ones are kept.
func (r *Refresher) Refresh(ctx context.Context) State {
	r.refreshing.Lock()
	defer r.refreshing.Unlock()

	start := r.now()
	repos, err := r.collector.Collect(ctx)
	now := r.now()

	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.LastAttemptAt = now
	r.state.LastSuccess = err == nil
	r.state.LastError = ""
	if err != nil {
		r.state.LastError = err.Error()
	}
	// A refresh that listed nothing has no repositories to replace the
	// previous ones with; one that failed on some still has news.
	if err == nil || repos != nil {
		r.state.Repos = repos
		r.state.RefreshedAt = now
	}
	log := r.Log
	if log == nil {
		log = slog.Default()
	}
	if err != nil {
		log.Error("refresh failed", "repositories", len(repos), "duration", now.Sub(start).String(), "error", err)
	} else {
		log.Info("refreshed", "repositories", len(repos), "duration", now.Sub(start).String())
	}
	return r.state
}

// Run refreshes at once and then every interval until ctx is done.
func (r *Refresher) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		r.Refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Refresher) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}
