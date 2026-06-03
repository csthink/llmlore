package classifier

import (
	"context"
	"strings"

	"github.com/csthink/llmlore/internal/collector"
	"github.com/csthink/llmlore/internal/model"
)

// HeuristicClassifier is the zero-key fallback (design §4). It is intentionally
// rough: cheap keyword signals over a candidate's name and description decide
// keep/type/topics. It only rejects the obvious — clearly non-AI projects, raw
// model weights, and pure paper-reproduction code. The strong "framework body
// must not slip in" guarantee belongs to the LLM path (AC-3, a keyed path); a
// framework's own source may well survive this heuristic, and that is accepted.
//
// It never produces a summary: per spec §3 the summary is LLM-generated, so the
// heuristic leaves it empty and downstream rendering (T5) should fall back to
// the description.
type HeuristicClassifier struct{}

// NewHeuristic returns the heuristic classifier.
func NewHeuristic() *HeuristicClassifier { return &HeuristicClassifier{} }

// Classify scores a candidate with keyword heuristics. It never returns an
// error (there is no I/O); the signature satisfies the Classifier interface.
func (HeuristicClassifier) Classify(_ context.Context, c collector.Candidate) (Decision, error) {
	text := normalize(c.Name, c.Description)

	dec := Decision{
		Type:         heuristicType(text),
		Topics:       heuristicTopics(text),
		ClassifiedBy: model.ClassifiedByHeuristic,
	}
	dec.Keep = heuristicKeep(text)
	return dec, nil
}

// normalize builds a single space-padded lowercase haystack from a candidate's
// name and description, with name separators turned into spaces so slug tokens
// ("build-your-own-llm") match as words. Padding lets short tokens like " ai "
// be matched on word boundaries.
func normalize(name, description string) string {
	n := strings.Map(func(r rune) rune {
		switch r {
		case '-', '_', '/', '.':
			return ' '
		default:
			return r
		}
	}, name)
	return " " + strings.ToLower(n+" "+description) + " "
}

// aiSignals mark a repository as being about LLMs/AI at all. Without one, the
// heuristic rejects (clearly unrelated).
var aiSignals = []string{
	"llm", "gpt", "large language model", "language model", " ai ", "a.i.",
	"artificial intelligence", "machine learning", "deep learning", "agent",
	"rag", "prompt", "openai", "anthropic", "claude", "gemini", "mistral",
	"llama", "langchain", "llamaindex", "llama index", "transformer",
	"diffusion", "embedding", "chatbot", "fine-tune", "fine tuning",
	"fine-tuning", "generative", "multimodal", "neural network", "huggingface",
	"hugging face", "semantic search", "copilot", "vector database",
}

// weightSignals mark raw model artifacts (not learning material) → reject.
var weightSignals = []string{
	"model weights", "pretrained weights", "released weights", "model checkpoint",
	"safetensors", " gguf ", ".gguf",
}

// researchSignals mark pure paper-reproduction code → reject, but only when no
// learning marker is present (a tutorial may legitimately cite a paper).
var researchSignals = []string{
	"official implementation", "official pytorch implementation",
	"code for our paper", "code for the paper", "reproduces the paper",
	"reproduce the results",
}

// learningMarkers indicate teaching/resource intent. Used both to choose a type
// and to spare a paper-citing tutorial from the research-exclusion rule.
var learningMarkers = []string{
	"tutorial", "course", "learn", "lesson", "bootcamp", "workshop", "101",
	"from scratch", "step by step", "step-by-step", "hands-on", "hands on",
	"example", "demo", "sample", "showcase", "template", "starter",
	"boilerplate", "scaffold", "guide", "handbook", "cookbook", "awesome",
	"getting started", "best practices", "cheatsheet", "cheat sheet", "roadmap",
}

func heuristicKeep(text string) bool {
	if !containsAny(text, aiSignals...) {
		return false // not about AI at all
	}
	if containsAny(text, weightSignals...) {
		return false // raw model artifacts
	}
	if !containsAny(text, learningMarkers...) && containsAny(text, researchSignals...) {
		return false // pure research / paper reproduction
	}
	return true
}

// heuristicType picks the safest single type from learning markers, defaulting
// to guide — the broadest "getting-started" bucket — when nothing specific hits.
func heuristicType(text string) string {
	switch {
	case containsAny(text, "tutorial", "course", "lesson", "bootcamp", "workshop",
		"101", "from scratch", "step by step", "step-by-step", "hands-on", "hands on", "learn"):
		return model.TypeTutorial
	case containsAny(text, "example", "examples", "demo", "sample", "showcase"):
		return model.TypeExample
	case containsAny(text, "template", "starter", "boilerplate", "scaffold", "prompt collection", "prompts"):
		return model.TypeTemplate
	case containsAny(text, "guide", "handbook", "cookbook", "awesome", "getting started",
		"best practices", "cheatsheet", "cheat sheet", "roadmap"):
		return model.TypeGuide
	default:
		return model.TypeGuide
	}
}

// topicRule maps a controlled topic to the signals that imply it.
type topicRule struct {
	topic   string
	signals []string
}

var topicRules = []topicRule{
	{model.TopicAgent, []string{"agent", "agents", "autonomous", "multi-agent", "multi agent", "autogen", "crewai"}},
	{model.TopicRAG, []string{"rag", "retrieval augmented", "retrieval-augmented", "retrieval", "vector", "embedding", "knowledge base"}},
	{model.TopicMultimodal, []string{"multimodal", "vision", "image", "audio", "speech", "video", "text-to-image", "diffusion", "stable diffusion", "clip", "vlm"}},
	{model.TopicAICoding, []string{"copilot", "ai coding", "coding agent", "code assistant", "code generation", "programming assistant", "pair programming"}},
	{model.TopicEval, []string{"eval", "evaluation", "benchmark", "guardrail", "red team", "red-team"}},
	{model.TopicInfra, []string{"infra", "infrastructure", "deploy", "serving", "inference", "model serving", "gateway", "proxy", "llmops", "mlops", "observability"}},
	{model.TopicPrompt, []string{"prompt", "prompts", "prompt engineering", "system prompt"}},
	{model.TopicLLM, []string{"llm", "gpt", "large language", "language model", "chatbot"}},
}

// heuristicTopics collects every controlled topic whose signals appear, falling
// back to "other" so a Decision always carries at least one valid topic
// (model.Repo.Validate requires >= 1).
func heuristicTopics(text string) []string {
	var topics []string
	for _, r := range topicRules {
		if containsAny(text, r.signals...) {
			topics = append(topics, r.topic)
		}
	}
	if len(topics) == 0 {
		return []string{model.TopicOther}
	}
	return topics
}

// containsAny reports whether haystack contains any of the needles.
func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}
