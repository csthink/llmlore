// Package stars owns the local-only "my-stars" dataset: the user's own starred
// repositories, kept entirely on the machine and NEVER committed to the repo
// (CLAUDE.md privacy red line, AC-8). It lives under ~/.local/share/llmlore/
// and is physically separate from data/repos.json (the shared open dataset).
//
// Responsibilities mirror the discover pipeline but for personal data:
//   - fetch the starred list from the GitHub API (public stars need only a
//     username; private stars / higher rate limits need a token),
//   - merge into the local dataset preserving classification and appending a
//     star snapshot per sync (same append-only history contract as the catalog),
//   - reuse the classifier to label each star with type/topics/summary,
//   - surface stale/archived "unfollow candidates" — LISTED ONLY, this package
//     performs NO account write operations (never stars/unstars/follows),
//   - search the personal stars and cross them against the discover catalog.
//
// API keys/tokens are held in memory only and never written into this file.
package stars

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/csthink/llmlore/internal/classifier"
	"github.com/csthink/llmlore/internal/collector"
	"github.com/csthink/llmlore/internal/model"
)

// SchemaVersion is the on-disk schema version for my-stars.json. It is tracked
// independently from the catalog's model.CurrentSchemaVersion because the two
// datasets are deliberately separate shapes.
const SchemaVersion = 1

// StaleThreshold is the age past which a starred repo is considered stale. It
// matches the catalog's default (~12 months) so "stale" means the same thing in
// both views.
const StaleThreshold = 365 * 24 * time.Hour

// Environment variable names this package honours for path resolution.
const (
	envXDGDataHome = "XDG_DATA_HOME"
	envHome        = "HOME"
)

// Dataset is the top-level shape of my-stars.json.
type Dataset struct {
	Meta  Meta   `json:"meta"`
	Repos []Repo `json:"repos"`
}

// Meta carries dataset-level bookkeeping for the personal stars file.
type Meta struct {
	SchemaVersion int       `json:"schema_version"`
	GeneratedAt   time.Time `json:"generated_at"`
	User          string    `json:"user"` // GitHub login whose stars these are ("" = authenticated user)
	Count         int       `json:"count"`
}

// Repo is one starred repository record. It carries the raw GitHub facts plus
// the classification fields organize fills in. Unlike model.Repo it has no
// `source` field (the source is always "your stars") and adds starred_at and
// archived, which only matter for personal-star bookkeeping.
type Repo struct {
	ID            string               `json:"id"` // "owner/name"
	URL           string               `json:"url"`
	Owner         string               `json:"owner"`
	Name          string               `json:"name"`
	Description   string               `json:"description"`
	Language      string               `json:"language"`
	Stars         int                  `json:"stars"`
	StarredAt     time.Time            `json:"starred_at"`
	PushedAt      time.Time            `json:"pushed_at"`
	Archived      bool                 `json:"archived"`
	IsStale       bool                 `json:"is_stale"` // derived: now - pushed_at > StaleThreshold
	Summary       string               `json:"summary"`  // one-line, LLM-generated; empty on heuristic path
	Type          string               `json:"type"`     // controlled type vocabulary; empty until organize runs
	Topics        []string             `json:"topics"`   // controlled topic vocabulary
	ClassifiedBy  string               `json:"classified_by"`
	StarSnapshots []model.StarSnapshot `json:"star_snapshots"` // append-only, ascending by time
}

// DefaultDataPath returns ~/.local/share/llmlore/my-stars.json, honouring
// XDG_DATA_HOME. getenv is injected for testability. This path is intentionally
// outside any repository so personal data can never be committed (AC-8).
func DefaultDataPath(getenv func(string) string) string {
	base := getenv(envXDGDataHome)
	if base == "" {
		base = filepath.Join(getenv(envHome), ".local", "share")
	}
	return filepath.Join(base, "llmlore", "my-stars.json")
}

// Load reads the dataset at path. A missing file is the first-run case and
// yields an empty, current-schema dataset rather than an error.
func Load(path string) (*Dataset, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Dataset{Meta: Meta{SchemaVersion: SchemaVersion}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open my-stars %s: %w", path, err)
	}
	defer f.Close()

	var d Dataset
	dec := json.NewDecoder(f)
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("decode my-stars %s: %w", path, err)
	}
	if d.Meta.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("unsupported my-stars schema_version %d (this build supports %d)", d.Meta.SchemaVersion, SchemaVersion)
	}
	return &d, nil
}

