package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/csthink/llmlore/internal/classifier"
	"github.com/csthink/llmlore/internal/collector"
	"github.com/csthink/llmlore/internal/model"
	"github.com/csthink/llmlore/internal/store"
)

func tparse(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// --- fakes ------------------------------------------------------------------

type fakeSearcher struct {
	cands []collector.Candidate
	err   error
}

func (f fakeSearcher) Search(context.Context, collector.SearchOptions) ([]collector.Candidate, error) {
	return f.cands, f.err
}

type fakeTrender struct {
	cands []collector.Candidate
	err   error
}

func (f fakeTrender) Trending(context.Context, collector.TrendingOptions) ([]collector.Candidate, error) {
	return f.cands, f.err
}

// fakeClassifier returns a preset decision per candidate id. A missing id
// defaults to keep=false. failIfCalled records whether Classify ran, so a test
// can assert a re-seen repo is NOT reclassified.
type fakeClassifier struct {
	decisions map[string]classifier.Decision
	err       error
	called    map[string]bool
}

func (f *fakeClassifier) Classify(_ context.Context, c collector.Candidate) (classifier.Decision, error) {
	if f.called == nil {
		f.called = map[string]bool{}
	}
	f.called[c.ID] = true
	if f.err != nil {
		return classifier.Decision{}, f.err
	}
	return f.decisions[c.ID], nil
}

func cand(id, owner, name string, stars int) collector.Candidate {
	return collector.Candidate{
		ID: id, Owner: owner, Name: name, URL: "https://github.com/" + id,
		Stars: stars, PushedAt: tparse("2026-05-01T00:00:00Z"), Source: model.SourceSearch,
	}
}

func keep(typ string, topics ...string) classifier.Decision {
	return classifier.Decision{Keep: true, Type: typ, Topics: topics, ClassifiedBy: model.ClassifiedByLLM}
}

func emptyDataset() *model.Dataset {
	return &model.Dataset{Meta: model.Meta{SchemaVersion: model.CurrentSchemaVersion}}
}

// --- tests ------------------------------------------------------------------

func TestRunPipeline_ClassifiesNewAndStampsMeta(t *testing.T) {
	now := tparse("2026-06-01T00:00:00Z")
	deps := pipelineDeps{
		search: fakeSearcher{cands: []collector.Candidate{
			cand("a/b", "a", "b", 100),
			cand("c/d", "c", "d", 50),
		}},
		classify: &fakeClassifier{decisions: map[string]classifier.Decision{
			"a/b": keep(model.TypeTutorial, model.TopicLLM),
			// c/d absent → keep=false → dropped.
		}},
		now: now,
	}
	out, err := runPipeline(context.Background(), emptyDataset(), deps, updateOptions{mode: modeHistorical})
	if err != nil {
		t.Fatalf("runPipeline: %v", err)
	}
	if len(out.Repos) != 1 || out.Repos[0].ID != "a/b" {
		t.Fatalf("expected only a/b kept, got %+v", out.Repos)
	}
	r := out.Repos[0]
	if r.Type != model.TypeTutorial || len(r.StarSnapshots) != 1 || r.AddedAt != now {
		t.Errorf("new repo not enriched as expected: %+v", r)
	}
	if out.Meta.Mode != modeHistorical || !out.Meta.GeneratedAt.Equal(now) || out.Meta.Count != 1 {
		t.Errorf("meta not stamped: %+v", out.Meta)
	}
}

func TestRunPipeline_RefreshesExistingWithoutReclassifying(t *testing.T) {
	now := tparse("2026-06-01T00:00:00Z")
	existing := &model.Dataset{
		Meta: model.Meta{SchemaVersion: model.CurrentSchemaVersion},
		Repos: []model.Repo{{
			ID: "a/b", URL: "https://github.com/a/b", Owner: "a", Name: "b",
			Stars: 100, Type: model.TypeTutorial, Topics: []string{model.TopicLLM},
			PushedAt: tparse("2026-04-01T00:00:00Z"), Source: model.SourceSearch,
			AddedAt: tparse("2026-04-01T00:00:00Z"), ClassifiedBy: model.ClassifiedByLLM,
			StarSnapshots: []model.StarSnapshot{{T: tparse("2026-04-01T00:00:00Z"), Stars: 100}},
		}},
	}
	clf := &fakeClassifier{decisions: map[string]classifier.Decision{}}
	deps := pipelineDeps{
		search:   fakeSearcher{cands: []collector.Candidate{cand("a/b", "a", "b", 150)}},
		classify: clf,
		now:      now,
	}
	out, err := runPipeline(context.Background(), existing, deps, updateOptions{mode: modeHistorical})
	if err != nil {
		t.Fatalf("runPipeline: %v", err)
	}
	if clf.called["a/b"] {
		t.Errorf("re-seen repo must NOT be reclassified")
	}
	if len(out.Repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(out.Repos))
	}
	r := out.Repos[0]
	if r.Stars != 150 {
		t.Errorf("stars not refreshed: %d", r.Stars)
	}
	if len(r.StarSnapshots) != 2 {
		t.Errorf("expected a snapshot appended, got %d", len(r.StarSnapshots))
	}
	if r.AddedAt != tparse("2026-04-01T00:00:00Z") {
		t.Errorf("AddedAt must be preserved, got %v", r.AddedAt)
	}
}

