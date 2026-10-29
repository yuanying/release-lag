package server

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/yuanying/release-lag/internal/lag"
)

// The metrics are a contract with the ServiceMonitor and whatever reads it
// in fleet-infra (ADR 0004). Change them there and in the ADR together.
var (
	daysSinceRelease = prometheus.NewDesc("release_lag_days_since_release",
		"Days since the latest release tag was made. Absent for a repository without one.",
		[]string{"repo"}, nil)
	commitsSinceRelease = prometheus.NewDesc("release_lag_commits_since_release",
		"Commits from the latest release tag to the default branch, merge commits aside.",
		[]string{"repo"}, nil)
	nonRenovateCommitsSinceRelease = prometheus.NewDesc("release_lag_non_renovate_commits_since_release",
		"Commits since the latest release tag not made by renovate[bot], merge commits aside.",
		[]string{"repo"}, nil)
	unreleased = prometheus.NewDesc("release_lag_unreleased",
		"1 when the repository has no release tag, 0 when it has one.",
		[]string{"repo"}, nil)
	repositoryInfo = prometheus.NewDesc("release_lag_repository_info",
		"1 for every tracked repository, with what is known about it as labels.",
		[]string{"repo", "default_branch", "latest_tag", "private"}, nil)
	lastRefreshTimestamp = prometheus.NewDesc("release_lag_last_refresh_timestamp_seconds",
		"When the last refresh finished, successful or not. 0 before the first.",
		nil, nil)
	lastRefreshSuccess = prometheus.NewDesc("release_lag_last_refresh_success",
		"1 when the last refresh measured every repository, 0 otherwise.",
		nil, nil)
)

// metrics reads the refresher's state at every scrape, so the days since a
// release keep growing between refreshes.
type metrics struct {
	refresher *lag.Refresher
	now       func() time.Time
}

func (m *metrics) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{
		daysSinceRelease, commitsSinceRelease, nonRenovateCommitsSinceRelease,
		unreleased, repositoryInfo, lastRefreshTimestamp, lastRefreshSuccess,
	} {
		ch <- d
	}
}

func (m *metrics) Collect(ch chan<- prometheus.Metric) {
	state := m.refresher.State()
	now := m.now()
	for _, r := range state.Repos {
		ch <- prometheus.MustNewConstMetric(repositoryInfo, prometheus.GaugeValue, 1,
			r.Name, r.DefaultBranch, r.LatestTag, strconv.FormatBool(r.Private))
		// A repository that failed before its tags were read is not known
		// to be unreleased.
		if r.Error == "" || r.Released() {
			ch <- prometheus.MustNewConstMetric(unreleased, prometheus.GaugeValue, boolValue(!r.Released()), r.Name)
		}
		if !r.ReleasedAt.IsZero() {
			ch <- prometheus.MustNewConstMetric(daysSinceRelease, prometheus.GaugeValue,
				now.Sub(r.ReleasedAt).Hours()/24, r.Name)
		}
		if r.CompareURL != "" {
			ch <- prometheus.MustNewConstMetric(commitsSinceRelease, prometheus.GaugeValue, float64(r.Commits), r.Name)
			ch <- prometheus.MustNewConstMetric(nonRenovateCommitsSinceRelease, prometheus.GaugeValue, float64(r.NonRenovateCommits), r.Name)
		}
	}
	var last float64
	if !state.LastAttemptAt.IsZero() {
		last = float64(state.LastAttemptAt.Unix())
	}
	ch <- prometheus.MustNewConstMetric(lastRefreshTimestamp, prometheus.GaugeValue, last)
	ch <- prometheus.MustNewConstMetric(lastRefreshSuccess, prometheus.GaugeValue, boolValue(state.LastSuccess))
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