// Save writes the dataset to path atomically (temp file + rename) with
// owner-only permissions, since it holds personal data. Parent directories are
// created as needed with restrictive permissions too.
func Save(path string, d *Dataset) error {
	d.Meta.SchemaVersion = SchemaVersion
	d.Meta.Count = len(d.Repos)

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create my-stars dir: %w", err)
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(d); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("encode my-stars: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// --- GitHub starred-list client ---------------------------------------------

// defaultBaseURL is the GitHub API root. Overridable for tests.
const defaultBaseURL = "https://api.github.com"

// perPageMax is the GitHub API's per-page ceiling for the starred endpoint.
const perPageMax = 100

// maxPages bounds pagination so a runaway loop cannot page forever; 100 pages of
// 100 covers 10k stars, far beyond any realistic personal list.
const maxPages = 100

// Client fetches a user's starred repositories from the GitHub API.
type Client struct {
	httpClient *http.Client
	baseURL    string
	token      string // optional; raises rate limits and enables private stars, never logged
}

// NewClient builds a Client. An empty token uses anonymous (low rate-limit)
// access, which can only read public stars. The HTTP client carries a timeout.
func NewClient(token string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    defaultBaseURL,
		token:      token,
	}
}

// ghStarredItem mirrors one element of the starred listing when requested with
// the star+json media type, which wraps the repo and adds starred_at.
type ghStarredItem struct {
	StarredAt time.Time `json:"starred_at"`
	Repo      ghRepo    `json:"repo"`
}

// ghRepo mirrors the subset of a GitHub repository object we consume.
type ghRepo struct {
	FullName    string    `json:"full_name"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Language    string    `json:"language"`
	Stars       int       `json:"stargazers_count"`
	HTMLURL     string    `json:"html_url"`
	PushedAt    time.Time `json:"pushed_at"`
	Archived    bool      `json:"archived"`
	Owner       struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// FetchStarred returns every repository the given user has starred. When user
// is empty it lists the authenticated user's stars (which requires a token and
// can include private stars). Network and non-2xx failures surface as
// *collector.UpstreamError (exit 3); a 401/403 surfaces as *AuthError (exit 4).
func (c *Client) FetchStarred(ctx context.Context, user string) ([]Repo, error) {
	endpoint := c.baseURL + "/user/starred"
	if user != "" {
		endpoint = c.baseURL + "/users/" + url.PathEscape(user) + "/starred"
	}

	var out []Repo
	for page := 1; page <= maxPages; page++ {
		items, err := c.fetchPage(ctx, endpoint, page)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			out = append(out, toRepo(it))
		}
		if len(items) < perPageMax {
			break // short page: the list is exhausted
		}
	}
	return out, nil
}

// fetchPage requests a single page of starred items.
func (c *Client) fetchPage(ctx context.Context, endpoint string, page int) ([]ghStarredItem, error) {
	const op = "github starred"

	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, &collector.UpstreamError{Op: op, Err: fmt.Errorf("build request url: %w", err)}
	}
	q := u.Query()
	q.Set("per_page", strconv.Itoa(perPageMax))
	q.Set("page", strconv.Itoa(page))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, &collector.UpstreamError{Op: op, Err: fmt.Errorf("build request: %w", err)}
	}
	// star+json yields starred_at alongside each repo.
	req.Header.Set("Accept", "application/vnd.github.star+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "llmlore")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &collector.UpstreamError{Op: op, Err: err}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, &collector.UpstreamError{Op: op, Err: fmt.Errorf("read response: %w", err)}
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, &AuthError{Err: fmt.Errorf("GitHub rejected the request (status %d: %s); set %s for private stars or a higher rate limit", resp.StatusCode, ghErrorMessage(body), "LLMLORE_GITHUB_TOKEN")}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &collector.UpstreamError{Op: op, Err: fmt.Errorf("unexpected status %d: %s", resp.StatusCode, ghErrorMessage(body))}
	}

	var items []ghStarredItem
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, &collector.UpstreamError{Op: op, Err: fmt.Errorf("decode response: %w", err)}
	}
	return items, nil
}

// toRepo converts a fetched starred item into a Repo carrying only raw facts;
// classification fields stay empty until organize runs.
func toRepo(it ghStarredItem) Repo {
	r := it.Repo
	owner := r.Owner.Login
	name := r.Name
	if (owner == "" || name == "") && r.FullName != "" {
		if o, n, ok := strings.Cut(r.FullName, "/"); ok {
			if owner == "" {
				owner = o
			}
			if name == "" {
				name = n
			}
		}
	}
	u := r.HTMLURL
	if u == "" {
		u = "https://github.com/" + model.NormalizeID(owner, name)
	}
	return Repo{
		ID:          model.NormalizeID(owner, name),
		URL:         u,
		Owner:       owner,
		Name:        name,
		Description: r.Description,
		Language:    r.Language,
		Stars:       r.Stars,
		StarredAt:   it.StarredAt,
		PushedAt:    r.PushedAt,
		Archived:    r.Archived,
	}
}

// ghErrorMessage extracts GitHub's "message" field, falling back to a truncated
// raw body so the user sees something actionable.
func ghErrorMessage(body []byte) string {
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &e) == nil && e.Message != "" {
		return e.Message
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// AuthError signals that GitHub rejected the credentials (401/403): a token is
// missing or invalid for the requested stars. The CLI maps it to exit code 4.
type AuthError struct{ Err error }

func (e *AuthError) Error() string { return e.Err.Error() }
func (e *AuthError) Unwrap() error { return e.Err }

// IsAuth reports whether err is (or wraps) an *AuthError (exit 4).
func IsAuth(err error) bool {
	var ae *AuthError
	return errors.As(err, &ae)
}

// --- Merge / organize / query ------------------------------------------------

// Sync folds a freshly-fetched starred list into the existing dataset. The
// result reflects the CURRENT starred set: repos still starred are kept and
// refreshed (volatile facts updated, a snapshot appended, classification and
// history preserved), newly-starred repos are added, and repos the user has
// since unstarred are dropped. now stamps the appended snapshot and recomputes
// staleness. The user field records whose stars these are.
//
// An out-of-order observation (now earlier than a repo's latest snapshot) is
// rejected wholesale for that repo, mirroring enricher.Refresh: no snapshot is
// appended and no volatile field is overwritten, so a replayed sync cannot
// regress stars or corrupt the append-only history.
func Sync(existing *Dataset, fetched []Repo, user string, now time.Time) *Dataset {
	index := make(map[string]Repo, len(existing.Repos))
	for _, r := range existing.Repos {
		index[r.ID] = r
	}

	out := make([]Repo, 0, len(fetched))
	for _, f := range fetched {
		prev, ok := index[f.ID]
		if !ok {
			f.IsStale = deriveStale(f.PushedAt, now)
			f.StarSnapshots = []model.StarSnapshot{{T: now, Stars: f.Stars}}
			out = append(out, f)
			continue
		}
		out = append(out, refresh(prev, f, now))
	}
	return &Dataset{
		Meta:  Meta{SchemaVersion: SchemaVersion, GeneratedAt: now, User: user, Count: len(out)},
		Repos: out,
	}
}

// refresh updates a still-starred repo from a fresh observation, preserving its
// classification (type/topics/summary/classified_by), its starred_at, and its
// snapshot history while appending one new snapshot. See Sync for the
// out-of-order contract.
func refresh(prev, fresh Repo, now time.Time) Repo {
	if n := len(prev.StarSnapshots); n > 0 && now.Before(prev.StarSnapshots[n-1].T) {
		return prev // out-of-order: accept nothing, change nothing
	}
	out := prev // struct copy; classification + starred_at retained
	out.Stars = fresh.Stars
	out.Archived = fresh.Archived
	if !fresh.PushedAt.IsZero() {
		out.PushedAt = fresh.PushedAt
	}
	if fresh.Description != "" {
		out.Description = fresh.Description
	}
	if fresh.Language != "" {
		out.Language = fresh.Language
	}
	out.IsStale = deriveStale(out.PushedAt, now)
	history := make([]model.StarSnapshot, len(prev.StarSnapshots), len(prev.StarSnapshots)+1)
	copy(history, prev.StarSnapshots)
	out.StarSnapshots = append(history, model.StarSnapshot{T: now, Stars: fresh.Stars})
	return out
}

// deriveStale reports whether a repo is stale. An unknown (zero) pushed_at is
// treated as not stale, matching the catalog enricher's rule.
func deriveStale(pushedAt, now time.Time) bool {
	if pushedAt.IsZero() {
		return false
	}
	return now.Sub(pushedAt) > StaleThreshold
}

// Organize labels every starred repo with a type, topics, and (on the LLM path)
// a one-line summary by reusing the catalog classifier. Unlike the discover
// pipeline it NEVER drops a repo: the user already chose to star these, so a
// Decision's Keep flag is ignored and only its labels are applied. The input is
// not mutated; a new dataset is returned.
func Organize(ctx context.Context, ds *Dataset, c classifier.Classifier) (*Dataset, error) {
	out := make([]Repo, len(ds.Repos))
	copy(out, ds.Repos)
	for i := range out {
		dec, err := c.Classify(ctx, asCandidate(out[i]))
		if err != nil {
			return nil, err
		}
		out[i].Type = dec.Type
		out[i].Topics = dec.Topics
		out[i].Summary = dec.Summary
		out[i].ClassifiedBy = dec.ClassifiedBy
	}
	meta := ds.Meta
	meta.Count = len(out)
	return &Dataset{Meta: meta, Repos: out}, nil
}

// asCandidate adapts a starred Repo to the collector.Candidate the classifier
// consumes. Source is set to search purely to satisfy the shape; the classifier
// only reads name/description (heuristic) plus the descriptive fields (LLM).
func asCandidate(r Repo) collector.Candidate {
	return collector.Candidate{
		ID:          r.ID,
		Owner:       r.Owner,
		Name:        r.Name,
		URL:         r.URL,
		Description: r.Description,
		Language:    r.Language,
		Stars:       r.Stars,
		PushedAt:    r.PushedAt,
		Source:      model.SourceSearch,
	}
}

// Stale returns the "unfollow candidates": repos that are archived or stale
// (no push within StaleThreshold). It ONLY lists them — this package performs
// no account write operations, so unstarring is always left to the user
// (CLAUDE.md security red line). Results are sorted by id for stable output.
func Stale(ds *Dataset) []Repo {
	var out []Repo
	for _, r := range ds.Repos {
		if r.Archived || r.IsStale {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Search returns the starred repos matching a free-text query (case-insensitive
// substring) across topics, language, type, id, and summary — i.e. the tag /
// language / topic axes called for by the spec, plus the obviously useful name
// match. An empty query returns every repo, sorted by stars descending.
func Search(ds *Dataset, query string) []Repo {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []Repo
	for _, r := range ds.Repos {
		if q == "" || matches(r, q) {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Stars != out[j].Stars {
			return out[i].Stars > out[j].Stars
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// matches reports whether the query appears in any searchable field of r.
func matches(r Repo, q string) bool {
	if strings.Contains(strings.ToLower(r.ID), q) ||
		strings.Contains(strings.ToLower(r.Language), q) ||
		strings.Contains(strings.ToLower(r.Type), q) ||
		strings.Contains(strings.ToLower(r.Summary), q) {
		return true
	}
	for _, t := range r.Topics {
		if strings.Contains(strings.ToLower(t), q) {
			return true
		}
	}
	return false
}

// CrossResult holds the intersection of the discover catalog with the personal
// stars: repos already starred (which discover could exclude) and high-star
// catalog repos not yet starred (recommendations).
type CrossResult struct {
	AlreadyStarred []model.Repo // catalog repos the user has already starred
	Recommended    []model.Repo // catalog repos the user has not starred, by stars desc
}

// Cross intersects the discover catalog with the personal stars. limit caps the
// recommendation list (<= 0 means no cap). Recommendations are sorted by stars
// descending with id as a stable tiebreaker.
func Cross(catalog *model.Dataset, mine *Dataset, limit int) CrossResult {
	starred := make(map[string]bool, len(mine.Repos))
	for _, r := range mine.Repos {
		starred[r.ID] = true
	}

	var res CrossResult
	for _, r := range catalog.Repos {
		if starred[r.ID] {
			res.AlreadyStarred = append(res.AlreadyStarred, r)
		} else {
			res.Recommended = append(res.Recommended, r)
		}
	}
	sort.SliceStable(res.Recommended, func(i, j int) bool {
		if res.Recommended[i].Stars != res.Recommended[j].Stars {
			return res.Recommended[i].Stars > res.Recommended[j].Stars
		}
		return res.Recommended[i].ID < res.Recommended[j].ID
	})
	if limit > 0 && len(res.Recommended) > limit {
		res.Recommended = res.Recommended[:limit]
	}
	return res
}
