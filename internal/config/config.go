// Package config loads llmlore's runtime configuration.
//
// SECURITY (CLAUDE.md red line): API keys and tokens are read ONLY from
// environment variables and held in memory for the duration of a run. They are
// never read from the TOML file, never written to disk, and never logged. The
// TOML decode target deliberately has no field that could hold a secret, and
// loadFile actively rejects a config file that smuggles one in.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/csthink/llmlore/internal/model"
)

// Defaults applied before any file or environment override. See spec §2.
const (
	DefaultPort    = 7777
	DefaultDataDir = "./data"
)

// ProviderPlaceholder is the sentinel `provider` value written by
// `llmlore config init`. An untouched template keeps this value, which the
// loader treats as "not yet configured" (heuristic mode, exactly like an absent
// file) while recording LLM.Placeholder so a server start can nudge the user.
// PROPOSAL-004 (D4).
const ProviderPlaceholder = "REPLACE_ME"

// ConfigTemplate is the scaffold `llmlore config init` writes. It carries only
// non-secret settings: there is deliberately NO api-key field, and the comments
// steer the user to export the key instead (privacy red line). The provider is
// the ProviderPlaceholder sentinel so an untouched file stays in heuristic mode
// and triggers the startup nudge (PROPOSAL-004 §2).
const ConfigTemplate = `# llmlore configuration — non-secret settings only.
# Your API key is NEVER stored here. Export it instead:
#   export LLMLORE_LLM_API_KEY=...

[llm]
# Set ` + "`provider`" + ` to any non-empty name to enable LLM classification + summaries.
# Leaving the REPLACE_ME placeholder keeps llmlore in heuristic mode.
# llmlore speaks the OpenAI-compatible /chat/completions API: point ` + "`base_url`" + `
# at OpenAI, DeepSeek, SiliconFlow, Moonshot, a local Ollama/vLLM, etc.
provider = "REPLACE_ME"
base_url = "https://api.openai.com/v1"
model    = "gpt-4o-mini"

[server]
port = 7777

[discover]
exclude_starred = false
`

// ErrConfigExists is returned by WriteTemplate when the target file already
// exists and force is false, so the caller can craft a readable message.
var ErrConfigExists = errors.New("config file already exists")

// Environment variable names. See spec §2.
const (
	EnvLLMProvider    = "LLMLORE_LLM_PROVIDER"
	EnvLLMAPIKey      = "LLMLORE_LLM_API_KEY"
	EnvGitHubToken    = "LLMLORE_GITHUB_TOKEN"
	EnvPort           = "LLMLORE_PORT"
	EnvDataDir        = "LLMLORE_DATA_DIR"
	EnvExcludeStarred = "LLMLORE_EXCLUDE_STARRED"
	EnvXDGConfigHome  = "XDG_CONFIG_HOME"
	EnvHome           = "HOME"
)

// LLM describes the configured language-model provider. The non-secret fields
// (Provider, BaseURL, Model) may come from the TOML file; APIKey comes ONLY
// from the environment and is never persisted.
type LLM struct {
	Provider string // e.g. "anthropic", "openai"; empty => heuristic fallback
	BaseURL  string // optional custom endpoint for self-hosted / proxy providers
	Model    string // optional model override
	APIKey   string // from env only; never read from file, never logged
	// Placeholder is true when the config file still holds the `config init`
	// provider sentinel (ProviderPlaceholder). It does NOT enable LLM (provider
	// stays empty); it only lets a server start nudge the user. PROPOSAL-004.
	Placeholder bool
}

// Config is the fully-resolved runtime configuration.
type Config struct {
	GitHubToken    string // from env only; raises rate limits / enables private stars
	LLM            LLM
	Port           int
	DataDir        string
	ExcludeStarred bool
}

// UseLLM reports whether LLM classification is available. Without both a
// provider and a key, llmlore degrades to heuristic classification.
func (c *Config) UseLLM() bool {
	return c.LLM.Provider != "" && c.LLM.APIKey != ""
}

// ClassifierMode returns the classification origin to record on produced
// records: model.ClassifiedByLLM when a provider+key are present, otherwise
// model.ClassifiedByHeuristic (the degraded path).
func (c *Config) ClassifierMode() string {
	if c.UseLLM() {
		return model.ClassifiedByLLM
	}
	return model.ClassifiedByHeuristic
}

// Redacted returns a copy with every secret replaced by a presence marker.
// Logging a Config directly is already safe (see String/GoString); use Redacted
// when you need the redacted value as data rather than a formatted string.
func (c *Config) Redacted() Config {
	out := *c
	out.GitHubToken = redact(c.GitHubToken)
	out.LLM.APIKey = redact(c.LLM.APIKey)
	return out
}

// String makes "%v", "%+v", and "%s" on a Config log-safe by default: secrets
// are redacted. The `plain` alias strips Config's own Stringer/GoStringer so the
// formatting below does not recurse back into these methods.
func (c Config) String() string {
	type plain Config
	return fmt.Sprintf("%+v", plain(c.Redacted()))
}

// GoString makes "%#v" on a Config log-safe too.
func (c Config) GoString() string {
	type plain Config
	return fmt.Sprintf("%#v", plain(c.Redacted()))
}

