package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/csthink/llmlore/internal/collector"
	"github.com/csthink/llmlore/internal/config"
	"github.com/csthink/llmlore/internal/model"
	"github.com/csthink/llmlore/internal/store"
)

// defaultDataURL is the canonical pre-generated dataset published from this
// repo's main branch. Overridable via --url or $LLMLORE_DATA_URL (read directly,
// not grown into config — it is a pull-only override).
const defaultDataURL = "https://raw.githubusercontent.com/csthink/llmlore/main/data/repos.json"

// envDataURL overrides the pull source URL.
const envDataURL = "LLMLORE_DATA_URL"

// pullTimeout bounds the dataset download.
const pullTimeout = 30 * time.Second

// newPullCmd builds `llmlore pull`: download the pre-generated open dataset,
// validate its schema_version, and overwrite the local dataset (spec §1, AC-2).
func newPullCmd() *cobra.Command {
	var url string
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Download the pre-generated open dataset",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			return runPull(cmd, cfg, resolvePullURL(url))
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "dataset URL (default: $LLMLORE_DATA_URL or the published dataset)")
	return cmd
}

// resolvePullURL picks the source: --url flag > $LLMLORE_DATA_URL > default.
func resolvePullURL(flagURL string) string {
	if flagURL != "" {
		return flagURL
	}
	if env := os.Getenv(envDataURL); env != "" {
		return env
	}
	return defaultDataURL
}

// runPull downloads the dataset, verifies its schema_version (via model.Load),
// and writes it to the local data path. store.Save runs full validation before
// replacing the file atomically, so a malformed download cannot corrupt the
// local source of truth.
func runPull(cmd *cobra.Command, cfg *config.Config, url string) error {
	logf(cmd, "Pulling dataset from %s ...", url)
	ds, err := fetchDataset(cmd.Context(), url)
	if err != nil {
		return err
	}
	path := dataPath(cfg)
	if err := store.Save(path, ds); err != nil {
		return err
	}
	logf(cmd, "Pulled %d repositories to %s", len(ds.Repos), path)
	return nil
}

// fetchDataset GETs url and decodes it, validating schema_version. Network and
// non-2xx failures are wrapped as upstream errors (exit code 3).
func fetchDataset(ctx context.Context, url string) (*model.Dataset, error) {
	ctx, cancel := context.WithTimeout(ctx, pullTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, &collector.UpstreamError{Op: "pull", Err: err}
	}
	req.Header.Set("User-Agent", "llmlore")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, &collector.UpstreamError{Op: "pull", Err: err}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, &collector.UpstreamError{Op: "pull", Err: fmt.Errorf("read response: %w", err)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &collector.UpstreamError{Op: "pull", Err: fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)}
	}

	// model.Load decodes and checks schema_version (AC-2).
	ds, err := model.Load(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("pulled dataset is invalid: %w", err)
	}
	return ds, nil
}
