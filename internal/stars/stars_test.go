package stars

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/csthink/llmlore/internal/classifier"
	"github.com/csthink/llmlore/internal/collector"
	"github.com/csthink/llmlore/internal/model"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return ts
}

// --- path resolution ---------------------------------------------------------

func TestDefaultDataPath(t *testing.T) {
	xdg := DefaultDataPath(func(k string) string {
		if k == envXDGDataHome {
			return "/xdg/data"
		}
		return ""
	})
	if want := filepath.Join("/xdg/data", "llmlore", "my-stars.json"); xdg != want {
		t.Errorf("XDG path = %q, want %q", xdg, want)
	}

	home := DefaultDataPath(func(k string) string {
		if k == envHome {
			return "/home/mars"
		}
		return ""
	})
	if want := filepath.Join("/home/mars", ".local", "share", "llmlore", "my-stars.json"); home != want {
		t.Errorf("HOME path = %q, want %q", home, want)
	}
}

// --- load / save roundtrip ---------------------------------------------------

func TestLoadMissingFileIsEmpty(t *testing.T) {
	ds, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if len(ds.Repos) != 0 || ds.Meta.SchemaVersion != SchemaVersion {
		t.Errorf("missing file should yield empty current-schema dataset, got %+v", ds.Meta)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "my-stars.json")
	now := mustTime(t, "2026-06-01T00:00:00Z")
	in := &Dataset{
		Meta:  Meta{GeneratedAt: now, User: "mars"},
		Repos: []Repo{{ID: "a/b", Owner: "a", Name: "b", Stars: 5, Type: model.TypeGuide, Topics: []string{model.TopicLLM}}},
	}
	if err := Save(path, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.Meta.SchemaVersion != SchemaVersion || out.Meta.Count != 1 || out.Meta.User != "mars" {
		t.Errorf("meta not persisted/stamped: %+v", out.Meta)
	}
	if len(out.Repos) != 1 || out.Repos[0].ID != "a/b" {
		t.Errorf("repos not persisted: %+v", out.Repos)
	}
}

func TestSaveUsesOwnerOnlyPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "my-stars.json")
	if err := Save(path, &Dataset{}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perm = %o, want 600 (personal data)", perm)
	}
}

// --- Sync --------------------------------------------------------------------

func TestSyncAddsNewReposWithSnapshot(t *testing.T) {
	now := mustTime(t, "2026-06-01T00:00:00Z")
	fetched := []Repo{{ID: "a/b", Owner: "a", Name: "b", Stars: 10, PushedAt: now}}
	got := Sync(&Dataset{Meta: Meta{SchemaVersion: SchemaVersion}}, fetched, "mars", now)

	if len(got.Repos) != 1 {
		t.Fatalf("want 1 repo, got %d", len(got.Repos))
	}
	r := got.Repos[0]
	if len(r.StarSnapshots) != 1 || r.StarSnapshots[0].Stars != 10 || !r.StarSnapshots[0].T.Equal(now) {
		t.Errorf("new repo should get one snapshot at now, got %+v", r.StarSnapshots)
	}
	if got.Meta.User != "mars" || !got.Meta.GeneratedAt.Equal(now) {
		t.Errorf("meta not stamped: %+v", got.Meta)
	}
}

func TestSyncRefreshPreservesClassificationAndAppendsSnapshot(t *testing.T) {
	t0 := mustTime(t, "2026-05-01T00:00:00Z")
	t1 := mustTime(t, "2026-06-01T00:00:00Z")
	existing := &Dataset{Meta: Meta{SchemaVersion: SchemaVersion}, Repos: []Repo{{
		ID: "a/b", Owner: "a", Name: "b", Stars: 10,
		Type: model.TypeTutorial, Topics: []string{model.TopicAgent}, Summary: "learn agents",
		ClassifiedBy: model.ClassifiedByLLM, StarredAt: t0,
		StarSnapshots: []model.StarSnapshot{{T: t0, Stars: 10}},
	}}}
	fetched := []Repo{{ID: "a/b", Owner: "a", Name: "b", Stars: 25, PushedAt: t1}}

	got := Sync(existing, fetched, "mars", t1)
	r := got.Repos[0]
	if r.Type != model.TypeTutorial || r.Summary != "learn agents" || r.ClassifiedBy != model.ClassifiedByLLM {
		t.Errorf("classification not preserved on refresh: %+v", r)
	}
	if !r.StarredAt.Equal(t0) {
		t.Errorf("starred_at should be preserved, got %v", r.StarredAt)
	}
	if r.Stars != 25 {
		t.Errorf("stars should refresh to 25, got %d", r.Stars)
	}
	if len(r.StarSnapshots) != 2 || r.StarSnapshots[1].Stars != 25 {
		t.Errorf("snapshot should be appended, got %+v", r.StarSnapshots)
	}
}

