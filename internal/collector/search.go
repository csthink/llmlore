package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/csthink/llmlore/internal/model"
)

// defaultSearchBaseURL is the GitHub Search API root. Overridable for tests.
const defaultSearchBaseURL = "https://api.github.com"

// searchPerPageMax is the GitHub Search API's per-page ceiling.
const searchPerPageMax = 100

// searchResultCap is the GitHub Search API's hard limit on total results for a
// single query. We never page past it.
const searchResultCap = 1000

// SearchOptions parameterizes a historical high-star search.
type SearchOptions struct {
	// Keywords are free-text terms (e.g. "llm tutorial"). They are joined into
	// the query verbatim.
	Keywords []string
	// Topics restrict results to repositories carrying these GitHub topics.
	Topics []string
	// MinStars sets a lower bound on stargazers; 0 means no bound.
	MinStars int
	// Limit caps how many candidates to return; <= 0 means up to searchResultCap.
	Limit int
}

// SearchClient queries the GitHub Search API for repository candidates.
type SearchClient struct {
	httpClient *http.Client
	baseURL    string
	token      string // optional; raises rate limits, never logged
}

// NewSearchClient builds a SearchClient. An empty token uses anonymous (low
// rate-limit) access. The HTTP client carries a sane timeout.
func NewSearchClient(token string) *SearchClient {
	return &SearchClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    defaultSearchBaseURL,
		token:      token,
	}
}

// ghSearchResponse mirrors the subset of the Search API payload we consume.
type ghSearchResponse struct {
	TotalCount        int      `json:"total_count"`
	IncompleteResults bool     `json:"incomplete_results"`
	Items             []ghRepo `json:"items"`
}

// ghRepo mirrors the subset of a Search API repository item we consume.
type ghRepo struct {
	FullName    string `json:"full_name"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Language    string `json:"language"`
	Stars       int    `json:"stargazers_count"`
	HTMLURL     string `json:"html_url"`
	Owner       struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// Search returns repository candidates sorted by stars (descending). Upstream
// failures (network, non-2xx status, malformed body) return *UpstreamError so
// the caller can exit with ExitUpstream.
func (c *SearchClient) Search(ctx context.Context, opts SearchOptions) ([]Candidate, error) {
	query := buildSearchQuery(opts)

	limit := opts.Limit
	if limit <= 0 || limit > searchResultCap {
		limit = searchResultCap
	}

	// per_page is fixed across pages: GitHub's `page` counts in units of
	// `per_page`, so a shrinking page size would re-fetch overlapping results.
	// We over-fetch the final page if needed and trim to limit below.
	perPage := limit
	if perPage > searchPerPageMax {
		perPage = searchPerPageMax
	}

	var candidates []Candidate
	for page := 1; len(candidates) < limit; page++ {
		resp, err := c.fetchPage(ctx, query, page, perPage)
		if err != nil {
			return nil, err
		}
		for _, item := range resp.Items {
			candidates = append(candidates, toCandidate(item))
		}

		// Stop when the source is exhausted: a short page or reaching the total
		// (capped) result count means there is nothing more to fetch.
		total := resp.TotalCount
		if total > searchResultCap {
			total = searchResultCap
		}
		if len(resp.Items) < perPage || len(candidates) >= total {
			break
		}
	}

	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, nil
}

// fetchPage requests a single page of search results.
func (c *SearchClient) fetchPage(ctx context.Context, query string, page, perPage int) (*ghSearchResponse, error) {
	const op = "github search"

	u, err := url.Parse(c.baseURL + "/search/repositories")
	if err != nil {
		return nil, upstreamErrorf(op, "build request url: %v", err)
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("sort", "stars")
	q.Set("order", "desc")
	q.Set("per_page", strconv.Itoa(perPage))
	q.Set("page", strconv.Itoa(page))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, upstreamErrorf(op, "build request: %v", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "llmlore")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &UpstreamError{Op: op, Err: err}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, upstreamErrorf(op, "read response: %v", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, upstreamErrorf(op, "unexpected status %d: %s", resp.StatusCode, ghErrorMessage(body))
	}

	var parsed ghSearchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, upstreamErrorf(op, "decode response: %v", err)
	}
	return &parsed, nil
}

// buildSearchQuery assembles a GitHub search query string from options.
func buildSearchQuery(opts SearchOptions) string {
	var parts []string
	for _, kw := range opts.Keywords {
		if kw = strings.TrimSpace(kw); kw != "" {
			parts = append(parts, kw)
		}
	}
	for _, t := range opts.Topics {
		if t = strings.TrimSpace(t); t != "" {
			parts = append(parts, "topic:"+t)
		}
	}
	if opts.MinStars > 0 {
		parts = append(parts, fmt.Sprintf("stars:>=%d", opts.MinStars))
	}
	return strings.Join(parts, " ")
}

// toCandidate converts a Search API item into a source-tagged Candidate.
func toCandidate(item ghRepo) Candidate {
	owner := item.Owner.Login
	name := item.Name
	// Fall back to splitting full_name if the structured fields are missing.
	if (owner == "" || name == "") && item.FullName != "" {
		if o, n, ok := strings.Cut(item.FullName, "/"); ok {
			if owner == "" {
				owner = o
			}
			if name == "" {
				name = n
			}
		}
	}
	url := item.HTMLURL
	if url == "" {
		url = "https://github.com/" + model.NormalizeID(owner, name)
	}
	return Candidate{
		ID:          model.NormalizeID(owner, name),
		Owner:       owner,
		Name:        name,
		URL:         url,
		Description: item.Description,
		Language:    item.Language,
		Stars:       item.Stars,
		Source:      model.SourceSearch,
	}
}

// ghErrorMessage extracts GitHub's "message" field from an error body, falling
// back to a truncated raw body so the user sees something actionable.
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
