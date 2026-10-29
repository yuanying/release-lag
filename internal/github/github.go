// Package github reads what release-lag needs from GitHub's REST API: the
// repositories an installation can see, the files at the top of a branch,
// the tags and their dates, and the commits between two refs.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	// ErrNotFound is GitHub's 404.
	ErrNotFound = errors.New("not found")
	// ErrEmpty is GitHub's 409 for a repository with no commits.
	ErrEmpty = errors.New("the repository is empty")
)

// perPage is the largest page GitHub serves.
const perPage = 100

// Client calls GitHub's REST API. Authentication is the HTTP client's
// business (githubapp.Transport).
type Client struct {
	apiURL string
	http   *http.Client
}

// NewClient talks to the API at apiURL (https://api.github.com) through hc.
func NewClient(apiURL string, hc *http.Client) *Client {
	return &Client{apiURL: strings.TrimRight(apiURL, "/"), http: hc}
}

// Repository is a repository an installation can see.
type Repository struct {
	Owner         string
	Name          string
	DefaultBranch string
	Private       bool
	Fork          bool
	Archived      bool
	HTMLURL       string
}

// TreeEntry is one entry of a git tree, not recursed into.
type TreeEntry struct {
	Path string
	// Type is "blob", "tree" or "commit" (a submodule).
	Type string
	SHA  string
}

// Tag is a tag ref.
type Tag struct {
	Name string
	// SHA is the tag object of an annotated tag, or the commit a lightweight
	// tag points at.
	SHA       string
	Annotated bool
}

// Comparison is what lies between two refs.
type Comparison struct {
	HTMLURL string
	// Commits are those reachable from head and not from base.
	Commits []Commit
}

// Commit is a commit of a comparison.
type Commit struct {
	SHA string
	// Login is the author's GitHub account, empty when the author's email
	// belongs to none.
	Login string
	// AuthorName is the git author name.
	AuthorName string
	Parents    int
}

// ListInstallationRepositories lists every repository the installation can
// see.
func (c *Client) ListInstallationRepositories(ctx context.Context) ([]Repository, error) {
	var out []Repository
	next := c.apiURL + "/installation/repositories?per_page=" + fmt.Sprint(perPage)
	for next != "" {
		var page struct {
			Repositories []struct {
				Name          string                 `json:"name"`
				Owner         struct{ Login string } `json:"owner"`
				DefaultBranch string                 `json:"default_branch"`
				Private       bool                   `json:"private"`
				Fork          bool                   `json:"fork"`
				Archived      bool                   `json:"archived"`
				HTMLURL       string                 `json:"html_url"`
			} `json:"repositories"`
		}
		var err error
		if next, err = c.get(ctx, next, &page); err != nil {
			return nil, fmt.Errorf("listing the installation's repositories: %w", err)
		}
		for _, r := range page.Repositories {
			out = append(out, Repository{
				Owner:         r.Owner.Login,
				Name:          r.Name,
				DefaultBranch: r.DefaultBranch,
				Private:       r.Private,
				Fork:          r.Fork,
				Archived:      r.Archived,
				HTMLURL:       r.HTMLURL,
			})
		}
	}
	return out, nil
}