func TestSyncDropsUnstarredRepos(t *testing.T) {
	now := mustTime(t, "2026-06-01T00:00:00Z")
	existing := &Dataset{Meta: Meta{SchemaVersion: SchemaVersion}, Repos: []Repo{
		{ID: "a/b", Owner: "a", Name: "b"},
		{ID: "c/d", Owner: "c", Name: "d"},
	}}
	// Only a/b is still starred.
	got := Sync(existing, []Repo{{ID: "a/b", Owner: "a", Name: "b"}}, "mars", now)
	if len(got.Repos) != 1 || got.Repos[0].ID != "a/b" {
		t.Errorf("unstarred repo should be dropped, got %+v", got.Repos)
	}
}

func TestSyncRejectsOutOfOrderObservation(t *testing.T) {
	t0 := mustTime(t, "2026-06-01T00:00:00Z")
	earlier := mustTime(t, "2026-05-01T00:00:00Z")
	existing := &Dataset{Meta: Meta{SchemaVersion: SchemaVersion}, Repos: []Repo{{
		ID: "a/b", Owner: "a", Name: "b", Stars: 10,
		StarSnapshots: []model.StarSnapshot{{T: t0, Stars: 10}},
	}}}
	got := Sync(existing, []Repo{{ID: "a/b", Owner: "a", Name: "b", Stars: 99}}, "mars", earlier)
	r := got.Repos[0]
	if r.Stars != 10 || len(r.StarSnapshots) != 1 {
		t.Errorf("out-of-order sync must change nothing, got stars=%d snaps=%+v", r.Stars, r.StarSnapshots)
	}
}

func TestSyncDerivesStale(t *testing.T) {
	now := mustTime(t, "2026-06-01T00:00:00Z")
	old := now.Add(-2 * StaleThreshold)
	got := Sync(&Dataset{Meta: Meta{SchemaVersion: SchemaVersion}},
		[]Repo{{ID: "a/b", Owner: "a", Name: "b", PushedAt: old}}, "mars", now)
	if !got.Repos[0].IsStale {
		t.Error("repo pushed long ago should be marked stale")
	}
}

// --- Organize ----------------------------------------------------------------

func TestOrganizeLabelsEveryRepoWithoutDropping(t *testing.T) {
	ds := &Dataset{Meta: Meta{SchemaVersion: SchemaVersion}, Repos: []Repo{
		{ID: "a/llm-tutorial", Owner: "a", Name: "llm-tutorial", Description: "a hands-on tutorial to learn llm agents"},
		{ID: "x/random", Owner: "x", Name: "random", Description: "a database driver"},
	}}
	got, err := Organize(context.Background(), ds, classifier.NewHeuristic())
	if err != nil {
		t.Fatalf("Organize: %v", err)
	}
	if len(got.Repos) != 2 {
		t.Fatalf("Organize must never drop repos, got %d", len(got.Repos))
	}
	for _, r := range got.Repos {
		if r.Type == "" || len(r.Topics) == 0 || r.ClassifiedBy != model.ClassifiedByHeuristic {
			t.Errorf("repo %s not fully labelled: %+v", r.ID, r)
		}
	}
	if ds.Repos[0].Type != "" {
		t.Error("Organize must not mutate the input dataset")
	}
}

// --- Stale / Search / Cross --------------------------------------------------

func TestStaleListsArchivedAndInactive(t *testing.T) {
	ds := &Dataset{Repos: []Repo{
		{ID: "a/active", IsStale: false, Archived: false},
		{ID: "b/archived", Archived: true},
		{ID: "c/inactive", IsStale: true},
	}}
	got := Stale(ds)
	if len(got) != 2 {
		t.Fatalf("want 2 candidates, got %d", len(got))
	}
	if got[0].ID != "b/archived" || got[1].ID != "c/inactive" {
		t.Errorf("stale list not sorted by id: %+v", got)
	}
}

