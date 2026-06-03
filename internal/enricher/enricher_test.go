package enricher

import (
	"strings"
	"testing"
	"time"

	"github.com/csthink/llmlore/internal/classifier"
	"github.com/csthink/llmlore/internal/collector"
	"github.com/csthink/llmlore/internal/model"
)

var (
	now    = time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	recent = now.AddDate(0, -1, 0) // 1 month ago → fresh
	old    = now.AddDate(-2, 0, 0) // 2 years ago → stale
)

func cand(id string, stars int, pushed time.Time, src string) collector.Candidate {
	owner, name, _ := strings.Cut(id, "/")
	return collector.Candidate{
		ID: id, Owner: owner, Name: name, URL: "https://github.com/" + id,
		Description: "desc", Language: "Go", Stars: stars, PushedAt: pushed, Source: src,
	}
}

func TestNewRepoBuildsFirstRecord(t *testing.T) {
	dec := classifier.Decision{
		Keep: true, Type: model.TypeTutorial, Topics: []string{model.TopicLLM},
		Summary: "A summary.", ClassifiedBy: model.ClassifiedByLLM,
	}
	r := NewRepo(cand("o/repo", 100, recent, model.SourceSearch), dec, now, DefaultStaleThreshold)

	if r.AddedAt != now {
		t.Errorf("AddedAt = %v, want %v", r.AddedAt, now)
	}
	if len(r.StarSnapshots) != 1 || r.StarSnapshots[0].T != now || r.StarSnapshots[0].Stars != 100 {
		t.Errorf("StarSnapshots = %v, want one {now,100}", r.StarSnapshots)
	}
	if r.Type != model.TypeTutorial || r.Summary != "A summary." || r.ClassifiedBy != model.ClassifiedByLLM {
		t.Errorf("classification not carried through: %+v", r)
	}
	if r.IsStale {
		t.Errorf("IsStale = true, want false for a recent push")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("produced record is invalid: %v", err)
	}
}

func TestNewRepoStaleAndZeroPushedAt(t *testing.T) {
	dec := classifier.Decision{Keep: true, Type: model.TypeGuide, Topics: []string{model.TopicOther}, ClassifiedBy: model.ClassifiedByHeuristic}

	stale := NewRepo(cand("o/old", 10, old, model.SourceSearch), dec, now, DefaultStaleThreshold)
	if !stale.IsStale {
		t.Errorf("IsStale = false, want true for a 2-year-old push")
	}

	// Zero pushed_at (e.g. trending) must NOT be judged stale (Q2).
	unknown := NewRepo(cand("o/trend", 10, time.Time{}, model.SourceTrending), dec, now, DefaultStaleThreshold)
	if unknown.IsStale {
		t.Errorf("IsStale = true, want false for unknown pushed_at")
	}
}

func TestRefreshAppendsSnapshotAndPreservesClassification(t *testing.T) {
	prev := model.Repo{
		ID: "o/repo", Owner: "o", Name: "repo", URL: "https://github.com/o/repo",
		Description: "old desc", Language: "Go", Stars: 100,
		Summary: "Original summary.", Type: model.TypeTutorial, Topics: []string{model.TopicLLM},
		PushedAt: recent, Source: model.SourceSearch, AddedAt: old, ClassifiedBy: model.ClassifiedByLLM,
		StarSnapshots: []model.StarSnapshot{{T: old, Stars: 50}, {T: recent, Stars: 100}},
	}
	later := now.AddDate(0, 1, 0)
	updated := Refresh(prev, cand("o/repo", 175, later, model.SourceSearch), later, DefaultStaleThreshold)

	// Volatile facts refreshed.
	if updated.Stars != 175 {
		t.Errorf("Stars = %d, want 175", updated.Stars)
	}
	if !updated.PushedAt.Equal(later) {
		t.Errorf("PushedAt = %v, want %v", updated.PushedAt, later)
	}

	// Classification, AddedAt, and identity preserved (no re-classify, design §8).
	if updated.Summary != "Original summary." || updated.Type != model.TypeTutorial || updated.ClassifiedBy != model.ClassifiedByLLM {
		t.Errorf("classification changed: %+v", updated)
	}
	if updated.AddedAt != old {
		t.Errorf("AddedAt = %v, want preserved %v", updated.AddedAt, old)
	}

	// History preserved + exactly one snapshot appended, ascending.
	if len(updated.StarSnapshots) != 3 {
		t.Fatalf("snapshots = %d, want 3 (2 history + 1 appended)", len(updated.StarSnapshots))
	}
	last := updated.StarSnapshots[2]
	if last.T != later || last.Stars != 175 {
		t.Errorf("appended snapshot = %v, want {later,175}", last)
	}
	if err := updated.Validate(); err != nil {
		t.Errorf("refreshed record is invalid: %v", err)
	}

	// prev must not be mutated (no slice aliasing).
	if len(prev.StarSnapshots) != 2 {
		t.Errorf("prev snapshots mutated: now %d, want 2", len(prev.StarSnapshots))
	}
}

