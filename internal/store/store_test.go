package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/csthink/llmlore/internal/collector"
	"github.com/csthink/llmlore/internal/model"
)

func repo(id string, stars int, typ string, topics ...string) model.Repo {
	ts := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	owner, name, _ := strings.Cut(id, "/")
	return model.Repo{
		ID: id, Owner: owner, Name: name, URL: "https://github.com/" + id,
		Stars: stars, Type: typ, Topics: topics, Source: model.SourceSearch,
		AddedAt: ts, PushedAt: ts, ClassifiedBy: model.ClassifiedByHeuristic,
		StarSnapshots: []model.StarSnapshot{{T: ts, Stars: stars}},
	}
}

func dataset(repos ...model.Repo) *model.Dataset {
	return &model.Dataset{
		Meta:  model.Meta{SchemaVersion: model.CurrentSchemaVersion, Mode: "historical"},
		Repos: repos,
	}
}

func TestLoadMissingFileIsEmptyDataset(t *testing.T) {
	d, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if d.Meta.SchemaVersion != model.CurrentSchemaVersion || len(d.Repos) != 0 {
		t.Errorf("got %+v, want empty current-schema dataset", d.Meta)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "repos.json")
	want := dataset(repo("o/a", 10, model.TypeTutorial, model.TopicLLM))

	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Repos) != 1 || got.Repos[0].ID != "o/a" {
		t.Errorf("round trip lost data: %+v", got.Repos)
	}
	if got.Meta.Count != 1 {
		t.Errorf("meta.count = %d, want refreshed to 1", got.Meta.Count)
	}
}

func TestSaveRejectsInvalidDataset(t *testing.T) {
	bad := dataset(model.Repo{ID: "o/a", Owner: "o", Name: "a"}) // no type/topics
	if err := Save(filepath.Join(t.TempDir(), "repos.json"), bad); err == nil {
		t.Fatal("Save accepted an invalid dataset, want refusal")
	}
}

func TestDedupeCandidatesPrefersSearch(t *testing.T) {
	in := []collector.Candidate{
		{ID: "o/a", Source: model.SourceTrending, Stars: 1},
		{ID: "o/a", Source: model.SourceSearch, Stars: 2}, // should win
		{ID: "o/b", Source: model.SourceSearch, Stars: 3},
		{ID: "o/b", Source: model.SourceTrending, Stars: 4}, // ignored
	}
	out := DedupeCandidates(in)
	if len(out) != 2 {
		t.Fatalf("got %d, want 2 unique", len(out))
	}
	byID := map[string]collector.Candidate{}
	for _, c := range out {
		byID[c.ID] = c
	}
	if byID["o/a"].Source != model.SourceSearch || byID["o/a"].Stars != 2 {
		t.Errorf("o/a = %+v, want the search hit", byID["o/a"])
	}
	if byID["o/b"].Source != model.SourceSearch {
		t.Errorf("o/b source = %q, want search kept", byID["o/b"].Source)
	}
}

func TestMergeDedupesByID(t *testing.T) {
	existing := dataset(
		repo("o/keep", 5, model.TypeGuide, model.TopicLLM), // untouched this run
		repo("o/upd", 10, model.TypeGuide, model.TopicLLM), // replaced
	)
	updated := repo("o/upd", 99, model.TypeGuide, model.TopicLLM)
	added := repo("o/new", 7, model.TypeExample, model.TopicAgent)

	got := Merge(existing, []model.Repo{updated, added})

	if len(got.Repos) != 3 {
		t.Fatalf("got %d repos, want 3", len(got.Repos))
	}
	idx := Index(got)
	if idx["o/upd"].Stars != 99 {
		t.Errorf("o/upd stars = %d, want replaced to 99", idx["o/upd"].Stars)
	}
	if idx["o/keep"].Stars != 5 {
		t.Errorf("o/keep stars = %d, want untouched 5", idx["o/keep"].Stars)
	}
	if _, ok := idx["o/new"]; !ok {
		t.Errorf("o/new not added")
	}
}

func TestSelectAppliesMinStars(t *testing.T) {
	d := dataset(
		repo("o/hi", 100, model.TypeGuide, model.TopicLLM),
		repo("o/lo", 5, model.TypeGuide, model.TopicLLM),
	)
	got := Select(d, SelectOptions{MinStars: 50})
	if len(got.Repos) != 1 || got.Repos[0].ID != "o/hi" {
		t.Errorf("got %v, want only o/hi above the floor", ids(got.Repos))
	}
}

func TestSelectAppliesPerTypeCap(t *testing.T) {
	d := dataset(
		repo("o/a", 30, model.TypeTutorial, model.TopicLLM),
		repo("o/b", 20, model.TypeTutorial, model.TopicLLM),
		repo("o/c", 10, model.TypeTutorial, model.TopicLLM),
	)
	got := Select(d, SelectOptions{PerTypeCap: 2})
	// Highest two by stars within the type are kept.
	if want := []string{"o/a", "o/b"}; !equal(ids(got.Repos), want) {
		t.Errorf("got %v, want %v (per-type cap 2, star-ranked)", ids(got.Repos), want)
	}
}

func TestSelectTopicCountingDoesNotChargeFullTopics(t *testing.T) {
	// Topic cap 1. o/a (llm) is admitted, filling llm. o/b is (llm, agent): llm
	// is full but agent has room → admitted via agent, and only agent is charged
	// (llm not charged again). o/c (agent) is then rejected: agent full.
	d := dataset(
		repo("o/a", 30, model.TypeGuide, model.TopicLLM),
		repo("o/b", 20, model.TypeGuide, model.TopicLLM, model.TopicAgent),
		repo("o/c", 10, model.TypeGuide, model.TopicAgent),
	)
	got := Select(d, SelectOptions{PerTopicCap: 1})
	if want := []string{"o/a", "o/b"}; !equal(ids(got.Repos), want) {
		t.Errorf("got %v, want %v", ids(got.Repos), want)
	}
}

func ids(repos []model.Repo) []string {
	out := make([]string, len(repos))
	for i, r := range repos {
		out[i] = r.ID
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