func TestSearchMatchesAcrossFields(t *testing.T) {
	ds := &Dataset{Repos: []Repo{
		{ID: "a/b", Language: "Go", Type: model.TypeGuide, Topics: []string{model.TopicRAG}, Stars: 5},
		{ID: "c/d", Language: "Python", Type: model.TypeExample, Topics: []string{model.TopicLLM}, Stars: 9},
	}}
	if got := Search(ds, "rag"); len(got) != 1 || got[0].ID != "a/b" {
		t.Errorf("topic search failed: %+v", got)
	}
	if got := Search(ds, "python"); len(got) != 1 || got[0].ID != "c/d" {
		t.Errorf("language search failed: %+v", got)
	}
	// empty query returns all, sorted by stars desc
	got := Search(ds, "")
	if len(got) != 2 || got[0].ID != "c/d" {
		t.Errorf("empty query should return all by stars desc: %+v", got)
	}
}

func TestCrossSplitsAndRanks(t *testing.T) {
	catalog := &model.Dataset{Repos: []model.Repo{
		{ID: "a/b", Stars: 100},
		{ID: "c/d", Stars: 300},
		{ID: "e/f", Stars: 200},
	}}
	mine := &Dataset{Repos: []Repo{{ID: "a/b"}}}
	res := Cross(catalog, mine, 0)
	if len(res.AlreadyStarred) != 1 || res.AlreadyStarred[0].ID != "a/b" {
		t.Errorf("already-starred split wrong: %+v", res.AlreadyStarred)
	}
	if len(res.Recommended) != 2 || res.Recommended[0].ID != "c/d" || res.Recommended[1].ID != "e/f" {
		t.Errorf("recommendations not ranked by stars desc: %+v", res.Recommended)
	}
	if got := Cross(catalog, mine, 1); len(got.Recommended) != 1 {
		t.Errorf("limit not applied: %+v", got.Recommended)
	}
}

// --- FetchStarred (fake API) -------------------------------------------------

func newStarsServer(t *testing.T, total int, status int) (*Client, *string) {
	t.Helper()
	var seenAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"message":"denied"}`)
			return
		}
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if perPage == 0 {
			perPage = perPageMax
		}
		start := (page - 1) * perPage
		var items []string
		for i := start; i < start+perPage && i < total; i++ {
			items = append(items, fmt.Sprintf(`{
				"starred_at":"2026-01-02T03:04:05Z",
				"repo":{"full_name":"owner%d/repo%d","name":"repo%d","stargazers_count":%d,
				"html_url":"https://github.com/owner%d/repo%d","archived":false,
				"pushed_at":"2026-05-01T00:00:00Z","owner":{"login":"owner%d"}}
			}`, i, i, i, 1000-i, i, i, i))
		}
		fmt.Fprintf(w, "[%s]", strings.Join(items, ","))
	}))
	t.Cleanup(srv.Close)
	c := NewClient("secret")
	c.baseURL = srv.URL
	return c, &seenAuth
}

func TestFetchStarredPaginates(t *testing.T) {
	c, auth := newStarsServer(t, 150, http.StatusOK) // 2 pages of 100
	repos, err := c.FetchStarred(context.Background(), "mars")
	if err != nil {
		t.Fatalf("FetchStarred: %v", err)
	}
	if len(repos) != 150 {
		t.Errorf("want 150 repos across pages, got %d", len(repos))
	}
	if repos[0].ID != "owner0/repo0" || repos[0].StarredAt.IsZero() {
		t.Errorf("repo not parsed (id/starred_at): %+v", repos[0])
	}
	if *auth != "Bearer secret" {
		t.Errorf("token not sent: %q", *auth)
	}
}

func TestFetchStarredAuthErrorMapsToAuth(t *testing.T) {
	c, _ := newStarsServer(t, 0, http.StatusUnauthorized)
	_, err := c.FetchStarred(context.Background(), "mars")
	if !IsAuth(err) {
		t.Errorf("401 should be an AuthError (exit 4), got %v", err)
	}
}

func TestFetchStarredServerErrorMapsToUpstream(t *testing.T) {
	c, _ := newStarsServer(t, 0, http.StatusInternalServerError)
	_, err := c.FetchStarred(context.Background(), "mars")
	if !collector.IsUpstream(err) {
		t.Errorf("5xx should be an UpstreamError (exit 3), got %v", err)
	}
}
