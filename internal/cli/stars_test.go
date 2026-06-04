package cli

import (
	"os"
	"testing"

	"github.com/csthink/llmlore/internal/config"
	"github.com/csthink/llmlore/internal/model"
	"github.com/csthink/llmlore/internal/stars"
)

// catalogWith builds a tiny discover catalog for the exclude-starred tests.
func catalogWith(ids ...string) *model.Dataset {
	d := &model.Dataset{Meta: model.Meta{SchemaVersion: model.CurrentSchemaVersion}}
	for _, id := range ids {
		d.Repos = append(d.Repos, model.Repo{ID: id})
	}
	d.Meta.Count = len(d.Repos)
	return d
}

func TestApplyExcludeStarred_Disabled(t *testing.T) {
	cat := catalogWith("a/b", "c/d")
	got, err := applyExcludeStarred(&config.Config{ExcludeStarred: false}, cat)
	if err != nil {
		t.Fatalf("applyExcludeStarred: %v", err)
	}
	if got != cat {
		t.Error("with the flag off, the catalog must pass through untouched")
	}
}

func TestApplyExcludeStarred_FiltersViewFromLocalMyStars(t *testing.T) {
	// Point my-stars resolution at an isolated HOME and seed a personal star.
	home := t.TempDir()
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", home)
	if err := stars.Save(stars.DefaultDataPath(os.Getenv), &stars.Dataset{
		Repos: []stars.Repo{{ID: "a/b", Owner: "a", Name: "b"}},
	}); err != nil {
		t.Fatalf("seed my-stars: %v", err)
	}

	cat := catalogWith("a/b", "c/d")
	got, err := applyExcludeStarred(&config.Config{ExcludeStarred: true}, cat)
	if err != nil {
		t.Fatalf("applyExcludeStarred: %v", err)
	}
	if len(got.Repos) != 1 || got.Repos[0].ID != "c/d" {
		t.Errorf("already-starred repo should be excluded from the view, got %+v", got.Repos)
	}
	// The source catalog must be left complete (open dataset never personalized).
	if len(cat.Repos) != 2 {
		t.Errorf("exclude-starred must not mutate the source catalog, got %+v", cat.Repos)
	}
}
