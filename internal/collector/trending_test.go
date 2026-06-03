package collector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/csthink/llmlore/internal/model"
)

// trendingHTML is a trimmed-but-realistic trending page: two well-formed rows,
// one row missing its repo link (must be skipped), and one with deeper href
// segments and no star count (parsed, stars left zero).
const trendingHTML = `<!DOCTYPE html><html><body>
<article class="Box-row">
  <h2 class="h3"><a href="/owner-a/repo-a">owner-a / repo-a</a></h2>
  <p class="col-9">A hands-on LLM tutorial.</p>
  <span itemprop="programmingLanguage">Python</span>
  <a class="Link--muted" href="/owner-a/repo-a/stargazers">12,345</a>
  <a class="Link--muted" href="/owner-a/repo-a/forks">678</a>
</article>
<article class="Box-row">
  <h2 class="h3"><a href="/owner-b/repo-b/tree/main">owner-b / repo-b</a></h2>
  <p class="col-9">An agent framework guide.</p>
</article>
<article class="Box-row">
  <p class="col-9">A row with no repo link at all.</p>
</article>
</body></html>`

func newTrendingServer(t *testing.T, body string) *TrendingClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	c := NewTrendingClient()
	c.baseURL = srv.URL
	return c
}

func TestTrendingParsesRowsAndToleratesBadOnes(t *testing.T) {
	c := newTrendingServer(t, trendingHTML)

	got, err := c.Trending(context.Background(), TrendingOptions{})
	if err != nil {
		t.Fatalf("Trending: %v", err)
	}
	// Three articles, but one has no link → two candidates.
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2", len(got))
	}

	a := got[0]
	if a.ID != "owner-a/repo-a" {
		t.Errorf("ID = %q, want owner-a/repo-a", a.ID)
	}
	if a.Description != "A hands-on LLM tutorial." {
		t.Errorf("Description = %q", a.Description)
	}
	if a.Language != "Python" {
		t.Errorf("Language = %q, want Python", a.Language)
	}
	if a.Stars != 12345 {
		t.Errorf("Stars = %d, want 12345", a.Stars)
	}
	if a.Source != model.SourceTrending {
		t.Errorf("Source = %q, want %q", a.Source, model.SourceTrending)
	}
	if a.URL != "https://github.com/owner-a/repo-a" {
		t.Errorf("URL = %q", a.URL)
	}

	// Second row: deeper href is trimmed to owner/name, no star count → zero.
	b := got[1]
	if b.ID != "owner-b/repo-b" {
		t.Errorf("ID = %q, want owner-b/repo-b", b.ID)
	}
	if b.Stars != 0 {
		t.Errorf("Stars = %d, want 0 (no count present)", b.Stars)
	}
}

func TestTrendingEmptyPageYieldsNoCandidates(t *testing.T) {
	c := newTrendingServer(t, `<html><body><p>nothing here</p></body></html>`)

	got, err := c.Trending(context.Background(), TrendingOptions{Since: "weekly", Language: "python"})
	if err != nil {
		t.Fatalf("Trending: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d candidates, want 0", len(got))
	}
}

func TestTrendingUpstreamErrorOnBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	c := NewTrendingClient()
	c.baseURL = srv.URL

	_, err := c.Trending(context.Background(), TrendingOptions{})
	if err == nil {
		t.Fatal("expected an error on 503")
	}
	if !IsUpstream(err) {
		t.Fatalf("IsUpstream(%v) = false, want true (must map to exit %d)", err, ExitUpstream)
	}
}

func TestOwnerNameFromHref(t *testing.T) {
	cases := []struct {
		href        string
		owner, name string
		ok          bool
	}{
		{"/owner/name", "owner", "name", true},
		{"/owner/name/tree/main", "owner", "name", true},
		{"owner/name", "owner", "name", true},
		{"/onlyowner", "", "", false},
		{"", "", "", false},
		{"/", "", "", false},
	}
	for _, tc := range cases {
		owner, name, ok := ownerNameFromHref(tc.href)
		if ok != tc.ok || owner != tc.owner || name != tc.name {
			t.Errorf("ownerNameFromHref(%q) = (%q,%q,%v), want (%q,%q,%v)",
				tc.href, owner, name, ok, tc.owner, tc.name, tc.ok)
		}
	}
}