// String makes "%v"/"%+v"/"%s" on a standalone LLM log-safe (APIKey redacted),
// covering callers that format the LLM field directly rather than the Config.
func (l LLM) String() string {
	type plain LLM
	l.APIKey = redact(l.APIKey)
	return fmt.Sprintf("%+v", plain(l))
}

// GoString makes "%#v" on a standalone LLM log-safe too.
func (l LLM) GoString() string {
	type plain LLM
	l.APIKey = redact(l.APIKey)
	return fmt.Sprintf("%#v", plain(l))
}

func redact(s string) string {
	if s == "" {
		return ""
	}
	return "***set***"
}

// fileConfig mirrors the optional ~/.config/llmlore/config.toml. It intentionally
// contains NO field for an API key or token: secrets must come from the
// environment, never from a file that could be committed.
type fileConfig struct {
	LLM struct {
		Provider string `toml:"provider"`
		BaseURL  string `toml:"base_url"`
		Model    string `toml:"model"`
	} `toml:"llm"`
	Server struct {
		Port    int    `toml:"port"`
		DataDir string `toml:"data_dir"`
	} `toml:"server"`
	Discover struct {
		ExcludeStarred bool `toml:"exclude_starred"`
	} `toml:"discover"`
}

// Load resolves configuration from the default TOML path (if present) and the
// process environment. Environment values override file values.
func Load() (*Config, error) {
	return load(DefaultConfigPath(os.Getenv), os.Getenv)
}

// DefaultConfigPath returns ~/.config/llmlore/config.toml, honouring
// XDG_CONFIG_HOME. getenv is injected for testability.
func DefaultConfigPath(getenv func(string) string) string {
	base := getenv(EnvXDGConfigHome)
	if base == "" {
		base = filepath.Join(getenv(EnvHome), ".config")
	}
	return filepath.Join(base, "llmlore", "config.toml")
}

// load is the testable core: it reads the file at tomlPath (absent is fine),
// then applies environment overrides via getenv.
func load(tomlPath string, getenv func(string) string) (*Config, error) {
	cfg := &Config{
		Port:    DefaultPort,
		DataDir: DefaultDataDir,
	}

	if err := applyFile(cfg, tomlPath); err != nil {
		return nil, err
	}
	if err := applyEnv(cfg, getenv); err != nil {
		return nil, err
	}
	return cfg, nil
}

func applyFile(cfg *Config, path string) error {
	if path == "" {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil // optional file
		}
		return fmt.Errorf("read config %s: %w", path, err)
	}

	var fc fileConfig
	md, err := toml.DecodeFile(path, &fc)
	if err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	// Defend the red line: a key/token/secret in a committable file is rejected.
	for _, key := range md.Undecoded() {
		if looksSecret(key.String()) {
			return fmt.Errorf("config %s: %q must not live in a config file; set it via an environment variable instead", path, key.String())
		}
	}

	if fc.LLM.Provider == ProviderPlaceholder {
		// An untouched `config init` template: treat provider as unconfigured
		// (heuristic, exactly like an absent file) but remember to nudge on a
		// server start. An env LLMLORE_LLM_PROVIDER can still override later.
		cfg.LLM.Placeholder = true
	} else if fc.LLM.Provider != "" {
		cfg.LLM.Provider = fc.LLM.Provider
	}
	if fc.LLM.BaseURL != "" {
		cfg.LLM.BaseURL = fc.LLM.BaseURL
	}
	if fc.LLM.Model != "" {
		cfg.LLM.Model = fc.LLM.Model
	}
	if fc.Server.Port != 0 {
		cfg.Port = fc.Server.Port
	}
	if fc.Server.DataDir != "" {
		cfg.DataDir = fc.Server.DataDir
	}
	cfg.ExcludeStarred = fc.Discover.ExcludeStarred
	return nil
}

func applyEnv(cfg *Config, getenv func(string) string) error {
	if v := getenv(EnvLLMProvider); v != "" {
		cfg.LLM.Provider = v
	}
	if v := getenv(EnvLLMAPIKey); v != "" {
		cfg.LLM.APIKey = v
	}
	if v := getenv(EnvGitHubToken); v != "" {
		cfg.GitHubToken = v
	}
	if v := getenv(EnvDataDir); v != "" {
		cfg.DataDir = v
	}
	if v := getenv(EnvPort); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p <= 0 || p > 65535 {
			return fmt.Errorf("%s: invalid port %q", EnvPort, v)
		}
		cfg.Port = p
	}
	if v := getenv(EnvExcludeStarred); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("%s: invalid boolean %q", EnvExcludeStarred, v)
		}
		cfg.ExcludeStarred = b
	}
	return nil
}

// WriteTemplate scaffolds ConfigTemplate at path, creating the parent directory
// 0700 and the file 0600. It refuses to overwrite an existing file unless force
// is true, returning ErrConfigExists so the caller can craft a readable message.
// It writes no secret to disk (the template has no key field). PROPOSAL-004 §1.
func WriteTemplate(path string, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return ErrConfigExists
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("check config %s: %w", path, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(ConfigTemplate), 0o600); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}

// looksSecret reports whether a config key name looks like it carries a secret.
func looksSecret(key string) bool {
	k := strings.ToLower(key)
	for _, marker := range []string{"key", "token", "secret", "password"} {
		if strings.Contains(k, marker) {
			return true
		}
	}
	return false
}
