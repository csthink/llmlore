package render

import (
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/csthink/llmlore/internal/model"
)

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// sampleDataset builds a small dataset exercising every dashboard dimension:
// mixed sources, a stale repo, a multi-topic repo, and snapshot histories of
// varying length. generated_at == the two new repos' added_at.
func sampleDataset() *model.Dataset {
	gen := ts("2026-06-01T00:00:00Z")
	return &model.Dataset{
		Meta: model.Meta{
			SchemaVersion: model.CurrentSchemaVersion,
			GeneratedAt:   gen,
			Mode:          "historical",
			Count:         3,
		},
		Repos: []model.Repo{
			{
				ID: "acme/llm-course", URL: "https://github.com/acme/llm-course",
				Owner: "acme", Name: "llm-course", Description: "Hands-on LLM course",
				Language: "Python", Stars: 5000, Summary: "Learn to build with LLMs",
				Type: model.TypeTutorial, Topics: []string{model.TopicLLM, model.TopicAgent},
				PushedAt: ts("2026-05-20T00:00:00Z"), IsStale: false,
				Source: model.SourceSearch, AddedAt: gen, ClassifiedBy: model.ClassifiedByLLM,
				StarSnapshots: []model.StarSnapshot{
					{T: ts("2026-04-01T00:00:00Z"), Stars: 4000},
					{T: ts("2026-05-25T00:00:00Z"), Stars: 4800},
					{T: gen, Stars: 5000},
				},
			},
			{
				ID: "beta/rag-demo", URL: "https://github.com/beta/rag-demo",
				Owner: "beta", Name: "rag-demo", Description: "A runnable RAG demo",
				Language: "TypeScript", Stars: 1200, Summary: "Try RAG end to end",
				Type: model.TypeExample, Topics: []string{model.TopicRAG},
				PushedAt: ts("2026-05-30T00:00:00Z"), IsStale: false,
				Source: model.SourceTrending, AddedAt: gen, ClassifiedBy: model.ClassifiedByHeuristic,
				StarSnapshots: []model.StarSnapshot{
					{T: ts("2026-05-29T00:00:00Z"), Stars: 1100},
					{T: gen, Stars: 1200},
				},
			},
			{
				ID: "old/agent-kit", URL: "https://github.com/old/agent-kit",
				Owner: "old", Name: "agent-kit", Description: "Agent templates",
				Language: "Go", Stars: 800, Summary: "Reusable agent templates",
				Type: model.TypeTemplate, Topics: []string{model.TopicAgent},
				PushedAt: ts("2024-01-01T00:00:00Z"), IsStale: true,
				Source: model.SourceSearch, AddedAt: ts("2025-01-01T00:00:00Z"),
				ClassifiedBy: model.ClassifiedByLLM,
				StarSnapshots: []model.StarSnapshot{
					{T: ts("2025-01-01T00:00:00Z"), Stars: 800},
				},
			},
		},
	}
}

func TestBuildView_Overview(t *testing.T) {
	now := ts("2026-06-01T12:00:00Z")
	v := BuildView(sampleDataset(), now)

	if v.Total != 3 {
		t.Errorf("Total = %d, want 3", v.Total)
	}
	if v.Mode != "historical" {
		t.Errorf("Mode = %q, want historical", v.Mode)
	}
	// Two repos carry AddedAt == GeneratedAt; the third was added earlier.
	if v.NewThisRound != 2 {
		t.Errorf("NewThisRound = %d, want 2", v.NewThisRound)
	}

	// agent appears in two repos, llm/rag in one each.
	want := map[string]int{"agent": 2, "llm": 1, "rag": 1}
	got := map[string]int{}
	for _, c := range v.TopicDist {
		got[c.Label] = c.N
	}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("TopicDist[%q] = %d, want %d", k, got[k], n)
		}
	}
	// Sorted by count desc: agent (2) must lead.
	if len(v.TopicDist) == 0 || v.TopicDist[0].Label != "agent" {
		t.Errorf("TopicDist[0] = %+v, want agent first", v.TopicDist)
	}
}

func TestBuildView_TopByStarsOrder(t *testing.T) {
	v := BuildView(sampleDataset(), ts("2026-06-01T12:00:00Z"))
	wantOrder := []string{"acme/llm-course", "beta/rag-demo", "old/agent-kit"}
	if len(v.TopByStars) != len(wantOrder) {
		t.Fatalf("TopByStars len = %d, want %d", len(v.TopByStars), len(wantOrder))
	}
	for i, id := range wantOrder {
		if v.TopByStars[i].ID != id {
			t.Errorf("TopByStars[%d] = %q, want %q", i, v.TopByStars[i].ID, id)
		}
	}
	// The stale repo must be flagged.
	if !v.TopByStars[2].Stale {
		t.Errorf("old/agent-kit should be marked stale")
	}
}

func TestBuildView_TrendingOnlyTrendingSource(t *testing.T) {
	v := BuildView(sampleDataset(), ts("2026-06-01T12:00:00Z"))
	if len(v.Trending) != 1 {
		t.Fatalf("Trending len = %d, want 1", len(v.Trending))
	}
	if v.Trending[0].ID != "beta/rag-demo" {
		t.Errorf("Trending[0] = %q, want beta/rag-demo", v.Trending[0].ID)
	}
}

