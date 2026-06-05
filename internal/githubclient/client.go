package githubclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Tag represents a Git reference returned from GitHub's tag listing.
type Tag struct {
	Name      string
	CommitSHA string
}

// Client abstracts GitHub interactions needed by ghapm.
type Client interface {
	// ListTags returns all tags for the repository sorted by recency (highest semantic version first).
	ListTags(ctx context.Context, owner, repo string) ([]Tag, error)
	// CommitDate returns the commit timestamp in UTC. The second return indicates whether the
	// date is authoritative (false when metadata is missing).
	CommitDate(ctx context.Context, owner, repo, sha string) (time.Time, bool, error)
	// ResolveRef resolves a tag or branch reference to a commit SHA.
	ResolveRef(ctx context.Context, owner, repo, ref string) (string, error)
}

// NewCachingClient wraps an inner client with in-memory caching to avoid duplicate requests.
func NewCachingClient(inner Client) Client {
	return &cachingClient{
		inner:      inner,
		tagCache:   make(map[string][]Tag),
		commitDate: make(map[string]map[string]commitEntry),
		refCache:   make(map[string]map[string]string),
	}
}

// NewCLIClient returns a client implementation backed by the `gh` CLI.
func NewCLIClient() Client {
	return &cliClient{}
}

// NewRESTClient returns a client implementation using the GitHub REST API.
// The provided token is optional; when empty, unauthenticated requests are performed.
func NewRESTClient(token string) Client {
	return &restClient{
		client: &http.Client{Timeout: 15 * time.Second},
		token:  token,
	}
}

// --- caching -----------------------------------------------------------------

type commitEntry struct {
	when time.Time
	ok   bool
	set  bool
}

var logFunc = func(string, ...interface{}) {}

