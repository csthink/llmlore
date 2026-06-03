package collector

import (
	"context"
	"fmt"
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
// tolerant of individual rows — one it cannot parse is skipped — but it does NOT
// swallow a wholesale layout change: when every row fails (or the listing
// container itself is gone with no empty-state marker), it reports a
// *LayoutError rather than silently returning an empty set. The page only
// provides candidates; downstream classification still applies.
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

// Trending returns repository candidates parsed from the trending page.
//
// It can return two distinct error kinds, and a caller (e.g. T6) should handle
// both:
//   - *UpstreamError — a transport-level failure (network, non-2xx status,
//     unparseable body). Maps to exit code ExitUpstream (3).
//   - *LayoutError — the page was fetched successfully but its structure was
//     not recognized: the row selector matched nothing with no empty-state
//     marker, or rows matched but none yielded an owner/name link. Severity is
//     the caller's call (log-and-continue vs fail); detect it via IsLayoutDrift.
//
// A single malformed row is tolerated (skipped); the failure only escalates to
// a *LayoutError when ALL rows fail, so a one-off markup quirk cannot empty the
// run while a genuine redesign cannot pass unnoticed. A legitimately empty
// listing (GitHub's empty-state) returns no candidates and no error.
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

	candidates, detail := parseTrending(doc)
	if detail != "" {
		// Fetched fine, but the structure was not recognized: surface it as a
		// layout drift so the caller can observe it instead of silently feeding
		// an empty trending set into the pipeline.
		return nil, &LayoutError{Source: op, Detail: detail}
	}
	return candidates, nil
}

// parseTrending extracts candidates from a trending document, skipping any row
// that does not yield a usable owner/name.
//
// It distinguishes a genuinely empty listing from a layout change. A non-empty
// detail string means the page structure was not recognized (suspected drift):
//   - the row selector matched nothing AND the page has no known empty-state
//     marker, or
//   - rows matched but none yielded a usable owner/name link.
//
// An empty detail with zero candidates means GitHub legitimately had nothing
// trending (its empty-state marker was present).
func parseTrending(doc *goquery.Document) (candidates []Candidate, detail string) {
	rows := doc.Find("article.Box-row")
	if rows.Length() == 0 {
		if isTrendingEmptyState(doc) {
			return nil, "" // legitimately no trending repositories
		}
		return nil, `no "article.Box-row" entries found`
	}

	rows.Each(func(_ int, row *goquery.Selection) {
		if cand, ok := parseTrendingRow(row); ok {
			candidates = append(candidates, cand)
		}
	})
	if len(candidates) == 0 {
		// Rows exist but the inner repo-link structure no longer parses.
		return nil, fmt.Sprintf("found %d row(s) but none yielded an owner/name link", rows.Length())
	}
	return candidates, ""
}

// isTrendingEmptyState reports whether the page is GitHub's "nothing trending"
// state rather than a structural change. GitHub renders an empty listing inside
// a `.blankslate` container; treating its presence as a legitimate empty result
// (and its absence, with no rows, as drift) errs toward signaling a problem
// rather than silently returning empty — the safer direction.
func isTrendingEmptyState(doc *goquery.Document) bool {
	return doc.Find(".blankslate").Length() > 0
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
