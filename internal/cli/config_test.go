package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/csthink/llmlore/internal/config"
)

// runConfigInit invokes `config init` with args, pointing XDG_CONFIG_HOME at a
// temp dir so the test never touches the real home. It returns combined stdout
// and the resolved config path.
func runConfigInit(t *testing.T, dir string, args ...string) (string, string, error) {
	t.Helper()
	t.Setenv(config.EnvXDGConfigHome, dir)
	path := filepath.Join(dir, "llmlore", "config.toml")

	cmd := newConfigCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"init"}, args...))
	err := cmd.Execute()
	return out.String(), path, err
}

func TestConfigInit_WritesTemplate(t *testing.T) {
	dir := t.TempDir()
	out, path, err := runConfigInit(t, dir)
	if err != nil {
		t.Fatalf("config init: %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("template not written: %v", statErr)
	}
	if !strings.Contains(out, path) {
		t.Errorf("output should name the resolved path:\n%s", out)
	}
	if !strings.Contains(out, config.EnvLLMAPIKey) {
		t.Errorf("output should tell the user to export the key:\n%s", out)
	}
}

func TestConfigInit_RefusesOverwriteThenForce(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runConfigInit(t, dir); err != nil {
		t.Fatalf("first init: %v", err)
	}
	// Second run without --force must fail with a readable message naming --force.
	_, _, err := runConfigInit(t, dir)
	if err == nil {
		t.Fatal("expected refuse-to-overwrite error")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error should suggest --force: %v", err)
	}
	// --force succeeds.
	if _, _, err := runConfigInit(t, dir, "--force"); err != nil {
		t.Errorf("force overwrite: %v", err)
	}
}

func TestNudgeIfPlaceholder(t *testing.T) {
	mk := func() (*cobra.Command, *bytes.Buffer) {
		c := &cobra.Command{}
		var b bytes.Buffer
		c.SetErr(&b)
		c.SetOut(&b)
		return c, &b
	}

	// Placeholder set: a non-fatal English notice is printed.
	c, b := mk()
	nudgeIfPlaceholder(c, &config.Config{LLM: config.LLM{Placeholder: true}})
	if !strings.Contains(b.String(), "placeholder") || !strings.Contains(b.String(), config.EnvLLMAPIKey) {
		t.Errorf("expected placeholder nudge mentioning the key env: %q", b.String())
	}

	// Not a placeholder: silence.
	c, b = mk()
	nudgeIfPlaceholder(c, &config.Config{LLM: config.LLM{Provider: "openai"}})
	if b.String() != "" {
		t.Errorf("expected no nudge for a configured provider: %q", b.String())
	}
}