func isFullSHA(ref string) bool {
	if len(ref) != 40 {
		return false
	}
	for _, r := range ref {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// SetLogger assigns a function used for verbose logging of client operations.
func SetLogger(fn func(string, ...interface{})) {
	if fn == nil {
		logFunc = func(string, ...interface{}) {}
		return
	}
	logFunc = fn
}

func logf(format string, args ...interface{}) {
	logFunc(format, args...)
}

type cachingClient struct {
	inner      Client
	mu         sync.Mutex
	tagCache   map[string][]Tag
	commitDate map[string]map[string]commitEntry
	refCache   map[string]map[string]string
}

func (c *cachingClient) ListTags(ctx context.Context, owner, repo string) ([]Tag, error) {
	key := owner + "/" + repo
	c.mu.Lock()
	if tags, ok := c.tagCache[key]; ok {
		c.mu.Unlock()
		logf("cache hit: tags for %s/%s", owner, repo)
		return tags, nil
	}
	c.mu.Unlock()
	logf("fetching tags for %s/%s", owner, repo)

	tags, err := c.inner.ListTags(ctx, owner, repo)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.tagCache[key] = tags
	c.mu.Unlock()
	logf("cached %d tags for %s/%s", len(tags), owner, repo)
	return tags, nil
}

func (c *cachingClient) CommitDate(ctx context.Context, owner, repo, sha string) (time.Time, bool, error) {
	key := owner + "/" + repo
	sha = strings.ToLower(sha)

	c.mu.Lock()
	repoCache, ok := c.commitDate[key]
	if !ok {
		repoCache = make(map[string]commitEntry)
		c.commitDate[key] = repoCache
	}
	if entry, ok := repoCache[sha]; ok && entry.set {
		c.mu.Unlock()
		logf("cache hit: commit date for %s/%s@%s", owner, repo, sha)
		return entry.when, entry.ok, nil
	}
	c.mu.Unlock()
	logf("fetching commit date for %s/%s@%s", owner, repo, sha)

	when, ok, err := c.inner.CommitDate(ctx, owner, repo, sha)
	if err != nil {
		return time.Time{}, false, err
	}

	c.mu.Lock()
	repoCache[sha] = commitEntry{when: when, ok: ok, set: true}
	c.mu.Unlock()
	logf("cached commit date for %s/%s@%s (ok=%v)", owner, repo, sha, ok)

	return when, ok, nil
}

func (c *cachingClient) ResolveRef(ctx context.Context, owner, repo, ref string) (string, error) {
	if isFullSHA(ref) {
		return strings.ToLower(ref), nil
	}

	key := owner + "/" + repo
	c.mu.Lock()
	repoCache, ok := c.refCache[key]
	if !ok {
		repoCache = make(map[string]string)
		c.refCache[key] = repoCache
	}
	if sha, ok := repoCache[ref]; ok {
		c.mu.Unlock()
		logf("cache hit: ref %s/%s@%s", owner, repo, ref)
		return sha, nil
	}
	c.mu.Unlock()

	logf("resolving ref %s/%s@%s", owner, repo, ref)
	sha, err := c.inner.ResolveRef(ctx, owner, repo, ref)
	if err != nil {
		return "", err
	}
	lower := strings.ToLower(sha)

	c.mu.Lock()
	repoCache[ref] = lower
	c.mu.Unlock()
	logf("cached ref %s/%s@%s -> %s", owner, repo, ref, lower)

	return lower, nil
}

// --- CLI implementation -------------------------------------------------------

type cliClient struct{}

func (cli *cliClient) ListTags(ctx context.Context, owner, repo string) ([]Tag, error) {
	const perPage = 100
	var tags []Tag

	for page := 1; ; page++ {
		endpoint := fmt.Sprintf("repos/%s/%s/tags?per_page=%d&page=%d", owner, repo, perPage, page)
		logf("gh api: %s", endpoint)
		out, err := cli.run(ctx, endpoint)
		if err != nil {
			return nil, err
		}

		var resp []struct {
			Name   string `json:"name"`
			Commit struct {
				SHA string `json:"sha"`
			} `json:"commit"`
		}

		if err := json.Unmarshal(out, &resp); err != nil {
			return nil, fmt.Errorf("parse gh output for %s: %w", endpoint, err)
		}

		if len(resp) == 0 {
			break
		}

		for _, item := range resp {
			tags = append(tags, Tag{Name: item.Name, CommitSHA: item.Commit.SHA})
		}

		if len(resp) < perPage {
			break
		}
	}

	sortTags(tags)
	return tags, nil
}

func (cli *cliClient) CommitDate(ctx context.Context, owner, repo, sha string) (time.Time, bool, error) {
	endpoint := path.Join("repos", owner, repo, "commits", sha)
	logf("gh api: %s", endpoint)
	out, err := cli.run(ctx, endpoint)
	if err != nil {
		return time.Time{}, false, err
	}

	var resp struct {
		Commit struct {
			Author struct {
				Date string `json:"date"`
			} `json:"author"`
			Committer struct {
				Date string `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}

	if err := json.Unmarshal(out, &resp); err != nil {
		return time.Time{}, false, fmt.Errorf("parse gh commit for %s: %w", endpoint, err)
	}

	dateStr := resp.Commit.Author.Date
	if dateStr == "" {
		dateStr = resp.Commit.Committer.Date
	}
	if dateStr == "" {
		return time.Time{}, false, nil
	}

	when, err := time.Parse(time.RFC3339, dateStr)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse commit date %s: %w", dateStr, err)
	}
	return when.UTC(), true, nil
}

func (cli *cliClient) run(ctx context.Context, endpoint string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", "api", endpoint)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh api %s: %v: %s", endpoint, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func (cli *cliClient) ResolveRef(ctx context.Context, owner, repo, ref string) (string, error) {
	if isFullSHA(ref) {
		return strings.ToLower(ref), nil
	}

	attempts := []string{
		fmt.Sprintf("repos/%s/%s/git/ref/tags/%s", owner, repo, ref),
		fmt.Sprintf("repos/%s/%s/git/ref/heads/%s", owner, repo, ref),
		fmt.Sprintf("repos/%s/%s/git/ref/%s", owner, repo, ref),
	}

	for _, endpoint := range attempts {
		sha, found, err := cli.resolveRefEndpoint(ctx, endpoint)
		if err != nil {
			return "", err
		}
		if found {
			return strings.ToLower(sha), nil
		}
	}

	return "", fmt.Errorf("ref %q not found", ref)
}

func (cli *cliClient) resolveRefEndpoint(ctx context.Context, endpoint string) (string, bool, error) {
	logf("gh api: %s", endpoint)
	cmd := exec.CommandContext(ctx, "gh", "api", endpoint, "--jq", ".object.sha")
	output, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(output))
	if err != nil {
		if strings.Contains(result, "404") {
			return "", false, nil
		}
		return "", false, fmt.Errorf("gh api %s: %v: %s", endpoint, err, result)
	}
	if result == "" {
		return "", false, nil
	}
	return result, true, nil
}

// --- REST implementation ------------------------------------------------------

type restClient struct {
	client *http.Client
	token  string
}

func (r *restClient) ListTags(ctx context.Context, owner, repo string) ([]Tag, error) {
	const perPage = 100
	const maxPages = 5

	var tags []Tag
	for page := 1; page <= maxPages; page++ {
		endpoint := fmt.Sprintf("https://api.github.com/repos/%s/%s/tags?per_page=%d&page=%d", owner, repo, perPage, page)
		logf("GET %s", endpoint)
		var payload []struct {
			Name   string `json:"name"`
			Commit struct {
				SHA string `json:"sha"`
			} `json:"commit"`
		}

		if err := r.getJSON(ctx, endpoint, &payload); err != nil {
			return nil, err
		}

		if len(payload) == 0 {
			break
		}

		for _, item := range payload {
			tags = append(tags, Tag{Name: item.Name, CommitSHA: item.Commit.SHA})
		}

		if len(payload) < perPage {
			break
		}
	}

	sortTags(tags)
	return tags, nil
}

func (r *restClient) CommitDate(ctx context.Context, owner, repo, sha string) (time.Time, bool, error) {
	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/%s/commits/%s", owner, repo, sha)
	logf("GET %s", endpoint)
	var payload struct {
		Commit struct {
			Author struct {
				Date string `json:"date"`
			} `json:"author"`
			Committer struct {
				Date string `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}

	if err := r.getJSON(ctx, endpoint, &payload); err != nil {
		return time.Time{}, false, err
	}

	dateStr := payload.Commit.Author.Date
	if dateStr == "" {
		dateStr = payload.Commit.Committer.Date
	}

	if dateStr == "" {
		return time.Time{}, false, nil
	}

	when, err := time.Parse(time.RFC3339, dateStr)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse commit date %s: %w", dateStr, err)
	}
	return when.UTC(), true, nil
}

func (r *restClient) ResolveRef(ctx context.Context, owner, repo, ref string) (string, error) {
	if isFullSHA(ref) {
		return strings.ToLower(ref), nil
	}

	attempts := []string{
		fmt.Sprintf("https://api.github.com/repos/%s/%s/git/ref/tags/%s", owner, repo, ref),
		fmt.Sprintf("https://api.github.com/repos/%s/%s/git/ref/heads/%s", owner, repo, ref),
		fmt.Sprintf("https://api.github.com/repos/%s/%s/git/ref/%s", owner, repo, ref),
	}

	for _, url := range attempts {
		var payload struct {
			Object struct {
				SHA string `json:"sha"`
			} `json:"object"`
		}
		ok, err := r.tryGetJSON(ctx, url, &payload)
		if err != nil {
			return "", err
		}
		if ok && payload.Object.SHA != "" {
			return strings.ToLower(payload.Object.SHA), nil
		}
	}

	return "", fmt.Errorf("ref %q not found", ref)
}

func (r *restClient) getJSON(ctx context.Context, url string, target interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request for %s: %w", url, err)
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ghapm-upgrade")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("request %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("request %s: %s: %s", url, resp.Status, strings.TrimSpace(string(body)))
	}

	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode response from %s: %w", url, err)
	}

	return nil
}

func (r *restClient) tryGetJSON(ctx context.Context, url string, target interface{}) (bool, error) {
	logf("GET %s", url)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Errorf("build request for %s: %w", url, err)
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ghapm-init")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("request %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		io.Copy(io.Discard, resp.Body)
		return false, nil
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("request %s: %s: %s", url, resp.Status, strings.TrimSpace(string(body)))
	}

	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return false, fmt.Errorf("decode response from %s: %w", url, err)
	}

	return true, nil
}

// --- helpers -----------------------------------------------------------------

func sortTags(tags []Tag) {
	sort.Slice(tags, func(i, j int) bool {
		ai, aj := parseTagVersion(tags[i].Name), parseTagVersion(tags[j].Name)
		if ai.major != aj.major {
			return ai.major > aj.major
		}
		if ai.minor != aj.minor {
			return ai.minor > aj.minor
		}
		if ai.patch != aj.patch {
			return ai.patch > aj.patch
		}
		return tags[i].Name > tags[j].Name
	})
}

type tagVersion struct {
	major int
	minor int
	patch int
}

func parseTagVersion(name string) tagVersion {
	name = strings.TrimPrefix(name, "v")
	parts := strings.SplitN(name, ".", 3)
	if len(parts) != 3 {
		return tagVersion{}
	}
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	patch, _ := strconv.Atoi(parts[2])
	return tagVersion{major: major, minor: minor, patch: patch}
}

var _ Client = (*cliClient)(nil)
var _ Client = (*restClient)(nil)
var _ Client = (*cachingClient)(nil)
