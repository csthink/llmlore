package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csthink/llmlore/internal/model"
)

// envFunc builds a getenv stub from a map.
func envFunc(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaultsWhenEmpty(t *testing.T) {
	cfg, err := load("", envFunc(nil))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Port != DefaultPort {
		t.Errorf("port: got %d want %d", cfg.Port, DefaultPort)
	}
	if cfg.DataDir != DefaultDataDir {
		t.Errorf("data_dir: got %q want %q", cfg.DataDir, DefaultDataDir)
	}
	if cfg.UseLLM() {
		t.Error("UseLLM should be false with no provider/key")
	}
	if cfg.ClassifierMode() != model.ClassifiedByHeuristic {
		t.Errorf("ClassifierMode: got %q want heuristic", cfg.ClassifierMode())
	}
}

func TestEnvOverridesAndKeyFromEnvOnly(t *testing.T) {
	env := map[string]string{
		EnvLLMProvider:    "anthropic",
		EnvLLMAPIKey:      "sk-secret",
		EnvGitHubToken:    "ghp-secret",
		EnvPort:           "9090",
		EnvDataDir:        "/tmp/llmlore-data",
		EnvExcludeStarred: "true",
	}
	cfg, err := load("", envFunc(env))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.LLM.Provider != "anthropic" || cfg.LLM.APIKey != "sk-secret" {
		t.Errorf("llm from env not applied: %+v", cfg.LLM)
	}
	if cfg.GitHubToken != "ghp-secret" {
		t.Errorf("github token from env not applied: %q", cfg.GitHubToken)
	}
	if cfg.Port != 9090 || cfg.DataDir != "/tmp/llmlore-data" || !cfg.ExcludeStarred {
		t.Errorf("scalar env overrides not applied: %+v", cfg)
	}
	if !cfg.UseLLM() || cfg.ClassifierMode() != model.ClassifiedByLLM {
		t.Errorf("expected LLM mode with provider+key, got UseLLM=%v mode=%q", cfg.UseLLM(), cfg.ClassifierMode())
	}
}

func TestProviderWithoutKeyStaysHeuristic(t *testing.T) {
	cfg, err := load("", envFunc(map[string]string{EnvLLMProvider: "anthropic"}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.UseLLM() {
		t.Error("provider without key must not enable LLM")
	}
}

func TestInvalidPortRejected(t *testing.T) {
	if _, err := load("", envFunc(map[string]string{EnvPort: "70000"})); err == nil {
		t.Error("expected error for out-of-range port")
	}
	if _, err := load("", envFunc(map[string]string{EnvPort: "abc"})); err == nil {
		t.Error("expected error for non-numeric port")
	}
}

func TestTOMLFileNonSecretFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
[llm]
provider = "openai"
base_url = "https://proxy.example.com/v1"
model = "gpt-x"

[server]
port = 8123
data_dir = "/data/x"

[discover]
exclude_starred = true
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := load(path, envFunc(nil))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.LLM.Provider != "openai" || cfg.LLM.BaseURL == "" || cfg.LLM.Model != "gpt-x" {
		t.Errorf("llm fields from file not applied: %+v", cfg.LLM)
	}
	if cfg.Port != 8123 || cfg.DataDir != "/data/x" || !cfg.ExcludeStarred {
		t.Errorf("server/discover fields from file not applied: %+v", cfg)
	}
	// Env must still win over file.
	cfg2, err := load(path, envFunc(map[string]string{EnvPort: "7000"}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg2.Port != 7000 {
		t.Errorf("env should override file port: got %d", cfg2.Port)
	}
}

func TestTOMLFileRejectsSecret(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
[llm]
provider = "anthropic"
api_key = "sk-should-not-be-here"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := load(path, envFunc(nil))
	if err == nil {
		t.Fatal("expected error: a secret in the config file must be rejected")
	}
	if !strings.Contains(err.Error(), "environment variable") {
		t.Errorf("error should steer user to env var: %v", err)
	}
}

func TestRedactedHidesSecrets(t *testing.T) {
	cfg := &Config{GitHubToken: "ghp-secret", LLM: LLM{APIKey: "sk-secret"}}
	r := cfg.Redacted()
	if strings.Contains(r.GitHubToken, "secret") || strings.Contains(r.LLM.APIKey, "secret") {
		t.Errorf("Redacted leaked a secret: %+v", r)
	}
	if r.GitHubToken == "" || r.LLM.APIKey == "" {
		t.Error("Redacted should mark presence, not blank out set secrets")
	}
}

func TestStringFormattingNeverLeaks(t *testing.T) {
	cfg := Config{
		GitHubToken: "ghp-TOPSECRET",
		LLM:         LLM{Provider: "anthropic", APIKey: "sk-TOPSECRET"},
		Port:        7777,
	}
	// Cover both the Config value and a *Config pointer across every verb a
	// caller might reach for, plus formatting the LLM field on its own.
	outputs := []string{
		fmt.Sprintf("%v", cfg),
		fmt.Sprintf("%+v", cfg),
		fmt.Sprintf("%s", cfg),
		fmt.Sprintf("%#v", cfg),
		fmt.Sprintf("%v", &cfg),
		fmt.Sprintf("%+v", &cfg),
		fmt.Sprintf("%#v", &cfg),
		fmt.Sprintf("%+v", cfg.LLM),
		fmt.Sprintf("%#v", cfg.LLM),
		fmt.Sprintf("%v", &cfg.LLM),
	}
	for i, out := range outputs {
		if strings.Contains(out, "TOPSECRET") {
			t.Errorf("output[%d] leaked a secret: %s", i, out)
		}
		// Non-secret context should still be visible for the value/+v forms.
	}
	// Sanity: presence is still indicated, and non-secret fields survive.
	plus := fmt.Sprintf("%+v", cfg)
	if !strings.Contains(plus, "***set***") || !strings.Contains(plus, "anthropic") {
		t.Errorf("expected redaction marker and provider in %%+v output: %s", plus)
	}
}

func TestDefaultConfigPathXDG(t *testing.T) {
	got := DefaultConfigPath(envFunc(map[string]string{EnvXDGConfigHome: "/xdg"}))
	if got != filepath.Join("/xdg", "llmlore", "config.toml") {
		t.Errorf("XDG path: got %q", got)
	}
	got = DefaultConfigPath(envFunc(map[string]string{EnvHome: "/home/u"}))
	if got != filepath.Join("/home/u", ".config", "llmlore", "config.toml") {
		t.Errorf("HOME path: got %q", got)
	}
}
