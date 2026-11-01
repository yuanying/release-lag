// Package lag measures how far each tracked repository's default branch has
// moved on from its latest release (ADR 0002).
package lag

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yuanying/release-lag/internal/github"
)

// renovateLogin is the account Renovate commits as, and its git author name.
const renovateLogin = "renovate[bot]"

// renovateConfigs are the places Renovate reads its config from, on the
// default branch (ADR 0002).
var renovateConfigs = map[string][]string{
	"":        {"renovate.json", "renovate.json5", ".renovaterc", ".renovaterc.json", ".renovaterc.json5"},
	".github": {"renovate.json", "renovate.json5"},
	".gitlab": {"renovate.json", "renovate.json5"},
}

// defaultConcurrency is how many repositories are measured at once: enough
// to finish a few dozen in seconds, few enough to stay clear of GitHub's
// secondary rate limits.
const defaultConcurrency = 8

// GitHub is what the collector reads; *github.Client is one.
type GitHub interface {
	ListInstallationRepositories(ctx context.Context) ([]github.Repository, error)
	Tree(ctx context.Context, owner, repo, treeish string) ([]github.TreeEntry, error)
	ListTags(ctx context.Context, owner, repo string) ([]github.Tag, error)
	TagDate(ctx context.Context, owner, repo string, tag github.Tag) (time.Time, error)
	Compare(ctx context.Context, owner, repo, base, head string) (*github.Comparison, error)
	CommitFiles(ctx context.Context, owner, repo, sha string) ([]string, error)
}

// Repo is one tracked repository and how far it is from its latest release.
type Repo struct {
	// Name is the repository's name without the owner.
	Name          string
	URL           string
	DefaultBranch string
	Private       bool
	// LatestTag is the greatest release tag, empty when there is none.
	LatestTag string
	// ReleasedAt is when LatestTag was made.
	ReleasedAt time.Time
	// Commits counts the commits from LatestTag to the default branch,
	// merge commits and those changing nothing released aside.
	Commits int
	// NonRenovateCommits counts those of Commits not made by Renovate.
	NonRenovateCommits int
	// CompareURL shows those commits on GitHub.
	CompareURL string
	// Error is why the repository could not be measured completely.
	Error string
}

// Released tells whether the repository has a release tag.
func (r Repo) Released() bool { return r.LatestTag != "" }

// Collector finds the tracked repositories and measures them.
type Collector struct {
	GitHub GitHub
	// Owner is the account whose repositories are tracked.
	Owner string
	// Exclude names repositories never to track.
	Exclude []string
	// Ignore matches the paths that change nothing released; commits that
	// change only those are not counted (ADR 0006). Nil counts them all.
	Ignore *Ignore
	// Concurrency is how many repositories are measured at once; zero
	// means a default.
	Concurrency int

	// collecting runs one collection at a time, for they share released.
	collecting sync.Mutex
	// released remembers, by repository and SHA, whether a commit changes
	// anything released: a commit never changes, so it is read once. A
	// collection keeps only the commits it met (seen).
	mu       sync.Mutex
	released map[string]bool
	seen     map[string]bool
}

// Collect lists the repositories the installation sees, keeps those to
// track and measures each, longest without a release first. A repository
// that could not be measured is still returned, with its Error set, and
// makes the returned error non-nil. Collections run one at a time.
func (c *Collector) Collect(ctx context.Context) ([]Repo, error) {
	c.collecting.Lock()
	defer c.collecting.Unlock()
	all, err := c.GitHub.ListInstallationRepositories(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.seen = map[string]bool{}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.released, c.seen = c.seen, nil
	}()
	var candidates []github.Repository
	for _, r := range all {
		if !strings.EqualFold(r.Owner, c.Owner) || r.Fork || r.Archived || slices.Contains(c.Exclude, r.Name) {
			continue
		}
		candidates = append(candidates, r)
	}

	type result struct {
		repo    Repo
		tracked bool
		err     error
	}
	results := make([]result, len(candidates))
	sem := make(chan struct{}, cmp.Or(c.Concurrency, defaultConcurrency))
	var wg sync.WaitGroup
	for i, r := range candidates {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			repo, tracked, err := c.measure(ctx, r)
			results[i] = result{repo, tracked, err}
		})
	}
	wg.Wait()

	var repos []Repo
	var errs []error
	for _, res := range results {
		if res.err != nil {
			res.repo.Error = res.err.Error()
			errs = append(errs, fmt.Errorf("%s: %w", res.repo.Name, res.err))
		}
		if res.tracked || res.err != nil {
			repos = append(repos, res.repo)
		}
	}
	Sort(repos)
	return repos, errors.Join(errs...)
}