func TestBuildView_GrowthFromSnapshots(t *testing.T) {
	now := ts("2026-06-01T12:00:00Z")
	v := BuildView(sampleDataset(), now)

	// old/agent-kit has a single snapshot → no measurable growth → excluded.
	for _, g := range v.Growing {
		if g.ID == "old/agent-kit" {
			t.Errorf("single-snapshot repo should be excluded from Growing")
		}
	}

	byID := map[string]GrowthRow{}
	for _, g := range v.Growing {
		byID[g.ID] = g
	}
	llm, ok := byID["acme/llm-course"]
	if !ok {
		t.Fatalf("acme/llm-course missing from Growing")
	}
	// 7d window (cutoff 2026-05-25 12:00): baseline is the 05-25 snapshot (4800)
	// since 04-01 is the last at/before cutoff... 05-25T00:00 is before cutoff
	// 05-25T12:00, so baseline = 4800, gain = 5000-4800 = 200.
	if llm.D7 != "+200" {
		t.Errorf("acme/llm-course D7 = %q, want +200", llm.D7)
	}
	// 30d window (cutoff 2026-05-02 12:00): baseline = 04-01 snapshot (4000),
	// gain = 5000-4000 = 1000.
	if llm.D30 != "+1000" {
		t.Errorf("acme/llm-course D30 = %q, want +1000", llm.D30)
	}

	// Growing is ordered by D30 desc: llm-course (+1000) before rag-demo (+100).
	if len(v.Growing) >= 2 && v.Growing[0].ID != "acme/llm-course" {
		t.Errorf("Growing[0] = %q, want acme/llm-course (highest 30d gain)", v.Growing[0].ID)
	}
}

func TestBuildView_FilterOptions(t *testing.T) {
	v := BuildView(sampleDataset(), ts("2026-06-01T12:00:00Z"))
	assertSorted := func(name string, got []string) {
		for i := 1; i < len(got); i++ {
			if got[i-1] > got[i] {
				t.Errorf("%s not sorted: %v", name, got)
				return
			}
		}
	}
	assertSorted("Topics", v.Topics)
	assertSorted("Types", v.Types)
	assertSorted("Languages", v.Languages)
	if want := []string{"agent", "llm", "rag"}; !equalStrings(v.Topics, want) {
		t.Errorf("Topics = %v, want %v", v.Topics, want)
	}
	if want := []string{"Go", "Python", "TypeScript"}; !equalStrings(v.Languages, want) {
		t.Errorf("Languages = %v, want %v", v.Languages, want)
	}
}

func TestBuildView_NilAndEmpty(t *testing.T) {
	v := BuildView(nil, ts("2026-06-01T12:00:00Z"))
	if v.Total != 0 || v.Mode != "unknown" || len(v.Cards) != 0 {
		t.Errorf("nil dataset should yield empty view, got %+v", v)
	}
	empty := &model.Dataset{Meta: model.Meta{SchemaVersion: 1}}
	v = BuildView(empty, ts("2026-06-01T12:00:00Z"))
	if v.Total != 0 || v.NewThisRound != 0 {
		t.Errorf("empty dataset should have zero counts, got %+v", v)
	}
}

func TestRender_SelfContainedSections(t *testing.T) {
	html, err := Render(sampleDataset(), ts("2026-06-01T12:00:00Z"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(html)
	// All five spec §6 sections present.
	for _, marker := range []string{
		`id="overview"`, `id="top"`, `id="trends"`, `id="filters"`, `id="catalog"`,
		"Overview", "Top by stars", "Trends", "Trending on GitHub", "Fastest growing",
	} {
		if !strings.Contains(s, marker) {
			t.Errorf("rendered HTML missing %q", marker)
		}
	}
	// Repo data made it into the cards.
	if !strings.Contains(s, "acme/llm-course") {
		t.Errorf("rendered HTML missing repo id")
	}
}

func TestRender_NoExternalAssets(t *testing.T) {
	html, err := Render(sampleDataset(), ts("2026-06-01T12:00:00Z"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(html)
	// Self-contained: no external stylesheet or script src. Repo URLs (anchor
	// hrefs to github.com) are expected and fine; <link rel=stylesheet> and
	// <script src> are not.
	for _, bad := range []string{"<link", "src=\"http"} {
		if strings.Contains(s, bad) {
			t.Errorf("rendered HTML references external asset via %q (not self-contained)", bad)
		}
	}
}

// TestRender_AllEnglish enforces AC-9: no user-facing CJK text. The sample data
// is English-only, so any CJK rune in the output is a leaked UI string.
func TestRender_AllEnglish(t *testing.T) {
	html, err := Render(sampleDataset(), ts("2026-06-01T12:00:00Z"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for i, r := range string(html) {
		if unicode.Is(unicode.Han, r) {
			t.Fatalf("found CJK rune %q at byte %d — UI text must be English (AC-9)", r, i)
		}
	}
}

func equalStrings(a, b []string) bool {
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