func TestRefreshAcceptsEqualTimestampSnapshot(t *testing.T) {
	// Boundary: now == the last snapshot's time. model.Repo.Validate defines
	// ascending as non-decreasing (it rejects only strictly-earlier), so an equal
	// timestamp is accepted and appended. This test pins that alignment: if
	// Validate is ever tightened to strictly-increasing, it will fail and force
	// Refresh's gate to change too.
	prev := model.Repo{
		ID: "o/repo", Owner: "o", Name: "repo", URL: "u", Type: model.TypeGuide,
		Topics: []string{model.TopicLLM}, Source: model.SourceSearch, ClassifiedBy: model.ClassifiedByLLM,
		AddedAt: old, Stars: 100, PushedAt: recent,
		StarSnapshots: []model.StarSnapshot{{T: recent, Stars: 100}},
	}
	updated := Refresh(prev, cand("o/repo", 120, recent, model.SourceSearch), recent, DefaultStaleThreshold)
	if len(updated.StarSnapshots) != 2 {
		t.Fatalf("snapshots = %d, want 2 (equal timestamp appended)", len(updated.StarSnapshots))
	}
	if err := updated.Validate(); err != nil {
		t.Errorf("equal-timestamp history rejected by Validate: %v", err)
	}
}

func TestRefreshKeepsPriorPushedAtWhenSourceCannotObserve(t *testing.T) {
	prev := model.Repo{
		ID: "o/repo", Owner: "o", Name: "repo", URL: "u", Type: model.TypeGuide,
		Topics: []string{model.TopicLLM}, Source: model.SourceSearch, ClassifiedBy: model.ClassifiedByLLM,
		PushedAt: recent, AddedAt: old, Stars: 100,
		StarSnapshots: []model.StarSnapshot{{T: recent, Stars: 100}},
	}
	// A trending re-hit has no pushed_at; the prior value must survive.
	updated := Refresh(prev, cand("o/repo", 120, time.Time{}, model.SourceTrending), now, DefaultStaleThreshold)
	if !updated.PushedAt.Equal(recent) {
		t.Errorf("PushedAt = %v, want preserved %v", updated.PushedAt, recent)
	}
}

func TestRefreshRejectsOutOfOrderObservationWholesale(t *testing.T) {
	prev := model.Repo{
		ID: "o/repo", Owner: "o", Name: "repo", URL: "u", Type: model.TypeGuide,
		Topics: []string{model.TopicLLM}, Source: model.SourceSearch, ClassifiedBy: model.ClassifiedByLLM,
		AddedAt: old, Stars: 100, PushedAt: recent, IsStale: false,
		StarSnapshots: []model.StarSnapshot{{T: recent, Stars: 100}},
	}
	// A replayed/older observation: now is before the last snapshot at `recent`,
	// AND it would regress stars (50 < 100) and claim a 3-year-old push. The whole
	// observation must be rejected — snapshot history AND current state untouched.
	earlier := recent.AddDate(0, -1, 0)
	stale := cand("o/repo", 50, now.AddDate(-3, 0, 0), model.SourceSearch)
	updated := Refresh(prev, stale, earlier, DefaultStaleThreshold)

	if len(updated.StarSnapshots) != 1 {
		t.Errorf("snapshots = %d, want 1 (out-of-order observation rejected)", len(updated.StarSnapshots))
	}
	// Volatile state must NOT be half-applied: no star regression, no flipped fields.
	if updated.Stars != 100 {
		t.Errorf("Stars = %d, want 100 (must not regress on a rejected observation)", updated.Stars)
	}
	if !updated.PushedAt.Equal(recent) {
		t.Errorf("PushedAt = %v, want unchanged %v", updated.PushedAt, recent)
	}
	if updated.IsStale {
		t.Errorf("IsStale = true, want unchanged false (must not flip on a rejected observation)")
	}
	if err := updated.Validate(); err != nil {
		t.Errorf("record invalid after rejection: %v", err)
	}
}
