package collector

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/csthink/llmlore/internal/model"
)

// newSearchServer spins up a fake GitHub Search API that records the last query
// it saw and serves a paged set of synthetic repositories.
func newSearchServer(t *testing.T, total int) (*SearchClient, *recordedRequest) {
	t.Helper()
	rec := &recordedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.path = r.URL.Path
		rec.query = r.URL.Query().Get("q")
		rec.sort = r.URL.Query().Get("sort")
		rec.authorization = r.Header.Get("Authorization")
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if perPage == 0 {
			perPage = 30
		}
		if page == 0 {
			page = 1
		}

		start := (page - 1) * perPage
		var items []string
		for i := start; i < start+perPage && i < total; i++ {
			items = append(items, fmt.Sprintf(`{
				"full_name": "owner%d/repo%d",
				"name": "repo%d",
				"description": "desc %d",
				"language": "Go",
				"stargazers_count": %d,
				"html_url": "https://github.com/owner%d/repo%d",
				"pushed_at": "2026-01-02T03:04:05Z",
				"owner": {"login": "owner%d"}
			}`, i, i, i, i, 1000-i, i, i, i))
		}
		fmt.Fprintf(w, `{"total_count": %d, "incomplete_results": false, "items": [%s]}`,
			total, strings.Join(items, ","))
	}))
	t.Cleanup(srv.Close)

	c := NewSearchClient("secret-token")
	c.baseURL = srv.URL
	return c, rec
}

type recordedRequest struct {
	path          string
	query         string
	sort          string
	authorization string
}

func TestSearchReturnsCandidates(t *testing.T) {
	c, rec := newSearchServer(t, 3)

	got, err := c.Search(context.Background(), SearchOptions{
		Keywords: []string{"llm", "tutorial"},
		Topics:   []string{"llm", "agent"},
		MinStars: 100,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d candidates, want 3", len(got))
	}

	first := got[0]
	if first.ID != "owner0/repo0" {
		t.Errorf("ID = %q, want owner0/repo0", first.ID)
	}
	if first.Owner != "owner0" || first.Name != "repo0" {
		t.Errorf("owner/name = %q/%q, want owner0/repo0", first.Owner, first.Name)
	}
	if first.Stars != 1000 {
		t.Errorf("Stars = %d, want 1000", first.Stars)
	}
	if first.Source != model.SourceSearch {
		t.Errorf("Source = %q, want %q", first.Source, model.SourceSearch)
	}
	if first.URL != "https://github.com/owner0/repo0" {
		t.Errorf("URL = %q", first.URL)
	}
	wantPushed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if !first.PushedAt.Equal(wantPushed) {
		t.Errorf("PushedAt = %v, want %v", first.PushedAt, wantPushed)
	}

	// Query construction and request shape.
	if rec.sort != "stars" {
		t.Errorf("sort = %q, want stars", rec.sort)
	}
	for _, want := range []string{"llm", "tutorial", "topic:llm", "topic:agent", "stars:>=100"} {
		if !strings.Contains(rec.query, want) {
			t.Errorf("query %q missing %q", rec.query, want)
		}
	}
	if rec.authorization != "Bearer secret-token" {
		t.Errorf("authorization = %q, want Bearer secret-token", rec.authorization)
	}
}

func TestSearchPaginatesUpToLimit(t *testing.T) {
	// 250 available, limit 120 → needs paging (per_page caps at 100).
	c, _ := newSearchServer(t, 250)

	got, err := c.Search(context.Background(), SearchOptions{Limit: 120})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 120 {
		t.Fatalf("got %d candidates, want 120", len(got))
	}
	// Candidates must be distinct across pages (no duplicate ids).
	seen := map[string]bool{}
	for _, cand := range got {
		if seen[cand.ID] {
			t.Fatalf("duplicate candidate %q across pages", cand.ID)
		}
		seen[cand.ID] = true
	}
}

func TestSearchStopsWhenSourceExhausted(t *testing.T) {
	// Only 5 available but limit is large: must stop, not loop forever.
	c, _ := newSearchServer(t, 5)

	got, err := c.Search(context.Background(), SearchOptions{Limit: 100})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d candidates, want 5", len(got))
	}
}

func TestSearchUpstreamErrorOnBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message": "API rate limit exceeded"}`)
	}))
	t.Cleanup(srv.Close)

	c := NewSearchClient("")
	c.baseURL = srv.URL

	_, err := c.Search(context.Background(), SearchOptions{Limit: 5})
	if err == nil {
		t.Fatal("expected an error on 403")
	}
	if !IsUpstream(err) {
		t.Fatalf("IsUpstream(%v) = false, want true (must map to exit %d)", err, ExitUpstream)
	}
	if !strings.Contains(err.Error(), "API rate limit exceeded") {
		t.Errorf("error %q should surface GitHub's message", err)
	}
}

func TestBuildSearchQuery(t *testing.T) {
	q := buildSearchQuery(SearchOptions{
		Keywords: []string{"  llm ", ""},
		Topics:   []string{"agent", "  "},
		MinStars: 50,
	})
	if q != "llm topic:agent stars:>=50" {
		t.Errorf("query = %q, want %q", q, "llm topic:agent stars:>=50")
	}
}