// Tree lists the entries of the tree at treeish (a branch or a tree SHA),
// without recursing.
func (c *Client) Tree(ctx context.Context, owner, repo, treeish string) ([]TreeEntry, error) {
	var tree struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"tree"`
	}
	if _, err := c.get(ctx, c.repoURL(owner, repo, "git/trees", treeish), &tree); err != nil {
		return nil, fmt.Errorf("reading the tree %s of %s/%s: %w", treeish, owner, repo, err)
	}
	out := make([]TreeEntry, 0, len(tree.Tree))
	for _, e := range tree.Tree {
		out = append(out, TreeEntry{Path: e.Path, Type: e.Type, SHA: e.SHA})
	}
	return out, nil
}

// ListTags lists the tags of a repository. An empty repository has none.
func (c *Client) ListTags(ctx context.Context, owner, repo string) ([]Tag, error) {
	var out []Tag
	next := c.repoURL(owner, repo, "git/matching-refs/tags") + "?per_page=" + fmt.Sprint(perPage)
	for next != "" {
		var refs []struct {
			Ref    string `json:"ref"`
			Object struct {
				Type string `json:"type"`
				SHA  string `json:"sha"`
			} `json:"object"`
		}
		var err error
		next, err = c.get(ctx, next, &refs)
		if errors.Is(err, ErrEmpty) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("listing the tags of %s/%s: %w", owner, repo, err)
		}
		for _, r := range refs {
			out = append(out, Tag{
				Name:      strings.TrimPrefix(r.Ref, "refs/tags/"),
				SHA:       r.Object.SHA,
				Annotated: r.Object.Type == "tag",
			})
		}
	}
	return out, nil
}

// TagDate is when a tag was made: the tagger's date for an annotated tag,
// the committer's date of the commit for a lightweight one.
func (c *Client) TagDate(ctx context.Context, owner, repo string, tag Tag) (time.Time, error) {
	if !tag.Annotated {
		return c.commitDate(ctx, owner, repo, tag.SHA)
	}
	var obj struct {
		Tagger struct {
			Date time.Time `json:"date"`
		} `json:"tagger"`
	}
	if _, err := c.get(ctx, c.repoURL(owner, repo, "git/tags", tag.SHA), &obj); err != nil {
		return time.Time{}, fmt.Errorf("reading the tag %s of %s/%s: %w", tag.Name, owner, repo, err)
	}
	return obj.Tagger.Date, nil
}

func (c *Client) commitDate(ctx context.Context, owner, repo, sha string) (time.Time, error) {
	var commit struct {
		Committer struct {
			Date time.Time `json:"date"`
		} `json:"committer"`
	}
	if _, err := c.get(ctx, c.repoURL(owner, repo, "git/commits", sha), &commit); err != nil {
		return time.Time{}, fmt.Errorf("reading the commit %s of %s/%s: %w", sha, owner, repo, err)
	}
	return commit.Committer.Date, nil
}

// Compare lists the commits reachable from head and not from base.
func (c *Client) Compare(ctx context.Context, owner, repo, base, head string) (*Comparison, error) {
	out := &Comparison{}
	basehead := url.PathEscape(base) + "..." + url.PathEscape(head)
	next := c.apiURL + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/compare/" + basehead + "?per_page=" + fmt.Sprint(perPage)
	for next != "" {
		var page struct {
			HTMLURL string `json:"html_url"`
			Commits []struct {
				SHA    string `json:"sha"`
				Author *struct {
					Login string `json:"login"`
				} `json:"author"`
				Commit struct {
					Author struct {
						Name string `json:"name"`
					} `json:"author"`
				} `json:"commit"`
				Parents []json.RawMessage `json:"parents"`
			} `json:"commits"`
		}
		var err error
		if next, err = c.get(ctx, next, &page); err != nil {
			return nil, fmt.Errorf("comparing %s...%s of %s/%s: %w", base, head, owner, repo, err)
		}
		if out.HTMLURL == "" {
			out.HTMLURL = page.HTMLURL
		}
		for _, cm := range page.Commits {
			commit := Commit{SHA: cm.SHA, AuthorName: cm.Commit.Author.Name, Parents: len(cm.Parents)}
			if cm.Author != nil {
				commit.Login = cm.Author.Login
			}
			out.Commits = append(out.Commits, commit)
		}
	}
	return out, nil
}

func (c *Client) repoURL(owner, repo string, parts ...string) string {
	u := c.apiURL + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
	for i, p := range parts {
		if i == 0 {
			// The fixed part of the path, such as git/trees.
			u += "/" + p
			continue
		}
		u += "/" + url.PathEscape(p)
	}
	return u
}

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// get decodes the JSON at u into v and returns the next page's URL, empty on
// the last page.
func (c *Client) get(ctx context.Context, u string, v any) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", ErrNotFound
	case http.StatusConflict:
		return "", ErrEmpty
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return "", fmt.Errorf("GET %s: %s: %s", req.URL.Path, resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return "", fmt.Errorf("GET %s: decoding: %w", req.URL.Path, err)
	}
	if m := nextLink.FindStringSubmatch(resp.Header.Get("Link")); m != nil {
		return m[1], nil
	}
	return "", nil
}