// measure tells whether r is to be tracked and, if it is, how far it is
// from its latest release.
func (c *Collector) measure(ctx context.Context, r github.Repository) (Repo, bool, error) {
	repo := Repo{Name: r.Name, URL: r.HTMLURL, DefaultBranch: r.DefaultBranch, Private: r.Private}
	tracked, err := c.hasRenovateConfig(ctx, r)
	if err != nil || !tracked {
		return repo, false, err
	}

	tags, err := c.GitHub.ListTags(ctx, r.Owner, r.Name)
	if err != nil {
		return repo, true, err
	}
	names := make([]string, len(tags))
	for i, t := range tags {
		names[i] = t.Name
	}
	latest := latestRelease(names)
	if latest == "" {
		return repo, true, nil
	}
	repo.LatestTag = latest
	tag := tags[slices.IndexFunc(tags, func(t github.Tag) bool { return t.Name == latest })]
	if repo.ReleasedAt, err = c.GitHub.TagDate(ctx, r.Owner, r.Name, tag); err != nil {
		return repo, true, err
	}

	comparison, err := c.GitHub.Compare(ctx, r.Owner, r.Name, latest, r.DefaultBranch)
	if err != nil {
		return repo, true, err
	}
	// The comparison as a whole may change nothing released, and then
	// none of its commits do. A list as long as GitHub lists may have been
	// cut short and tells nothing.
	if !c.Ignore.empty() && len(comparison.Files) < github.MaxComparisonFiles && !slices.ContainsFunc(comparison.Files, c.releases) {
		repo.CompareURL = comparison.HTMLURL
		return repo, true, nil
	}
	var commits, nonRenovate int
	for _, commit := range comparison.Commits {
		if commit.Parents > 1 {
			continue
		}
		released, err := c.changesRelease(ctx, r, commit.SHA)
		if err != nil {
			return repo, true, err
		}
		if !released {
			continue
		}
		commits++
		if commit.Login != renovateLogin && commit.AuthorName != renovateLogin {
			nonRenovate++
		}
	}
	repo.CompareURL = comparison.HTMLURL
	repo.Commits, repo.NonRenovateCommits = commits, nonRenovate
	return repo, true, nil
}

// releases tells whether a change to the path goes into a release.
func (c *Collector) releases(path string) bool {
	return !c.Ignore.Match(path)
}

// changesRelease tells whether the commit changes anything released,
// reading its files the first time it is asked about.
func (c *Collector) changesRelease(ctx context.Context, r github.Repository, sha string) (bool, error) {
	if c.Ignore.empty() {
		return true, nil
	}
	key := r.Owner + "/" + r.Name + "@" + sha
	c.mu.Lock()
	released, known := c.released[key]
	if !known {
		released, known = c.seen[key]
	}
	if known {
		c.seen[key] = released
	}
	c.mu.Unlock()
	if known {
		return released, nil
	}

	files, err := c.GitHub.CommitFiles(ctx, r.Owner, r.Name, sha)
	if err != nil {
		return false, err
	}
	released = slices.ContainsFunc(files, c.releases)
	c.mu.Lock()
	c.seen[key] = released
	c.mu.Unlock()
	return released, nil
}

// hasRenovateConfig looks for a Renovate config file at the top of the
// default branch and in .github/ and .gitlab/. An empty repository has none.
func (c *Collector) hasRenovateConfig(ctx context.Context, r github.Repository) (bool, error) {
	root, err := c.GitHub.Tree(ctx, r.Owner, r.Name, r.DefaultBranch)
	if errors.Is(err, github.ErrEmpty) || errors.Is(err, github.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if hasBlob(root, renovateConfigs[""]) {
		return true, nil
	}
	for _, dir := range []string{".github", ".gitlab"} {
		i := slices.IndexFunc(root, func(e github.TreeEntry) bool { return e.Path == dir && e.Type == "tree" })
		if i < 0 {
			continue
		}
		sub, err := c.GitHub.Tree(ctx, r.Owner, r.Name, root[i].SHA)
		if err != nil {
			return false, err
		}
		if hasBlob(sub, renovateConfigs[dir]) {
			return true, nil
		}
	}
	return false, nil
}

func hasBlob(entries []github.TreeEntry, names []string) bool {
	return slices.ContainsFunc(entries, func(e github.TreeEntry) bool {
		return e.Type == "blob" && slices.Contains(names, e.Path)
	})
}

// Sort orders repositories longest without a release first: those never
// released, then the oldest release first, by name within each.
func Sort(repos []Repo) {
	slices.SortStableFunc(repos, func(a, b Repo) int {
		if a.Released() != b.Released() {
			if !a.Released() {
				return -1
			}
			return 1
		}
		return cmp.Or(a.ReleasedAt.Compare(b.ReleasedAt), strings.Compare(a.Name, b.Name))
	})
}
