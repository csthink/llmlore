package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/csthink/llmlore/internal/collector"
)

const validDataset = `{"meta":{"schema_version":1,"generated_at":"2026-06-01T00:00:00Z","mode":"historical","count":1},
"repos":[{"id":"a/b","url":"https://github.com/a/b","owner":"a","name":"b","description":"",
"language":"Go","stars":10,"summary":"","type":"tutorial","topics":["llm"],
"pushed_at":"2026-05-01T00:00:00Z","is_stale":false,"source":"search","added_at":"2026-05-01T00:00:00Z",
"classified_by":"heuristic","star_snapshots":[{"t":"2026-05-01T00:00:00Z","stars":10}]}]}`

func TestFetchDataset_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(validDataset))
	}))
	defer srv.Close()

	ds, err := fetchDataset(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetchDataset: %v", err)
	}
	if len(ds.Repos) != 1 || ds.Repos[0].ID != "a/b" {
		t.Errorf("unexpected dataset: %+v", ds.Repos)
	}
}

func TestFetchDataset_BadStatusIsUpstream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := fetchDataset(context.Background(), srv.URL)
	if !collector.IsUpstream(err) {
		t.Fatalf("a 404 should map to an upstream error (exit 3), got %v", err)
	}
}

func TestFetchDataset_WrongSchemaRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"meta":{"schema_version":999},"repos":[]}`))
	}))
	defer srv.Close()

	if _, err := fetchDataset(context.Background(), srv.URL); err == nil {
		t.Fatal("a dataset with an unsupported schema_version must be rejected (AC-2)")
	}
}

func TestResolvePullURL(t *testing.T) {
	if got := resolvePullURL("https://example.com/x.json"); got != "https://example.com/x.json" {
		t.Errorf("flag should win, got %q", got)
	}
	t.Setenv(envDataURL, "https://env.example/data.json")
	if got := resolvePullURL(""); got != "https://env.example/data.json" {
		t.Errorf("env should be used when no flag, got %q", got)
	}
	t.Setenv(envDataURL, "")
	if got := resolvePullURL(""); got != defaultDataURL {
		t.Errorf("default should be used, got %q", got)
	}
}