func TestRunPipeline_BothModeDedupes(t *testing.T) {
	now := tparse("2026-06-01T00:00:00Z")
	deps := pipelineDeps{
		search:   fakeSearcher{cands: []collector.Candidate{cand("a/b", "a", "b", 100)}},
		trending: fakeTrender{cands: []collector.Candidate{cand("a/b", "a", "b", 100)}},
		classify: &fakeClassifier{decisions: map[string]classifier.Decision{
			"a/b": keep(model.TypeTutorial, model.TopicLLM),
		}},
		now: now,
	}
	out, err := runPipeline(context.Background(), emptyDataset(), deps, updateOptions{mode: modeBoth})
	if err != nil {
		t.Fatalf("runPipeline: %v", err)
	}
	if len(out.Repos) != 1 {
		t.Errorf("duplicate id across sources should collapse to one, got %d", len(out.Repos))
	}
}

func TestRunPipeline_PropagatesClassifierError(t *testing.T) {
	sentinel := &classifier.UpstreamError{Op: "llm", Err: errors.New("boom")}
	deps := pipelineDeps{
		search:   fakeSearcher{cands: []collector.Candidate{cand("a/b", "a", "b", 100)}},
		classify: &fakeClassifier{err: sentinel},
		now:      tparse("2026-06-01T00:00:00Z"),
	}
	_, err := runPipeline(context.Background(), emptyDataset(), deps, updateOptions{mode: modeHistorical})
	if !classifier.IsUpstream(err) {
		t.Fatalf("expected upstream error to propagate, got %v", err)
	}
}

func TestRunPipeline_SearchErrorPropagates(t *testing.T) {
	sentinel := &collector.UpstreamError{Op: "search", Err: errors.New("net down")}
	deps := pipelineDeps{
		search:   fakeSearcher{err: sentinel},
		classify: &fakeClassifier{},
		now:      tparse("2026-06-01T00:00:00Z"),
	}
	_, err := runPipeline(context.Background(), emptyDataset(), deps, updateOptions{mode: modeHistorical})
	if !collector.IsUpstream(err) {
		t.Fatalf("expected search upstream error to propagate, got %v", err)
	}
}

func TestSelectOptionsFor(t *testing.T) {
	// No --limit: fall back to built-in caps; --min-stars passes through.
	sel := selectOptionsFor(updateOptions{minStars: 50})
	if sel.MinStars != 50 {
		t.Errorf("MinStars = %d, want 50", sel.MinStars)
	}
	if sel.PerTypeCap != store.DefaultPerTypeCap || sel.PerTopicCap != store.DefaultPerTopicCap {
		t.Errorf("zero --limit should use default caps, got type=%d topic=%d", sel.PerTypeCap, sel.PerTopicCap)
	}
	// --limit overrides both per-category caps.
	sel = selectOptionsFor(updateOptions{limit: 5})
	if sel.PerTypeCap != 5 || sel.PerTopicCap != 5 {
		t.Errorf("--limit 5 should set both caps to 5, got type=%d topic=%d", sel.PerTypeCap, sel.PerTopicCap)
	}
}

// TestUpdateLimit_ConstrainsRenderedCards proves --limit is effective end to
// end, not merely registered: it flows selectOptionsFor → buildDashboard →
// store.Select → render and reduces the number of cards in the rendered HTML.
// Three repos share one topic/type; --limit 1 must leave exactly one card.
func TestUpdateLimit_ConstrainsRenderedCards(t *testing.T) {
	now := tparse("2026-06-01T00:00:00Z")
	mk := func(id, owner, name string, stars int) model.Repo {
		return model.Repo{
			ID: id, URL: "https://github.com/" + id, Owner: owner, Name: name,
			Stars: stars, Type: model.TypeTutorial, Topics: []string{model.TopicLLM},
			PushedAt: now, Source: model.SourceSearch, AddedAt: now,
			ClassifiedBy:  model.ClassifiedByHeuristic,
			StarSnapshots: []model.StarSnapshot{{T: now, Stars: stars}},
		}
	}
	ds := &model.Dataset{
		Meta:  model.Meta{SchemaVersion: model.CurrentSchemaVersion, GeneratedAt: now},
		Repos: []model.Repo{mk("a/x", "a", "x", 300), mk("b/y", "b", "y", 200), mk("c/z", "c", "z", 100)},
	}

	countCards := func(html []byte) int {
		return strings.Count(string(html), `<article class="card"`)
	}

	full, err := buildDashboard(ds, now, selectOptionsFor(updateOptions{}))
	if err != nil {
		t.Fatalf("buildDashboard (no limit): %v", err)
	}
	if n := countCards(full); n != 3 {
		t.Fatalf("no --limit: rendered %d cards, want 3", n)
	}

	limited, err := buildDashboard(ds, now, selectOptionsFor(updateOptions{limit: 1}))
	if err != nil {
		t.Fatalf("buildDashboard (limit 1): %v", err)
	}
	if n := countCards(limited); n != 1 {
		t.Errorf("--limit 1: rendered %d cards, want 1 (per-topic visible cap not applied)", n)
	}
}

func TestValidateMode(t *testing.T) {
	for _, m := range []string{modeHistorical, modeTrending, modeBoth} {
		if err := validateMode(m); err != nil {
			t.Errorf("validateMode(%q) = %v, want nil", m, err)
		}
	}
	err := validateMode("nope")
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Errorf("validateMode(nope) should be a usageError, got %v", err)
	}
}
