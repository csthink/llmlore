package classifier

import (
	"testing"

	"github.com/csthink/llmlore/internal/config"
)

func TestNewSelectsImplementationByConfig(t *testing.T) {
	// No provider/key → heuristic path.
	heuristicCfg := &config.Config{}
	if _, ok := New(heuristicCfg).(*HeuristicClassifier); !ok {
		t.Errorf("New with no key = %T, want *HeuristicClassifier", New(heuristicCfg))
	}

	// Provider + key → LLM path.
	llmCfg := &config.Config{LLM: config.LLM{Provider: "openai", APIKey: "k", Model: "gpt-4o-mini"}}
	if _, ok := New(llmCfg).(*LLMClassifier); !ok {
		t.Errorf("New with key = %T, want *LLMClassifier", New(llmCfg))
	}
}

func TestNewOpenAIProviderDefaults(t *testing.T) {
	p := newOpenAIProvider(config.LLM{APIKey: "k"})
	if p.baseURL != DefaultBaseURL {
		t.Errorf("baseURL = %q, want default %q", p.baseURL, DefaultBaseURL)
	}
	if p.model != DefaultModel {
		t.Errorf("model = %q, want default %q", p.model, DefaultModel)
	}

	// Overrides honored; trailing slash on base_url trimmed.
	p2 := newOpenAIProvider(config.LLM{APIKey: "k", BaseURL: "https://api.deepseek.com/v1/", Model: "deepseek-chat"})
	if p2.baseURL != "https://api.deepseek.com/v1" {
		t.Errorf("baseURL = %q, want trimmed override", p2.baseURL)
	}
	if p2.model != "deepseek-chat" {
		t.Errorf("model = %q, want override", p2.model)
	}
}
