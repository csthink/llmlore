package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/csthink/llmlore/internal/classifier"
	"github.com/csthink/llmlore/internal/collector"
	"github.com/csthink/llmlore/internal/config"
)

func TestExitCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"usage", &usageError{errors.New("bad flag")}, 2},
		{"collector upstream", &collector.UpstreamError{Op: "search", Err: errors.New("x")}, 3},
		{"classifier upstream", &classifier.UpstreamError{Op: "llm", Err: errors.New("x")}, 3},
		{"layout drift", &collector.LayoutError{Source: "trending", Detail: "x"}, 3},
		{"missing config", &classifier.MissingConfigError{Op: "llm", Err: errors.New("x")}, 4},
		{"generic", errors.New("something"), 1},
	}
	for _, c := range cases {
		if got := exitCode(c.err); got != c.want {
			t.Errorf("%s: exitCode = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestExitCode_WrappedErrors confirms wrapped typed errors still map correctly
// (exitCode uses errors.As / Is-based predicates).
func TestExitCode_WrappedErrors(t *testing.T) {
	wrapped := fmt.Errorf("pull failed: %w", &collector.UpstreamError{Op: "pull", Err: errors.New("x")})
	if got := exitCode(wrapped); got != 3 {
		t.Errorf("wrapped upstream error: exitCode = %d, want 3", got)
	}
}

// TestLoadDataset_FallsBackToEmbedded points DataDir at an empty dir so the
// on-disk load yields nothing and loadDataset must serve the embedded snapshot.
func TestLoadDataset_FallsBackToEmbedded(t *testing.T) {
	dir := t.TempDir()
	if _, err := os.Stat(filepath.Join(dir, "repos.json")); !os.IsNotExist(err) {
		t.Fatalf("temp dir should have no dataset")
	}
	cfg := &config.Config{DataDir: dir, Port: config.DefaultPort}
	ds, err := loadDataset(cfg)
	if err != nil {
		t.Fatalf("loadDataset: %v", err)
	}
	if len(ds.Repos) == 0 {
		t.Fatal("expected embedded snapshot fallback to provide repos")
	}
}
