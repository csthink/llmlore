package classifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/csthink/llmlore/internal/collector"
	"github.com/csthink/llmlore/internal/config"
	"github.com/csthink/llmlore/internal/model"
)

// Provider is the pluggable LLM seam. The OpenAI-compatible chat provider below
// is the only implementation today (it covers OpenAI, DeepSeek, SiliconFlow,
// Moonshot, local Ollama/vLLM, … via base_url); an Anthropic-native provider
// can be added behind this interface when needed, without touching the
// classification logic.
type Provider interface {
	// Complete sends a system+user prompt and returns the model's text reply.
	// Transport failures must be *UpstreamError; credential rejections (401/403)
	// must be *MissingConfigError, so the CLI can pick exit 3 vs 4.
	Complete(ctx context.Context, system, user string) (string, error)
}

// LLMClassifier judges a candidate with an LLM, falling back to the heuristic
// for a single candidate whose reply cannot be parsed (so one bad reply never
// empties a run). A provider-level error (transport/credentials) is fatal and
// returned as-is.
type LLMClassifier struct {
	provider Provider
	fallback *HeuristicClassifier
}

// NewLLM builds an LLM classifier over the given provider.
func NewLLM(p Provider) *LLMClassifier {
	return &LLMClassifier{provider: p, fallback: NewHeuristic()}
}

// classifySystemPrompt encodes the keep/reject rules (spec §5) and the
// controlled vocabulary (spec §4). The summary must be a single English
// sentence (red line: user-facing text is English).
const classifySystemPrompt = `You classify GitHub repositories for a curated catalog of resources that teach people how to USE large language models and AI.

Decide whether to KEEP a repository and, if kept, label it.

KEEP when its core purpose is teaching or helping someone get hands-on with LLMs/AI, fitting exactly one of these types:
- tutorial: a course, tutorial, or learning path
- example: a runnable example app or demo
- template: a collection of prompt/agent/workflow templates
- guide: a getting-started guide for a tool or framework

DO NOT KEEP:
- the source code of a framework or library itself (e.g. transformers, pytorch)
- model weights or checkpoints
- pure research or paper-reproduction code
- projects unrelated to AI
Boundary: the transformers library itself is NOT kept; a tutorial teaching how to use transformers IS kept.

Choose exactly one "type" from: tutorial, example, template, guide.
Choose one or more "topics" from: llm, agent, rag, multimodal, ai-coding, eval, infra, prompt, other.
Write "summary" as one English sentence (no other language).

Respond with ONLY a JSON object, no markdown fences, of this shape:
{"keep": true, "type": "tutorial", "topics": ["llm","agent"], "summary": "..."}
When keep is false, the other fields may be empty.`

// Classify asks the provider to judge the candidate, parses and sanitizes the
// reply, and degrades to the heuristic for an unusable reply.
func (l *LLMClassifier) Classify(ctx context.Context, c collector.Candidate) (Decision, error) {
	reply, err := l.provider.Complete(ctx, classifySystemPrompt, buildUserPrompt(c))
	if err != nil {
		// Transport / credential failure: fatal, surfaced for exit-code mapping.
		return Decision{}, err
	}

	dec, ok := parseDecision(reply)
	if !ok {
		// Got a reply, but it was unusable. Degrade THIS candidate to the
		// heuristic; ClassifiedBy=heuristic lets T6 count systemic fallbacks.
		return l.fallback.Classify(ctx, c)
	}
	return dec, nil
}

// buildUserPrompt presents the candidate's observable facts.
func buildUserPrompt(c collector.Candidate) string {
	return fmt.Sprintf("Repository: %s\nDescription: %s\nPrimary language: %s\nStars: %d\nURL: %s",
		c.ID, dash(c.Description), dash(c.Language), c.Stars, c.URL)
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none)"
	}
	return s
}

// llmReply is the JSON contract the model is asked to return.
type llmReply struct {
	Keep    *bool    `json:"keep"`
	Type    string   `json:"type"`
	Topics  []string `json:"topics"`
	Summary string   `json:"summary"`
}

// parseDecision validates and sanitizes a model reply into a Decision. ok is
// false when the reply is unusable (not JSON, missing keep, or — for a kept
// repo — an invalid type), signaling the caller to fall back. Out-of-vocabulary
// topics are coerced (dropped; "other" if none remain) rather than rejected.
func parseDecision(reply string) (Decision, bool) {
	var r llmReply
	if err := json.Unmarshal([]byte(extractJSON(reply)), &r); err != nil {
		return Decision{}, false
	}
	if r.Keep == nil {
		return Decision{}, false // ambiguous verdict
	}
	if !*r.Keep {
		return Decision{Keep: false, ClassifiedBy: model.ClassifiedByLLM}, true
	}

	typ := strings.ToLower(strings.TrimSpace(r.Type))
	if !model.ValidType(typ) {
		return Decision{}, false // invalid type counts as an unusable reply
	}
	return Decision{
		Keep:         true,
		Type:         typ,
		Topics:       coerceTopics(r.Topics),
		Summary:      strings.TrimSpace(r.Summary),
		ClassifiedBy: model.ClassifiedByLLM,
	}, true
}

