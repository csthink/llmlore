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

func TestPlaceholderProviderStaysHeuristic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := "[llm]\nprovider = \"" + ProviderPlaceholder + "\"\nbase_url = \"https://api.openai.com/v1\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := load(path, envFunc(nil))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.LLM.Provider != "" {
		t.Errorf("placeholder provider must not configure a provider: %q", cfg.LLM.Provider)
	}
	if !cfg.LLM.Placeholder {
		t.Error("placeholder provider must set LLM.Placeholder")
	}
	if cfg.UseLLM() || cfg.ClassifierMode() != model.ClassifiedByHeuristic {
		t.Errorf("placeholder must stay heuristic: UseLLM=%v mode=%q", cfg.UseLLM(), cfg.ClassifierMode())
	}
	// An env provider still overrides the placeholder (env wins over file).
	cfg2, err := load(path, envFunc(map[string]string{EnvLLMProvider: "openai"}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg2.LLM.Provider != "openai" {
		t.Errorf("env provider should override placeholder: %q", cfg2.LLM.Provider)
	}
}

func TestWriteTemplate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "config.toml")

	if err := WriteTemplate(path, false); err != nil {
		t.Fatalf("WriteTemplate: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perm: got %o want 600", perm)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	// The template must carry the sentinel so the placeholder check can fire...
	if !strings.Contains(body, ProviderPlaceholder) {
		t.Error("template missing provider placeholder sentinel")
	}
	// ...and must NEVER contain a secret FIELD (red line). The comments mention
	// "API key" and the LLMLORE_LLM_API_KEY env var deliberately, so we scan only
	// the actual TOML assignments (non-comment, non-blank lines) for a secret key.
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		key, _, ok := strings.Cut(line, "=")
		if ok && looksSecret(strings.TrimSpace(key)) {
			t.Errorf("template must not contain a secret field %q:\n%s", strings.TrimSpace(key), body)
		}
	}
	// A freshly written template must load cleanly and stay heuristic (AC-1). The
	// loader also rejects any secret-looking TOML field, so a clean load is itself
	// proof the template smuggles no secret to disk.
	cfg, err := load(path, envFunc(nil))
	if err != nil {
		t.Fatalf("load written template: %v", err)
	}
	if cfg.UseLLM() || !cfg.LLM.Placeholder {
		t.Errorf("written template should be heuristic + placeholder: %+v", cfg.LLM)
	}

	// Refuse to overwrite without force.
	if err := WriteTemplate(path, false); err != ErrConfigExists {
		t.Errorf("expected ErrConfigExists, got %v", err)
	}
	// Force overwrites.
	if err := WriteTemplate(path, true); err != nil {
		t.Errorf("force overwrite: %v", err)
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
