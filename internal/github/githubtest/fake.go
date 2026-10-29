// Package githubtest is a fake of the parts of GitHub's REST API release-lag
// reads, served by httptest. Tests describe the repositories as data and the
// fake answers the requests the real client makes, paginating as GitHub
// does.
package githubtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Repo is one repository the installation can see.
type Repo struct {
	Owner         string
	Name          string
	DefaultBranch string // "main" when empty
	Private       bool
	Fork          bool
	Archived      bool
	// Empty makes the repository have no commits: GitHub answers 409.
	Empty bool
	// Files are the paths on the default branch.
	Files []string
	Tags  []Tag
	// Compare maps "base...head" to the commits GitHub lists for it.
	Compare map[string][]Commit
}

// Tag is a tag in a repository.
type Tag struct {
	Name string
	// Annotated tags carry their own date; lightweight ones point straight
	// at a commit, whose date is CommitDate.
	Annotated  bool
	TagDate    time.Time
	CommitDate time.Time
}

// Commit is one commit of a comparison.
type Commit struct {
	SHA string
	// Login is the GitHub account of the author, empty when the author's
	// email belongs to no account.
	Login string
	// Name is the git author name.
	Name string
	// Merge commits have two parents.
	Merge bool
}

// Server is the fake GitHub.
type Server struct {
	*httptest.Server
	// PerPage caps the page size whatever the client asks for, to make
	// short lists paginate.
	PerPage int
	// Token is the bearer token every request must carry; empty accepts any.
	Token string

	mu    sync.Mutex
	repos []*Repo
	// Requests counts the requests by path.
	requests map[string]int
	// Fail makes a path answer with the status.
	fail map[string]int
}

