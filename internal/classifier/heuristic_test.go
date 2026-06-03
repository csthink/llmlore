package classifier

import (
	"context"
	"testing"

	"github.com/csthink/llmlore/internal/collector"
	"github.com/csthink/llmlore/internal/model"
)

func cand(name, desc, lang string) collector.Candidate {
	return collector.Candidate{
		ID: "owner/" + name, Owner: "owner", Name: name,
		Description: desc, Language: lang, Source: model.SourceSearch,
	}
}

func TestHeuristicKeepsLearningResources(t *testing.T) {
	h := NewHeuristic()
	cases := []struct {
		name     string
		cand     collector.Candidate
		wantType string
		wantTop  string // a topic expected to be present
	}{
		{"tutorial", cand("llm-course", "A hands-on tutorial to learn how to build LLM apps", "Python"), model.TypeTutorial, model.TopicLLM},
		{"agent-example", cand("agent-demo", "A runnable demo app showing an autonomous agent with tools", "Python"), model.TypeExample, model.TopicAgent},
		{"rag-guide", cand("rag-handbook", "A getting started guide for retrieval augmented generation with embeddings", "Python"), model.TypeGuide, model.TopicRAG},
		{"prompt-template", cand("prompt-pack", "A collection of prompt templates for ChatGPT", "Markdown"), model.TypeTemplate, model.TopicPrompt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dec, err := h.Classify(context.Background(), tc.cand)
			if err != nil {
				t.Fatalf("Classify: %v", err)
			}
			if !dec.Keep {
				t.Fatalf("Keep = false, want true")
			}
			if dec.Type != tc.wantType {
				t.Errorf("Type = %q, want %q", dec.Type, tc.wantType)
			}
			if !contains(dec.Topics, tc.wantTop) {
				t.Errorf("Topics = %v, want to contain %q", dec.Topics, tc.wantTop)
			}
			if dec.ClassifiedBy != model.ClassifiedByHeuristic {
				t.Errorf("ClassifiedBy = %q, want heuristic", dec.ClassifiedBy)
			}
			if dec.Summary != "" {
				t.Errorf("Summary = %q, want empty on heuristic path", dec.Summary)
			}
		})
	}
}

func TestHeuristicRejectsObvious(t *testing.T) {
	h := NewHeuristic()
	cases := []struct {
		name string
		cand collector.Candidate
	}{
		{"non-ai", cand("awesome-cooking", "A curated tutorial list of recipes and cooking guides", "Markdown")},
		{"model-weights", cand("big-model", "Pretrained LLM model weights in safetensors format", "Python")},
		{"pure-research", cand("paper-code", "Official implementation of our NeurIPS diffusion model paper", "Python")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dec, _ := h.Classify(context.Background(), tc.cand)
			if dec.Keep {
				t.Errorf("Keep = true, want false for %q", tc.name)
			}
		})
	}
}

func TestHeuristicAlwaysProducesValidTopic(t *testing.T) {
	// AI-relevant but with no specific topic signal → must still carry >= 1
	// valid topic so model.Repo.Validate passes downstream.
	h := NewHeuristic()
	dec, _ := h.Classify(context.Background(), cand("ai-starter", "A starter template to learn generative ai", "Python"))
	if len(dec.Topics) == 0 {
		t.Fatal("Topics is empty, want at least one")
	}
	for _, top := range dec.Topics {
		if !model.ValidTopic(top) {
			t.Errorf("topic %q not in controlled vocabulary", top)
		}
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