// coerceTopics keeps only in-vocabulary topics (deduped), substituting "other"
// when nothing valid remains, so a Decision always carries a valid topic.
func coerceTopics(raw []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range raw {
		t = strings.ToLower(strings.TrimSpace(t))
		if model.ValidTopic(t) && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return []string{model.TopicOther}
	}
	return out
}

// extractJSON pulls the JSON object out of a reply, tolerating markdown fences
// or surrounding prose by slicing from the first "{" to the last "}".
func extractJSON(s string) string {
	i := strings.IndexByte(s, '{')
	j := strings.LastIndexByte(s, '}')
	if i >= 0 && j > i {
		return s[i : j+1]
	}
	return s
}

// --- OpenAI-compatible chat provider ---------------------------------------

// Defaults for the OpenAI-compatible provider. The model defaults to a cheap
// one: classification is a high-frequency, low-stakes task.
const (
	DefaultBaseURL = "https://api.openai.com/v1"
	DefaultModel   = "gpt-4o-mini"
)

// chatProvider talks to an OpenAI-compatible /chat/completions endpoint. The
// API key lives only in memory (red line) and is sent as a Bearer header, never
// logged.
type chatProvider struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	model      string
	maxRetries int
	baseDelay  time.Duration
	sleep      func(time.Duration) // injectable for fast tests
}

// newOpenAIProvider builds the provider from LLM config, applying base_url and
// model defaults.
func newOpenAIProvider(cfg config.LLM) *chatProvider {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	mdl := strings.TrimSpace(cfg.Model)
	if mdl == "" {
		mdl = DefaultModel
	}
	return &chatProvider{
		httpClient: &http.Client{Timeout: 60 * time.Second},
		baseURL:    base,
		apiKey:     cfg.APIKey,
		model:      mdl,
		maxRetries: 2,
		baseDelay:  500 * time.Millisecond,
		sleep:      time.Sleep,
	}
}

type chatRequest struct {
	Model       string        `json:"model"`
	Temperature float64       `json:"temperature"`
	Messages    []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Complete posts a chat completion, retrying transient failures (transport,
// 429, 5xx) with exponential backoff before giving up. A 401/403 fails fast as
// *MissingConfigError; exhausted retries surface as *UpstreamError.
func (p *chatProvider) Complete(ctx context.Context, system, user string) (string, error) {
	payload, err := json.Marshal(chatRequest{
		Model:       p.model,
		Temperature: 0,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	})
	if err != nil {
		return "", &UpstreamError{Op: opChat, Err: fmt.Errorf("encode request: %w", err)}
	}

	var lastErr error
	for attempt := 0; attempt <= p.maxRetries; attempt++ {
		if attempt > 0 {
			p.sleep(backoff(p.baseDelay, attempt))
		}
		text, retryable, err := p.doOnce(ctx, payload)
		if err == nil {
			return text, nil
		}
		lastErr = err
		if !retryable {
			return "", err
		}
	}
	return "", lastErr // retries exhausted; lastErr is already an *UpstreamError
}

const opChat = "llm chat completion"

// doOnce performs a single request. retryable is true for transient failures
// (transport, 429, 5xx) the caller may retry.
func (p *chatProvider) doOnce(ctx context.Context, payload []byte) (text string, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", false, &UpstreamError{Op: opChat, Err: fmt.Errorf("build request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", true, &UpstreamError{Op: opChat, Err: err} // transport: retry
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return "", false, &MissingConfigError{Op: opChat,
			Err: fmt.Errorf("provider rejected credentials (status %d): %s", resp.StatusCode, providerMessage(body))}
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return "", true, &UpstreamError{Op: opChat,
			Err: fmt.Errorf("transient status %d: %s", resp.StatusCode, providerMessage(body))}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return "", false, &UpstreamError{Op: opChat,
			Err: fmt.Errorf("unexpected status %d: %s", resp.StatusCode, providerMessage(body))}
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", false, &UpstreamError{Op: opChat, Err: fmt.Errorf("decode response: %w", err)}
	}
	if len(parsed.Choices) == 0 {
		return "", false, &UpstreamError{Op: opChat, Err: fmt.Errorf("response contained no choices")}
	}
	return parsed.Choices[0].Message.Content, false, nil
}

// backoff returns the delay before retry attempt n (n >= 1): baseDelay * 2^(n-1).
func backoff(base time.Duration, attempt int) time.Duration {
	d := base
	for i := 1; i < attempt; i++ {
		d *= 2
	}
	return d
}

// providerMessage extracts an OpenAI-style {"error":{"message":...}} or falls
// back to a truncated raw body, so the surfaced error is actionable.
func providerMessage(body []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