// New starts a fake serving repos and stops it when the test ends.
func New(t testing.TB, repos ...*Repo) *Server {
	t.Helper()
	s := &Server{repos: repos, requests: map[string]int{}, fail: map[string]int{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// SetRepos replaces the repositories the installation can see.
func (s *Server) SetRepos(repos ...*Repo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.repos = repos
}

// Fail makes requests to the path answer with status.
func (s *Server) Fail(path string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail[path] = status
}

// Requests returns how many requests reached path.
func (s *Server) Requests(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[path]
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests[r.URL.Path]++
	if s.Token != "" && r.Header.Get("Authorization") != "Bearer "+s.Token {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "Bad credentials"})
		return
	}
	if status, ok := s.fail[r.URL.Path]; ok {
		writeJSON(w, status, map[string]string{"message": http.StatusText(status)})
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, nil)
		return
	}
	if r.URL.Path == "/installation/repositories" {
		s.listRepos(w, r)
		return
	}
	// /repos/{owner}/{repo}/...
	parts := strings.SplitN(strings.TrimPrefix(r.URL.EscapedPath(), "/repos/"), "/", 3)
	if !strings.HasPrefix(r.URL.Path, "/repos/") || len(parts) < 3 {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	repo := s.repo(parts[0], parts[1])
	if repo == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	rest := parts[2]
	switch {
	case strings.HasPrefix(rest, "git/trees/"):
		s.tree(w, repo, unescape(strings.TrimPrefix(rest, "git/trees/")))
	case rest == "git/matching-refs/tags":
		s.refs(w, repo)
	case strings.HasPrefix(rest, "git/tags/"):
		s.tagObject(w, repo, unescape(strings.TrimPrefix(rest, "git/tags/")))
	case strings.HasPrefix(rest, "git/commits/"):
		s.commit(w, repo, unescape(strings.TrimPrefix(rest, "git/commits/")))
	case strings.HasPrefix(rest, "compare/"):
		s.compare(w, r, repo, unescape(strings.TrimPrefix(rest, "compare/")))
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	}
}

func unescape(s string) string {
	u, err := url.PathUnescape(s)
	if err != nil {
		return s
	}
	return u
}

func (s *Server) repo(owner, name string) *Repo {
	for _, r := range s.repos {
		if strings.EqualFold(r.Owner, owner) && r.Name == name {
			return r
		}
	}
	return nil
}

func defaultBranch(r *Repo) string {
	if r.DefaultBranch == "" {
		return "main"
	}
	return r.DefaultBranch
}

// page slices items as GitHub would for the request and sets the Link header
// to the next page.
func page[T any](s *Server, w http.ResponseWriter, r *http.Request, items []T) []T {
	per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if per <= 0 {
		per = 30
	}
	if s.PerPage > 0 && s.PerPage < per {
		per = s.PerPage
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if n <= 0 {
		n = 1
	}
	start := min((n-1)*per, len(items))
	end := min(start+per, len(items))
	if end < len(items) {
		next := *r.URL
		q := next.Query()
		q.Set("page", strconv.Itoa(n+1))
		next.RawQuery = q.Encode()
		next.Scheme, next.Host = "http", r.Host
		w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next"`, next.String()))
	}
	return items[start:end]
}

func (s *Server) listRepos(w http.ResponseWriter, r *http.Request) {
	all := make([]map[string]any, 0, len(s.repos))
	for _, repo := range s.repos {
		all = append(all, map[string]any{
			"name":           repo.Name,
			"full_name":      repo.Owner + "/" + repo.Name,
			"owner":          map[string]any{"login": repo.Owner},
			"private":        repo.Private,
			"fork":           repo.Fork,
			"archived":       repo.Archived,
			"default_branch": defaultBranch(repo),
			"html_url":       "https://github.com/" + repo.Owner + "/" + repo.Name,
		})
	}
	items := page(s, w, r, all)
	writeJSON(w, http.StatusOK, map[string]any{"total_count": len(all), "repositories": items})
}

const treePrefix = "tree-"

func (s *Server) tree(w http.ResponseWriter, repo *Repo, ref string) {
	if repo.Empty {
		writeJSON(w, http.StatusConflict, map[string]string{"message": "Git Repository is empty."})
		return
	}
	var dir string
	switch {
	case ref == defaultBranch(repo):
	case strings.HasPrefix(ref, treePrefix):
		dir = strings.TrimPrefix(ref, treePrefix)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	seen := map[string]bool{}
	entries := []map[string]any{}
	for _, f := range repo.Files {
		rel := f
		if dir != "" {
			var ok bool
			if rel, ok = strings.CutPrefix(f, dir+"/"); !ok {
				continue
			}
		}
		name, _, isDir := strings.Cut(rel, "/")
		if seen[name] {
			continue
		}
		seen[name] = true
		entry := map[string]any{"path": name, "type": "blob", "sha": "blob-" + path.Join(dir, name)}
		if isDir {
			entry["type"] = "tree"
			entry["sha"] = treePrefix + path.Join(dir, name)
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i]["path"].(string) < entries[j]["path"].(string) })
	writeJSON(w, http.StatusOK, map[string]any{"sha": ref, "tree": entries, "truncated": false})
}

func (s *Server) refs(w http.ResponseWriter, repo *Repo) {
	if repo.Empty {
		writeJSON(w, http.StatusConflict, map[string]string{"message": "Git Repository is empty."})
		return
	}
	out := []map[string]any{}
	for _, t := range repo.Tags {
		obj := map[string]any{"type": "commit", "sha": "commit-" + t.Name}
		if t.Annotated {
			obj = map[string]any{"type": "tag", "sha": "tagobj-" + t.Name}
		}
		out = append(out, map[string]any{"ref": "refs/tags/" + t.Name, "object": obj})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) tagObject(w http.ResponseWriter, repo *Repo, sha string) {
	for _, t := range repo.Tags {
		if t.Annotated && sha == "tagobj-"+t.Name {
			writeJSON(w, http.StatusOK, map[string]any{
				"sha":    sha,
				"tag":    t.Name,
				"tagger": map[string]any{"name": "someone", "date": t.TagDate.UTC().Format(time.RFC3339)},
				"object": map[string]any{"type": "commit", "sha": "commit-" + t.Name},
			})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
}

func (s *Server) commit(w http.ResponseWriter, repo *Repo, sha string) {
	for _, t := range repo.Tags {
		if sha == "commit-"+t.Name {
			writeJSON(w, http.StatusOK, map[string]any{
				"sha":       sha,
				"author":    map[string]any{"name": "someone", "date": t.CommitDate.UTC().Format(time.RFC3339)},
				"committer": map[string]any{"name": "someone", "date": t.CommitDate.UTC().Format(time.RFC3339)},
			})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
}

func (s *Server) compare(w http.ResponseWriter, r *http.Request, repo *Repo, basehead string) {
	commits, ok := repo.Compare[basehead]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	all := make([]map[string]any, 0, len(commits))
	for _, c := range commits {
		parents := []map[string]any{{"sha": c.SHA + "^1"}}
		if c.Merge {
			parents = append(parents, map[string]any{"sha": c.SHA + "^2"})
		}
		var author any
		if c.Login != "" {
			author = map[string]any{"login": c.Login}
		}
		all = append(all, map[string]any{
			"sha":     c.SHA,
			"author":  author,
			"commit":  map[string]any{"author": map[string]any{"name": c.Name}},
			"parents": parents,
		})
	}
	items := page(s, w, r, all)
	writeJSON(w, http.StatusOK, map[string]any{
		"html_url":      "https://github.com/" + repo.Owner + "/" + repo.Name + "/compare/" + basehead,
		"ahead_by":      len(all),
		"total_commits": len(all),
		"commits":       items,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
