package model

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func sampleDataset() Dataset {
	ts := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	return Dataset{
		Meta: Meta{
			SchemaVersion: CurrentSchemaVersion,
			GeneratedAt:   ts,
			Mode:          "historical",
			Count:         1,
		},
		Repos: []Repo{{
			ID:           "owner/name",
			URL:          "https://github.com/owner/name",
			Owner:        "owner",
			Name:         "name",
			Description:  "original description",
			Language:     "Python",
			Stars:        12345,
			Summary:      "one-line summary",
			Type:         TypeTutorial,
			Topics:       []string{TopicLLM, TopicAgent},
			PushedAt:     ts,
			IsStale:      false,
			Source:       SourceSearch,
			AddedAt:      ts,
			ClassifiedBy: ClassifiedByLLM,
			StarSnapshots: []StarSnapshot{
				{T: ts.AddDate(0, -2, 0), Stars: 12000},
				{T: ts, Stars: 12345},
			},
		}},
	}
}

func TestDatasetRoundTrip(t *testing.T) {
	in := sampleDataset()

	var buf bytes.Buffer
	if err := in.Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}

	out, err := Load(&buf)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if out.Meta.SchemaVersion != in.Meta.SchemaVersion {
		t.Errorf("schema_version: got %d want %d", out.Meta.SchemaVersion, in.Meta.SchemaVersion)
	}
	if len(out.Repos) != 1 {
		t.Fatalf("repos: got %d want 1", len(out.Repos))
	}
	got, want := out.Repos[0], in.Repos[0]
	if got.ID != want.ID || got.Stars != want.Stars || got.Type != want.Type {
		t.Errorf("repo scalar mismatch: %+v", got)
	}
	if !got.PushedAt.Equal(want.PushedAt) {
		t.Errorf("pushed_at: got %v want %v", got.PushedAt, want.PushedAt)
	}
	if len(got.StarSnapshots) != 2 || got.StarSnapshots[0].Stars != 12000 {
		t.Errorf("star_snapshots not preserved: %+v", got.StarSnapshots)
	}
}

func TestEncodeRefreshesCount(t *testing.T) {
	d := sampleDataset()
	d.Meta.Count = 999 // stale on purpose
	var buf bytes.Buffer
	if err := d.Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := Load(&buf)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if out.Meta.Count != 1 {
		t.Errorf("count not refreshed: got %d want 1", out.Meta.Count)
	}
}

func TestLoadRejectsUnsupportedSchema(t *testing.T) {
	body := `{"meta":{"schema_version":2},"repos":[]}`
	_, err := Load(strings.NewReader(body))
	if err == nil {
		t.Fatal("expected error for unsupported schema_version")
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Errorf("error should mention schema_version: %v", err)
	}
}

func TestRepoValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Repo)
		wantErr bool
	}{
		{"valid", func(*Repo) {}, false},
		{"empty id", func(r *Repo) { r.ID = "" }, true},
		{"id mismatch", func(r *Repo) { r.ID = "other/name" }, true},
		{"bad type", func(r *Repo) { r.Type = "library" }, true},
		{"no topics", func(r *Repo) { r.Topics = nil }, true},
		{"bad topic", func(r *Repo) { r.Topics = []string{"blockchain"} }, true},
		{"bad source", func(r *Repo) { r.Source = "scraper" }, true},
		{"bad classified_by", func(r *Repo) { r.ClassifiedBy = "human" }, true},
		{"snapshots out of order", func(r *Repo) {
			ts := r.AddedAt
			r.StarSnapshots = []StarSnapshot{
				{T: ts, Stars: 100},
				{T: ts.AddDate(0, -1, 0), Stars: 90}, // earlier than predecessor
			}
		}, true},
		{"snapshots equal timestamps ok", func(r *Repo) {
			ts := r.AddedAt
			r.StarSnapshots = []StarSnapshot{
				{T: ts, Stars: 100},
				{T: ts, Stars: 101}, // equal time is allowed (non-decreasing)
			}
		}, false},
		{"single snapshot ok", func(r *Repo) {
			r.StarSnapshots = []StarSnapshot{{T: r.AddedAt, Stars: 100}}
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := sampleDataset().Repos[0]
			tc.mutate(&r)
			err := r.Validate()
			if tc.wantErr != (err != nil) {
				t.Errorf("Validate() err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestNormalizeID(t *testing.T) {
	if got := NormalizeID("owner", "name"); got != "owner/name" {
		t.Errorf("NormalizeID = %q want owner/name", got)
	}
}
