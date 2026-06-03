package collector

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/csthink/llmlore/internal/model"
)

// defaultTrendingBaseURL is the github.com/trending root. Overridable for tests.
const defaultTrendingBaseURL = "https://github.com"

// TrendingOptions parameterizes a trending scrape.
type TrendingOptions struct {
	// Since selects the window: "daily", "weekly", or "monthly". Empty uses
	// GitHub's default (daily).
	Since string
	// Language restricts the listing to a single GitHub language slug (e.g.
	// "python"). Empty means all languages.
	Language string
}

// TrendingClient scrapes github.com/trending for recent-momentum candidates.
//
// Trending has no official API: the listing is limited, not pre-filtered by
// topic, and its HTML can change without notice. This client is therefore
// deliberately tolerant — a row it cannot parse is skipped, never fatal — and
// the page only provides candidates; downstream classification still applies.
type TrendingClient struct {
	httpClient *http.Client
	baseURL    string
}

// NewTrendingClient builds a TrendingClient with a sane HTTP timeout.
func NewTrendingClient() *TrendingClient {
	return &TrendingClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    defaultTrendingBaseURL,
	}
}

// Trending returns repository candidates parsed from the trending page. Only
// fetch/transport-level problems are fatal (returned as *UpstreamError); a
// malformed individual row is skipped so a layout tweak cannot empty the run.
func (c *TrendingClient) Trending(ctx context.Context, opts TrendingOptions) ([]Candidate, error) {
	const op = "github trending"

	u, err := url.Parse(c.baseURL + "/trending")
	if err != nil {
		return nil, upstreamErrorf(op, "build request url: %v", err)
	}
	if opts.Language != "" {
		u.Path += "/" + strings.TrimSpace(opts.Language)
	}
	if opts.Since != "" {
		q := u.Query()
		q.Set("since", strings.TrimSpace(opts.Since))
		u.RawQuery = q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, upstreamErrorf(op, "build request: %v", err)
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", "llmlore")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &UpstreamError{Op: op, Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, upstreamErrorf(op, "unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	doc, err := goquery.NewDocumentFromReader(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, upstreamErrorf(op, "parse html: %v", err)
	}

	return parseTrending(doc), nil
}

// parseTrending extracts candidates from a trending document, skipping any row
// that does not yield a usable owner/name.
func parseTrending(doc *goquery.Document) []Candidate {
	var candidates []Candidate
	doc.Find("article.Box-row").Each(func(_ int, row *goquery.Selection) {
		if cand, ok := parseTrendingRow(row); ok {
			candidates = append(candidates, cand)
		}
	})
	return candidates
}

// parseTrendingRow extracts a single Candidate. ok is false when the row lacks
// the minimum we need (a valid owner/name link), so the caller skips it.
func parseTrendingRow(row *goquery.Selection) (Candidate, bool) {
	href, ok := row.Find("h2 a").First().Attr("href")
	if !ok {
		return Candidate{}, false
	}
	owner, name, ok := ownerNameFromHref(href)
	if !ok {
		return Candidate{}, false
	}

	cand := Candidate{
		ID:     model.NormalizeID(owner, name),
		Owner:  owner,
		Name:   name,
		URL:    "https://github.com/" + model.NormalizeID(owner, name),
		Source: model.SourceTrending,
	}

	if desc := strings.TrimSpace(row.Find("p").First().Text()); desc != "" {
		cand.Description = desc
	}
	if lang := strings.TrimSpace(row.Find(`span[itemprop="programmingLanguage"]`).First().Text()); lang != "" {
		cand.Language = lang
	}
	// The first muted link in a row is the cumulative-stars count; tolerate its
	// absence or an unparseable value by leaving Stars at zero.
	if stars, ok := parseStarCount(row.Find("a.Link--muted").First().Text()); ok {
		cand.Stars = stars
	}

	return cand, true
}

// ownerNameFromHref turns a trending href like "/owner/name" into its parts.
func ownerNameFromHref(href string) (owner, name string, ok bool) {
	trimmed := strings.Trim(strings.TrimSpace(href), "/")
	owner, name, ok = strings.Cut(trimmed, "/")
	if !ok || owner == "" || name == "" {
		return "", "", false
	}
	// Guard against deeper paths (e.g. "/owner/name/tree/..."): keep only the
	// first two segments.
	if i := strings.IndexByte(name, '/'); i >= 0 {
		name = name[:i]
	}
	if name == "" {
		return "", "", false
	}
	return owner, name, true
}

// parseStarCount parses a trending star figure like "1,234" into an int. ok is
// false for empty or non-numeric text.
func parseStarCount(text string) (int, bool) {
	cleaned := strings.ReplaceAll(strings.TrimSpace(text), ",", "")
	if cleaned == "" {
		return 0, false
	}
	n, err := strconv.Atoi(cleaned)
	if err != nil {
		return 0, false
	}
	return n, true
}
